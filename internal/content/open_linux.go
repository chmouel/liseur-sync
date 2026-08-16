//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// OpenBook opens a catalog book's file under its folder's root.
//
// This is the single read chokepoint: a download, a cover render and the
// reader all arrive here, so the rules about reading somebody else's
// directory are written once.
//
// Everything treats the root as a directory this server does not own.
// The open goes through os.Root, so a path that climbs out of the folder
// — or a symlink pointing out of it — fails rather than reads. The size
// and modification time are compared against what the last pass saw,
// because the alternative is serving whatever bytes happen to sit at
// that path now under the title of the book that used to be there. A
// mismatch is not a corruption to repair; it is a file its owner
// changed, and the next pass re-reads it.
//
// The caller owns the returned file and must close it.
func OpenBook(
	ctx context.Context, rootPath string, book store.CatalogBook,
) (*os.File, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	opened, info, err := openUnderRoot(rootPath, book.RelativePath)
	if err != nil {
		return nil, 0, err
	}
	if !fileMatches(book, info) {
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	return opened, info.Size(), nil
}

// OpenBookCover opens the cover a folder's curator chose for this book —
// Calibre's cover.jpg — which lives beside the publication rather than
// inside it.
//
// The bytes are bounded and then proved by digest rather than by stat. A
// cover has no snapshot of its own to compare against and is small
// enough to hash on the way out, so a cover.jpg somebody replaced fails
// here and falls back to the publication's own until the next pass
// records the new one, rather than being served under a cache key that
// names the old image.
func OpenBookCover(
	ctx context.Context, rootPath string, book store.CatalogBook,
) (*os.File, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if book.CoverSHA256 == "" || book.CoverRelativePath == nil {
		return nil, 0, ErrStageMissing
	}
	opened, info, err := openUnderRoot(rootPath, *book.CoverRelativePath)
	if err != nil {
		return nil, 0, err
	}
	if info.Size() > MaxCoverBytes {
		// A pass only ever records a cover within this bound, so anything
		// larger is a file somebody swapped afterwards. Refusing it
		// before the hash is what stops a request reading an arbitrary
		// amount of their disk.
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(opened, MaxCoverBytes)); err != nil {
		opened.Close()
		return nil, 0, err
	}
	if hex.EncodeToString(digest.Sum(nil)) != book.CoverSHA256 {
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	if _, err := opened.Seek(0, io.SeekStart); err != nil {
		opened.Close()
		return nil, 0, err
	}
	return opened, info.Size(), nil
}

// openUnderRoot is the only way this package opens a file beneath a
// watched folder. Read-only, rooted, and refusing symlinks and every
// file type that is not a regular file.
func openUnderRoot(rootPath, relative string) (*os.File, os.FileInfo, error) {
	if rootPath == "" {
		return nil, nil, ErrRootMissing
	}
	if !SafeRelativePath(relative) {
		return nil, nil, ErrUnsafePath
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s: %v", ErrRootMissing, rootPath, err)
	}
	defer root.Close()

	// Lstat first: os.Root would follow a symlink that stays inside the
	// folder, and a pass skips symlinks by policy, so one appearing here
	// is a path the catalog never validated.
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrStageMissing
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, ErrUnsafePath
	}
	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrStageMissing
	}
	if err != nil {
		return nil, nil, err
	}
	// Stat the descriptor rather than trust the lstat: between the two
	// calls the path could have been replaced, and it is the file that
	// will actually be read that has to be checked.
	opened2, err := opened.Stat()
	if err != nil {
		opened.Close()
		return nil, nil, err
	}
	if !opened2.Mode().IsRegular() {
		opened.Close()
		return nil, nil, ErrUnsafePath
	}
	return opened, opened2, nil
}

// fileMatches is the cheap proof that a file is still the one that was
// scanned. It is not a digest: re-hashing every download would read the
// file twice and turn a range request into a full read. Re-hashing is
// what the pass is for.
func fileMatches(book store.CatalogBook, info os.FileInfo) bool {
	if book.SizeBytes > 0 && info.Size() != book.SizeBytes {
		return false
	}
	if book.MTime.IsZero() {
		return true
	}
	// Filesystems and databases disagree about sub-second precision, so
	// the comparison is to the second.
	return info.ModTime().UTC().Truncate(time.Second).
		Equal(book.MTime.UTC().Truncate(time.Second))
}

// SafeRelativePath refuses anything a pass would never have recorded: an
// absolute path, a parent reference, or an empty segment. os.Root would
// refuse most of them too, but a path that cannot be right should not
// reach the filesystem at all.
func SafeRelativePath(relative string) bool {
	if relative == "" || strings.HasPrefix(relative, "/") {
		return false
	}
	if relative != path.Clean(relative) {
		return false
	}
	for segment := range strings.SplitSeq(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
