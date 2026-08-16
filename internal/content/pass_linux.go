//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Catalog is the store surface one pass needs. It is an interface rather
// than the concrete store so a test can drive a pass without a database
// and so the pass cannot reach anything else.
type Catalog interface {
	BooksInFolder(ctx context.Context, folderID string) ([]store.KnownBook, error)
	ReconcileFolder(
		ctx context.Context, folderID string, observed []store.ObservedBook,
		complete bool, at time.Time,
	) (store.ReconcileResult, error)
}

// Reconciler runs passes over folders. It holds no per-folder state:
// running a pass twice is running it once, which is what lets the
// watcher fire one whenever it likes.
type Reconciler struct {
	catalog Catalog
	limits  ScanLimits
	epub    epub.Limits
	log     *slog.Logger
	now     func() time.Time
}

// NewReconciler builds a reconciler. now is injectable because the
// tests assert on seen_at and absent_at.
func NewReconciler(
	catalog Catalog, scan ScanLimits, epubLimits epub.Limits, log *slog.Logger,
) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		catalog: catalog,
		limits:  scan.withDefaults(),
		epub:    epubLimits,
		log:     log,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Reconcile runs one pass over one folder and writes the result.
func (r *Reconciler) Reconcile(
	ctx context.Context, folder store.Folder,
) (store.ReconcileResult, error) {
	if folder.Kind == store.FolderCalibre {
		return r.reconcileCalibre(ctx, folder)
	}
	return r.reconcilePlain(ctx, folder)
}

// reconcilePlain walks the tree, re-reads what changed, and hands the
// whole picture to the store in one call.
//
// The honesty of `complete` is the load-bearing part. A scan that could
// not read a directory, hit a bound or failed to read a file reports
// false, and the store then refuses to conclude that anything is absent
// — because a pass that did not see everything has no opinion about what
// it did not see.
func (r *Reconciler) reconcilePlain(
	ctx context.Context, folder store.Folder,
) (store.ReconcileResult, error) {
	report, err := ScanWatchedRoot(ctx, folder.RootPath, r.limits)
	if err != nil {
		return store.ReconcileResult{}, err
	}

	known, err := r.catalog.BooksInFolder(ctx, folder.ID)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	byPath := make(map[string]store.KnownBook, len(known))
	for _, b := range known {
		byPath[b.RelativePath] = b
	}

	series := inferSeries(report.Files)
	observed := make([]store.ObservedBook, 0, len(report.Files))
	complete := report.Complete

	for _, file := range report.Files {
		if err := ctx.Err(); err != nil {
			return store.ReconcileResult{}, err
		}
		prior, hadPrior := byPath[file.RelativePath]
		if hadPrior && unchanged(prior, file) {
			// The file has not moved and its stat has not changed, so
			// re-reading it would produce the metadata already stored.
			// The observation is still recorded, because being seen is
			// what keeps a book out of the missing list.
			observed = append(observed, seenAsBefore(prior, file))
			continue
		}
		shelved, inSeries := series[file.RelativePath]
		obs, err := r.readBook(ctx, folder.RootPath, file, inSeries)
		if err != nil {
			// One unreadable or unparseable file is not a reason to
			// forget the rest of the folder — but it does mean this pass
			// did not see everything, so it may not conclude anything is
			// gone.
			r.log.Warn("skipping unreadable book",
				"folder", folder.ID, "path", file.RelativePath, "error", err)
			complete = false
			continue
		}
		// Rule 4: bytes that changed at a path are a different book, not
		// the same book with new contents.
		obs.Replaces = hadPrior && prior.ContentSHA256 != obs.ContentSHA256
		if inSeries {
			obs.Series = append(obs.Series, shelved)
		}
		observed = append(observed, obs)
	}

	return r.catalog.ReconcileFolder(ctx, folder.ID, observed, complete, r.now())
}

// unchanged is the stat gate. Size and modification time are what a
// filesystem gives cheaply, and a file whose bytes changed without
// either moving is a case the next full re-read catches rather than one
// worth hashing every file on every pass to detect.
func unchanged(prior store.KnownBook, file ScannedFile) bool {
	return prior.SizeBytes == file.SizeBytes &&
		prior.MTime.UTC().Truncate(time.Second).
			Equal(file.ModifiedAt.UTC().Truncate(time.Second))
}

// seenAsBefore records an unchanged file without re-reading it. Only the
// identity and the stat are carried: the store leaves the metadata and
// relations of an unchanged book alone.
func seenAsBefore(prior store.KnownBook, file ScannedFile) store.ObservedBook {
	return store.ObservedBook{
		RelativePath:  file.RelativePath,
		SizeBytes:     file.SizeBytes,
		MTime:         file.ModifiedAt,
		ContentSHA256: prior.ContentSHA256,
		Unchanged:     true,
	}
}

// readBook opens one publication and turns it into an observation:
// digest, embedded metadata, and whatever the filename says that the
// file itself did not.
func (r *Reconciler) readBook(
	ctx context.Context, rootPath string, file ScannedFile, inSeries bool,
) (store.ObservedBook, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return store.ObservedBook{}, fmt.Errorf("%w: %s: %v",
			ErrRootMissing, rootPath, err)
	}
	defer root.Close()

	opened, err := root.OpenFile(file.RelativePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return store.ObservedBook{}, err
	}
	defer opened.Close()

	info, err := opened.Stat()
	if err != nil {
		return store.ObservedBook{}, err
	}
	if !info.Mode().IsRegular() {
		return store.ObservedBook{}, ErrUnsafePath
	}

	digest := sha256.New()
	if _, err := io.Copy(digest, opened); err != nil {
		return store.ObservedBook{}, err
	}
	obs := store.ObservedBook{
		RelativePath:     file.RelativePath,
		SizeBytes:        info.Size(),
		MTime:            info.ModTime().UTC(),
		ContentSHA256:    hex.EncodeToString(digest.Sum(nil)),
		OriginalFilename: path.Base(file.RelativePath),
		MediaType:        "application/epub+zip",
	}

	result, err := epub.Validate(ctx, opened, info.Size(), r.epub)
	if err != nil {
		// A file that is not a readable EPUB is still a file somebody
		// put in their library, and refusing to catalog it would make it
		// invisible with no way to find out why. It is catalogued from
		// its name, and the failure is the caller's to log.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return store.ObservedBook{}, err
		}
		applyFilename(&obs, file.RelativePath, inSeries)
		return obs, nil
	}
	applyEPUB(&obs, result.Metadata)
	applyFilename(&obs, file.RelativePath, inSeries)
	return obs, nil
}

// applyEPUB copies what the publication says about itself.
func applyEPUB(obs *store.ObservedBook, m epub.Metadata) {
	obs.Title = m.Title
	obs.Subtitle = m.Subtitle
	obs.Description = m.Description
	obs.Publisher = m.Publisher
	obs.PublishedDate = m.PublishedDate
	obs.Languages = m.Languages
	obs.Tags = m.Subjects
	for _, id := range m.Identifiers {
		obs.Identifiers = append(obs.Identifiers,
			store.BookIdentifier{Scheme: id.Scheme, Value: id.Value})
	}
	for _, s := range m.Series {
		obs.Series = append(obs.Series,
			store.ObservedSeries{Name: s.Name, Position: s.Position})
	}
	for i, c := range m.Contributors {
		role := c.Role
		if role == "" {
			role = store.ContributorRoleAuthor
		}
		obs.Contributors = append(obs.Contributors,
			store.ObservedContributor{Name: c.Name, Role: role, Position: i})
	}
}

// applyFilename fills only what the file itself did not say. A
// publication's own metadata is better evidence than a guess from a
// path, so the path is a fallback and never an override.
func applyFilename(obs *store.ObservedBook, relative string, inSeries bool) {
	// A file in a subdirectory of a plain folder has already been read as
	// a volume of a series named after that directory, so the directory
	// is not also an author. Only the file's own name is left to read.
	if inSeries {
		relative = path.Base(relative)
	}
	candidate := metadata.ParsePath(relative, metadata.DefaultPathPatterns())
	if obs.Title == "" {
		obs.Title = candidate.Title
	}
	if obs.Title == "" {
		obs.Title = strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	}
	if len(obs.Contributors) == 0 && candidate.Author != "" {
		obs.Contributors = append(obs.Contributors, store.ObservedContributor{
			Name: candidate.Author,
			Role: store.ContributorRoleAuthor,
		})
	}
}

// inferSeries reads the directory tree as shelving, which is the one
// piece of organisation a plain folder already carries: a subdirectory
// holding EPUBs is a series, and the files in it are its volumes.
//
// Nothing about this is configurable and nothing asks a person to
// confirm it. Rename the directory and the series is renamed on the next
// pass. Files sitting loose at the root belong to no series, so a folder
// with everything at the top level behaves exactly as it did before.
func inferSeries(files []ScannedFile) map[string]store.ObservedSeries {
	byDirectory := map[string][]ScannedFile{}
	for _, f := range files {
		dir := path.Dir(f.RelativePath)
		if dir == "." || dir == "" {
			continue
		}
		byDirectory[dir] = append(byDirectory[dir], f)
	}
	out := map[string]store.ObservedSeries{}
	for dir, members := range byDirectory {
		name := path.Base(dir)
		if name == "" {
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			return members[i].RelativePath < members[j].RelativePath
		})
		for i, m := range members {
			position := positionFromName(m.RelativePath)
			if position == nil {
				// Sorted order is the fallback, because a shelf in some
				// order is more useful than a shelf in none — but a
				// number read from the filename always wins, since that
				// is what the person naming the file meant.
				fallback := float64(i + 1)
				position = &fallback
			}
			out[m.RelativePath] = store.ObservedSeries{
				Name:     name,
				Position: position,
			}
		}
	}
	return out
}

// positionFromName reads the number a person put at the front of a
// filename, so "03 - Equal Rites.epub" is volume three rather than
// volume whatever-it-sorted-to.
//
// It parses the leaf itself rather than going through the catalog's path
// patterns, because those read a two-segment path as author/title and
// would take "Discworld" for an author before ever looking at the
// number. Here the directory is already known to be a series.
func positionFromName(relative string) *float64 {
	name := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	digits := 0
	for digits < len(name) && (name[digits] >= '0' && name[digits] <= '9' ||
		name[digits] == '.' && digits > 0) {
		digits++
	}
	if digits == 0 {
		return nil
	}
	// The number has to be a prefix somebody meant as one: a separator
	// must follow it, so "1984.epub" stays a title rather than becoming
	// volume 1984 of its directory.
	rest := strings.TrimLeft(name[digits:], " ")
	if rest != "" && !strings.HasPrefix(rest, "-") &&
		!strings.HasPrefix(rest, "_") && !strings.HasPrefix(rest, ".") {
		return nil
	}
	if rest == "" {
		return nil
	}
	position, err := strconv.ParseFloat(strings.TrimSuffix(name[:digits], "."), 64)
	if err != nil {
		return nil
	}
	return &position
}
