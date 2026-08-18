package content

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/chmouel/liseur-sync/internal/store"
)

// This file is the only place in the server that creates a file under a
// folder root, and it exists because of ADR-0023. Everything else here
// opens read-only and refuses symlinks, and that has not changed: a
// folder the administrator did not mark as accepting uploads is still
// untouchable, and even one that did is only ever *added* to. Nothing
// here modifies, renames or removes a file that was already there.

// ErrUploadsRefused is a folder that was not marked as accepting
// uploads. It is checked at the handler edge too; having it here means
// the guarantee does not depend on the caller remembering.
var ErrUploadsRefused = errors.New("content: folder does not accept uploads")

// ErrNameExhausted is a filename whose every collision candidate was
// taken. It means somebody is uploading the same title repeatedly, and
// answering is better than looping.
var ErrNameExhausted = errors.New("content: no free filename")

// maxNameAttempts bounds the " (2)", " (3)" walk. A hundred copies of
// one title is not a case worth serving.
const maxNameAttempts = 100

// Place writes a publication into a folder that accepts uploads and
// answers with its path relative to the root.
//
// The bytes come from a file the caller already validated somewhere
// outside every folder root, so a partial transfer was never visible to
// a pass. What happens here is the last step: create a file that was not
// there, with O_EXCL so the kernel rather than a stat guarantees it, and
// copy the bytes in. On any failure the new file is removed, because a
// half-copied EPUB under a watched root is litter that every future pass
// would trip over.
func Place(
	ctx context.Context, folder store.Folder, src io.Reader, base string,
) (string, error) {
	if !folder.AcceptsUploads {
		return "", ErrUploadsRefused
	}
	root, err := os.OpenRoot(folder.RootPath)
	if err != nil {
		return "", fmt.Errorf("content: open root: %w", err)
	}
	defer root.Close()

	name := SanitizeBookFilename(base)
	for attempt := range maxNameAttempts {
		candidate := name + ".epub"
		if attempt > 0 {
			candidate = name + " (" + strconv.Itoa(attempt+1) + ").epub"
		}
		file, err := root.OpenFile(candidate,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("content: create %q: %w", candidate, err)
		}
		if err := copyInto(ctx, file, src); err != nil {
			file.Close()
			_ = root.Remove(candidate)
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = root.Remove(candidate)
			return "", fmt.Errorf("content: close %q: %w", candidate, err)
		}
		return candidate, nil
	}
	return "", ErrNameExhausted
}

// copyInto streams and then syncs. The sync is what makes the file
// durable before a pass is told to go and look at it: a reconcile that
// read a file the page cache had not written yet would record a size and
// mtime the disk does not agree with.
func copyInto(ctx context.Context, dst *os.File, src io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("content: write publication: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("content: sync publication: %w", err)
	}
	return nil
}

// SanitizeBookFilename turns whatever a publication calls itself into a
// filename this server is willing to create.
//
// It is deliberately strict rather than clever. A name is a convenience
// — the catalog reads the file's own metadata afterwards and does not
// care what the file is called — so anything doubtful is replaced rather
// than escaped, and a name that survives to nothing becomes "book".
func SanitizeBookFilename(name string) string {
	name = strings.TrimSuffix(name, ".epub")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0:
			b.WriteRune('-')
		case unicode.IsControl(r):
			// Dropped rather than replaced: a control character is not
			// a character somebody meant to type.
		case r < 0x20:
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".")
	out = strings.TrimSpace(out)
	if out == "" {
		return "book"
	}
	// A long name is a real failure on most filesystems, and 120 bytes
	// leaves room for a collision suffix and the extension inside the
	// usual 255-byte limit.
	if len(out) > 120 {
		out = strings.ToValidUTF8(out[:120], "")
		out = strings.TrimSpace(out)
		if out == "" {
			return "book"
		}
	}
	return out
}

// BookFilenameFrom builds the base filename for an uploaded publication:
// "Author - Title" where both are known, and whatever is known where one
// is not. The uploaded filename is the fallback, because a publication
// with no usable metadata still came from somewhere with a name.
func BookFilenameFrom(title, author, uploaded string) string {
	title = strings.TrimSpace(title)
	author = strings.TrimSpace(author)
	switch {
	case title != "" && author != "":
		return author + " - " + title
	case title != "":
		return title
	case author != "":
		return author
	default:
		return path.Base(strings.TrimSpace(uploaded))
	}
}

// Ingester places a publication and then reconciles the folder it went
// into, which is the whole of ADR-0023's write path: the catalog is
// still written by a pass and only by a pass, so an uploaded book and a
// book somebody copied in by hand are the same kind of thing.
type Ingester struct{ rec *Reconciler }

// NewIngester wraps the reconciler the watcher already uses. A pass is
// idempotent, so the two firing at once is not a problem worth locking
// against.
func NewIngester(rec *Reconciler) *Ingester { return &Ingester{rec: rec} }

// Ingest writes the publication into the folder and reads the folder
// back.
//
// The reconcile error is deliberately not returned. By the time it runs
// the bytes are on the disk and durable, so a pass that failed — or that
// rules 1 and 2 stopped from concluding anything — has not lost the
// upload. The watcher will come back to it. What the caller needs to
// know is whether the file landed, and that is what the error says.
func (i *Ingester) Ingest(
	ctx context.Context, folder store.Folder, src io.Reader, base string,
) (string, error) {
	relative, err := Place(ctx, folder, src, base)
	if err != nil {
		return "", err
	}
	_, _ = i.rec.Reconcile(ctx, folder)
	return relative, nil
}
