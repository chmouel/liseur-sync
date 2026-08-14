//go:build linux

package content

// The Calibre refresh (ADR-0014). Where a directory library is
// discovered by walking, a Calibre library is discovered by reading
// metadata.db: Calibre knows what it contains, and guessing at the tree
// would both miss books it has and find files it does not consider part
// of the library.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/catalog"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// calibreStore is the durable surface one Calibre refresh needs on top
// of what a sweep of any root needs.
type calibreStore interface {
	watchedStore
	CalibreBookMappings(context.Context, string) (map[int64]string, error)
	MapCalibreBook(context.Context, string, int64, string, time.Time) error
	DeleteCalibreBooks(context.Context, string, []int64, time.Time) (store.TrashPurgeResult, error)
	SetLibraryInventoryDigest(context.Context, string, string, time.Time) error
}

// CalibreSyncReport totals one Calibre refresh, on top of what the file
// reconciliation did.
type CalibreSyncReport struct {
	Sync WatchedSyncReport
	// Skipped reports the whole refresh stopping at the gate, because
	// the inventory hashed to what the last one recorded. It is the
	// common case on a library nobody is editing, and it costs one read
	// of metadata.db and a stat per book — and no catalog write at all.
	Skipped bool
	// Books is how many books metadata.db described.
	Books int
	// MetadataUpdated counts books whose catalog metadata moved.
	MetadataUpdated int
	// Mapped counts books newly tied to a Calibre id.
	Mapped int
	// Deleted counts catalog books removed because Calibre no longer has
	// them.
	Deleted int
	// Unresolved counts Calibre books with no catalog row yet — queued
	// this pass and not promoted until a worker gets to them, so their
	// metadata lands on the next refresh rather than this one.
	Unresolved int
}

// SyncCalibreLibrary reconciles one Calibre library against its
// metadata.db.
//
// The order matters and is not arbitrary. The gate comes first, because
// a library nobody touched must cost nothing. Files come next, through
// exactly the same reconciliation a directory library uses — a Calibre
// book is not a second kind of book — with the file list coming from
// the database rather than from a walk. Metadata comes after that, so a
// book promoted by an earlier pass is described before anything else
// looks at it. Deletions come last, because a book that vanished from
// Calibre must not be deleted on the strength of a read that failed
// half way through.
func SyncCalibreLibrary(
	ctx context.Context,
	st calibreStore,
	blobs watchedStager,
	library ScannedLibrary,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (CalibreSyncReport, error) {
	var report CalibreSyncReport
	if st == nil || blobs == nil || clock == nil ||
		library.ID == "" || library.RootPath == "" ||
		library.ActorUserID == "" || opts.MaxFileBytes <= 0 {
		return report, store.ErrInvalidTransition
	}

	db, err := calibre.Open(library.RootPath)
	if err != nil {
		// A root that cannot be opened, or that is not a Calibre library
		// at all, is a configuration or mount problem. It is reported and
		// changes nothing: an unmounted volume must never take a
		// household's catalog away.
		return report, fmt.Errorf("%w: %s: %w",
			ErrRootUnavailable, library.RootPath, err)
	}
	defer db.Close()

	inventory, err := db.Inventory(ctx)
	if err != nil {
		return report, fmt.Errorf("read calibre inventory: %w", err)
	}
	report.Books = len(inventory.Books)
	if !inventory.Changed(library.InventoryDigest) {
		report.Skipped = true
		report.Sync.Scan.Complete = true
		return report, nil
	}

	books, err := db.Books(ctx)
	if err != nil {
		return report, fmt.Errorf("read calibre books: %w", err)
	}

	sync, err := syncCalibreFiles(
		ctx, st, blobs, library, books, inventory, opts, clock)
	report.Sync = sync
	if err != nil {
		return report, err
	}

	if err := applyCalibreMetadata(
		ctx, st, db, library, books, clock, &report); err != nil {
		return report, err
	}

	if !report.Sync.Scan.Complete {
		// Absence was not established, so neither is deletion, and the
		// digest is not recorded: the next refresh must do this again.
		return report, nil
	}
	if err := reconcileCalibreDeletions(
		ctx, st, library, inventory, clock, &report); err != nil {
		return report, err
	}
	if err := st.SetLibraryInventoryDigest(
		ctx, library.ID, inventory.Digest, clock().UTC()); err != nil {
		return report, fmt.Errorf("record calibre inventory digest: %w", err)
	}
	return report, nil
}

// syncCalibreFiles reconciles the publications metadata.db names, using
// the same per-path decisions a directory sweep makes. The list is the
// difference: Calibre says which files are books, so nothing here has to
// guess whether a stray file in the tree is one.
func syncCalibreFiles(
	ctx context.Context,
	st calibreStore,
	blobs watchedStager,
	library ScannedLibrary,
	books []calibre.Book,
	inventory calibre.Inventory,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (WatchedSyncReport, error) {
	var report WatchedSyncReport
	sweepStartedAt := clock().UTC()
	if sweepStartedAt.IsZero() {
		return report, store.ErrInvalidTransition
	}
	stamps := make(map[string]calibre.FileStamp)
	for _, book := range inventory.Books {
		for _, file := range book.Files {
			stamps[file.RelativePath] = file
		}
	}

	root, err := os.OpenRoot(library.RootPath)
	if err != nil {
		return report, fmt.Errorf("%w: %s: %v",
			ErrRootUnavailable, library.RootPath, err)
	}
	defer root.Close()

	report.Scan.Complete = true
	observations := make([]store.WatchedObservation, 0, len(books))
	for _, book := range books {
		if err := ctx.Err(); err != nil {
			report.Scan.Complete = false
			return report, err
		}
		format, ok := book.EPUB()
		if !ok {
			// A book Calibre holds only as a MOBI or a PDF stays in
			// Calibre and is not in this catalog. It is not an error and
			// not a review item: there is nothing here to serve.
			report.Scan.Skipped++
			continue
		}
		relativePath := book.RelativePath(format)
		stamp, known := stamps[relativePath]
		if !known || !stamp.Present {
			// Calibre lists a file that is not on the disk. The path is
			// deliberately not recorded as seen, so the absence sweep
			// below marks the book unavailable rather than leaving it
			// advertised as downloadable.
			report.Scan.Skipped++
			continue
		}
		scanned := ScannedFile{
			RelativePath: relativePath,
			SizeBytes:    stamp.SizeBytes,
			ModifiedAt:   stamp.ModifiedAt,
		}
		report.Scan.Files = append(report.Scan.Files, scanned)
		observations = append(observations, store.WatchedObservation{
			SourceRelativePath: scanned.RelativePath,
			SizeBytes:          scanned.SizeBytes,
			ModifiedAt:         scanned.ModifiedAt,
		})
		if err := reconcileWatchedFile(
			ctx, st, blobs, root, library, scanned, opts, clock, &report,
		); err != nil {
			return report, err
		}
	}

	if err := recordWatchedObservations(
		ctx, st, library.ID, observations, clock().UTC()); err != nil {
		return report, err
	}
	if !report.Scan.Complete {
		return report, nil
	}
	absent, err := markWatchedAbsent(
		ctx, st, library.ID, sweepStartedAt, clock().UTC())
	report.MarkedAbsent = absent
	return report, err
}

// applyCalibreMetadata describes each catalog book the way Calibre does.
//
// A book with no catalog row yet is one this pass has only just queued:
// promotion is another worker's job, so its metadata lands on the next
// refresh. That is a pass behind rather than a race, and the alternative
// — this pass promoting books itself — is the ingest pipeline in two
// places.
func applyCalibreMetadata(
	ctx context.Context,
	st calibreStore,
	db *calibre.Library,
	library ScannedLibrary,
	books []calibre.Book,
	clock func() time.Time,
	report *CalibreSyncReport,
) error {
	mappings, err := st.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		return fmt.Errorf("read calibre mappings: %w", err)
	}
	for _, book := range books {
		if err := ctx.Err(); err != nil {
			return err
		}
		bookID, err := resolveCalibreBook(ctx, st, library, mappings, book)
		if err != nil {
			return err
		}
		if bookID == "" {
			report.Unresolved++
			continue
		}
		if mappings[book.ID] != bookID {
			if err := st.MapCalibreBook(
				ctx, library.ID, book.ID, bookID, clock().UTC()); err != nil {
				return fmt.Errorf("map calibre book %d: %w", book.ID, err)
			}
			mappings[book.ID] = bookID
			report.Mapped++
		}
		// The OPF fills what the database row lacks and nothing more,
		// which is what makes a Calibre version that moved a column cost
		// a field rather than a refresh.
		filled := book
		if err := db.ReadOPF(ctx, &filled); err != nil {
			return fmt.Errorf("read metadata.opf for calibre book %d: %w",
				book.ID, err)
		}
		changed, err := applyCalibreBookMetadata(
			ctx, st, library, bookID, metadata.FromCalibre(filled), clock)
		if err != nil {
			return err
		}
		if changed {
			report.MetadataUpdated++
		}
	}
	return nil
}

// resolveCalibreBook finds the catalog row for one Calibre book: the
// mapping if there is one, and otherwise the file at the path Calibre
// says the book lives at.
//
// Resolving by path is not a second identity scheme. It is how a book
// first acquires its mapping, since the row is created by the promotion
// worker some time after this pass queued the file, and the path is the
// only thing the two passes share. A path more than one book claims is
// left alone: the sweep has already flagged it for review, and guessing
// there is how one book's metadata lands on another.
func resolveCalibreBook(
	ctx context.Context,
	st calibreStore,
	library ScannedLibrary,
	mappings map[int64]string,
	book calibre.Book,
) (string, error) {
	if bookID, ok := mappings[book.ID]; ok {
		return bookID, nil
	}
	format, ok := book.EPUB()
	if !ok {
		return "", nil
	}
	known, err := st.WatchedFilesByPath(
		ctx, library.ID, book.RelativePath(format))
	if err != nil {
		return "", fmt.Errorf("read calibre path for book %d: %w",
			book.ID, err)
	}
	if len(known) != 1 {
		return "", nil
	}
	if known[0].BookStatus == store.BookTrashed {
		return "", nil
	}
	// A book already spoken for by another Calibre id is not this one.
	// That happens when two Calibre entries share one EPUB, and the
	// second of them has no catalog row of its own to describe.
	for id, mapped := range mappings {
		if mapped == known[0].BookID && id != book.ID {
			return "", nil
		}
	}
	return known[0].BookID, nil
}

// applyCalibreBookMetadata resolves one proposal against one book, with
// the same optimistic retry a promotion uses: losing to a concurrent
// writer costs a re-read, not the refresh.
func applyCalibreBookMetadata(
	ctx context.Context,
	st calibreStore,
	library ScannedLibrary,
	bookID string,
	proposal metadata.Proposal,
	clock func() time.Time,
) (bool, error) {
	var lastErr error
	for range metadataApplyAttempts {
		current, err := st.CatalogBookMetadata(
			ctx, library.ActorUserID, bookID, store.LibraryRoleManage)
		if err != nil {
			return false, fmt.Errorf(
				"read catalog metadata for book %q: %w", bookID, err)
		}
		resolved, changed := catalog.Resolve(current, proposal)
		if !changed {
			return false, nil
		}
		request := store.ApplyBookMetadataRequest{
			Metadata:         resolved,
			ExpectedRevision: current.Book.Revision,
			UpdatedAt:        clock().UTC(),
		}
		if err := store.ValidateApplyBookMetadata(request); err != nil {
			return false, fmt.Errorf(
				"resolved calibre metadata for book %q: %w", bookID, err)
		}
		_, err = st.ApplyCatalogBookMetadata(ctx, library.ActorUserID, request)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, store.ErrStaleRevision) {
			return false, fmt.Errorf(
				"apply calibre metadata for book %q: %w", bookID, err)
		}
		lastErr = err
	}
	return false, fmt.Errorf(
		"apply calibre metadata for book %q: %w", bookID, lastErr)
}

// reconcileCalibreDeletions removes the catalog books Calibre no longer
// has. Calibre is what this library means by true, so a book deleted
// there is deleted here; reading history survives, because it hangs off
// user-scoped works rather than off catalog books.
func reconcileCalibreDeletions(
	ctx context.Context,
	st calibreStore,
	library ScannedLibrary,
	inventory calibre.Inventory,
	clock func() time.Time,
	report *CalibreSyncReport,
) error {
	mappings, err := st.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		return fmt.Errorf("read calibre mappings: %w", err)
	}
	present := inventory.IDs()
	var gone []int64
	for calibreID := range mappings {
		if _, ok := present[calibreID]; !ok {
			gone = append(gone, calibreID)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	result, err := st.DeleteCalibreBooks(
		ctx, library.ID, gone, clock().UTC())
	if err != nil {
		return fmt.Errorf("delete books gone from calibre: %w", err)
	}
	report.Deleted += len(result.BookIDs)
	return nil
}
