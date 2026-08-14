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
	"path"
	"time"

	"github.com/google/uuid"

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
	ListBookFiles(context.Context, string, string, store.LibraryRole) ([]store.BookFile, error)
	SetBookFileCover(context.Context, string, string, string, string, time.Time) (bool, error)
	MapCalibreBook(context.Context, string, int64, string, time.Time) error
	DeleteCalibreBooks(context.Context, string, []int64, time.Time) (store.TrashPurgeResult, error)
	SetLibraryInventoryDigest(context.Context, string, string, string, time.Time) error
	RelocateWatchedFile(context.Context, string, string, string, time.Time, time.Time) error
	SupersedeInPlaceBookFile(context.Context, string, string, store.BookFile, time.Time) (store.BookFile, error)
}

// ErrCalibreUnreadable says metadata.db could not be read: it is not a
// Calibre library, it is not the file that was checked at the root, or a
// query against it failed. It is a fact about the database rather than
// about any book, so the whole refresh stops on it — degrading into a
// tree walk is the discovery mechanism this design rejected, arriving
// through a side door.
var ErrCalibreUnreadable = errors.New(
	"content: calibre metadata.db could not be read")

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
	// CoversUpdated counts books whose recorded cover moved to the
	// cover.jpg Calibre now has, or lost one Calibre no longer has.
	CoversUpdated int
	// Unresolved counts Calibre books with no catalog row yet — queued
	// this pass and not promoted until a worker gets to them, so their
	// metadata lands on the next refresh rather than this one. While any
	// remain, the inventory digest is not recorded, so that next refresh
	// happens whether or not anybody touches Calibre again.
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
		if errors.Is(err, calibre.ErrNotCalibre) ||
			errors.Is(err, calibre.ErrUnsafeRoot) {
			return report, fmt.Errorf("%w: %s: %w",
				ErrCalibreUnreadable, library.RootPath, err)
		}
		return report, fmt.Errorf("%w: %s: %w",
			ErrRootUnavailable, library.RootPath, err)
	}
	defer db.Close()

	lease := newRefreshLease(st, library, clock().UTC())

	inventory, err := db.Inventory(ctx)
	if err != nil {
		return report, calibreReadError("read calibre inventory", err)
	}
	report.Books = len(inventory.Books)
	if !inventory.Changed(library.InventoryDigest) {
		report.Skipped = true
		report.Sync.Scan.Complete = true
		return report, nil
	}

	books, err := db.Books(ctx)
	if err != nil {
		return report, calibreReadError("read calibre books", err)
	}

	sync, err := syncCalibreFiles(
		ctx, st, blobs, library, books, inventory, opts, clock, lease)
	report.Sync = sync
	if err != nil {
		return report, err
	}

	if err := applyCalibreMetadata(
		ctx, st, db, library, books, clock, lease, &report); err != nil {
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
	if report.Unresolved > 0 {
		// Some of Calibre's books have no catalog row yet: this pass
		// queued their files and a promotion worker will create them.
		// Recording the digest now would gate every later refresh on an
		// inventory that has not changed since, and those books would
		// stay unmapped and undescribed for as long as nobody edits the
		// library. The gate stays dirty until there is nothing left to
		// resolve.
		return report, nil
	}
	if err := st.SetLibraryInventoryDigest(ctx, library.ID, lease.owner(),
		inventory.Digest, clock().UTC()); err != nil {
		return report, fmt.Errorf("record calibre inventory digest: %w", err)
	}
	return report, nil
}

// calibreReadError separates a schema this server does not understand
// from a database it could not read at all, because the two get
// different codes and an administrator does different things about them.
func calibreReadError(what string, err error) error {
	if errors.Is(err, calibre.ErrUnsupportedSchema) {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %w: %w", what, ErrCalibreUnreadable, err)
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
	lease *refreshLease,
) (WatchedSyncReport, error) {
	var report WatchedSyncReport
	sweepStartedAt := clock().UTC()
	if sweepStartedAt.IsZero() {
		return report, store.ErrInvalidTransition
	}
	mappings, err := st.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		return report, fmt.Errorf("read calibre mappings: %w", err)
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
		if err := lease.hold(ctx, clock().UTC()); err != nil {
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
		if bookID, mapped := mappings[book.ID]; mapped {
			// A book Calibre already knows this catalog's row for is
			// reconciled against that row, not against the path. Calibre
			// renames directories when a title or an author is edited,
			// and resolving by path would catalog the renamed book a
			// second time and mark the first absent (ADR-0014).
			handled, err := reconcileCalibreBookFile(
				ctx, st, root, library, bookID, scanned, opts, clock, &report)
			if err != nil {
				return report, err
			}
			if handled {
				continue
			}
		}
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
	lease *refreshLease,
	report *CalibreSyncReport,
) error {
	mappings, err := st.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		return fmt.Errorf("read calibre mappings: %w", err)
	}
	root, err := os.OpenRoot(library.RootPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %v",
			ErrRootUnavailable, library.RootPath, err)
	}
	defer root.Close()
	for _, book := range books {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := lease.hold(ctx, clock().UTC()); err != nil {
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
		if err := applyCalibreCover(
			ctx, st, root, library, bookID, book, clock, report); err != nil {
			return err
		}
	}
	return nil
}

// MaxCoverBytes bounds the cover.jpg one book may have. It is far past
// any real cover and small enough that a library full of them cannot
// make a refresh — or a request — read an unbounded amount of somebody
// else's disk. Recording and serving share it: a cover too large to
// record must not become one that is read anyway on the way out.
const MaxCoverBytes = 16 << 20

// applyCalibreCover records the cover Calibre holds beside the book, in
// preference to the one inside the EPUB.
//
// It is stored rather than extracted because it is a choice somebody
// made — a curator who replaced a bad cover means it — and because two
// Calibre books can share one EPUB and still want different pictures.
// The digest is what makes that work: the rendered-cover cache is keyed
// by it, so a replaced cover.jpg is a different key rather than a stale
// image (ADR-0014). The path is compared too, because renaming a book in
// Calibre moves the same bytes to a new directory: a digest-only
// comparison would leave the recorded path pointing at a directory that
// no longer exists, and no later refresh could ever correct it.
func applyCalibreCover(
	ctx context.Context,
	st calibreStore,
	root *os.Root,
	library ScannedLibrary,
	bookID string,
	book calibre.Book,
	clock func() time.Time,
	report *CalibreSyncReport,
) error {
	files, err := st.ListBookFiles(
		ctx, library.ActorUserID, bookID, store.LibraryRoleManage)
	if err != nil {
		return fmt.Errorf("read files of book %q: %w", bookID, err)
	}
	var file store.BookFile
	for _, candidate := range files {
		if candidate.Availability == store.BookFileAvailable {
			file = candidate
			break
		}
	}
	if file.ID == "" {
		return nil
	}

	var relativePath, digest string
	if book.HasCover {
		relativePath = book.CoverPath()
		digest, err = hashWatchedSource(
			ctx, root, relativePath, MaxCoverBytes)
		if err != nil {
			// Calibre's flag is what it believes it wrote, not a stat.
			// A cover that is not there, is too large, or cannot be read
			// leaves the book with whatever its EPUB declares, which is
			// a picture rather than an error.
			relativePath, digest = "", ""
		}
	}
	stored := ""
	if file.CoverRelativePath != nil {
		stored = *file.CoverRelativePath
	}
	if digest == file.CoverSHA256 && relativePath == stored {
		return nil
	}
	changed, err := st.SetBookFileCover(
		ctx, library.ID, file.ID, relativePath, digest, clock().UTC())
	if err != nil {
		return fmt.Errorf("record cover of book %q: %w", bookID, err)
	}
	if changed {
		report.CoversUpdated++
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

// reconcileCalibreBookFile decides what one mapped Calibre book's file
// means, by book id rather than by path.
//
// It reports whether it settled the question. A book whose live file
// this pass cannot identify — one with no file row yet, or with several
// — is left to the path-based reconciliation, which is where ambiguity
// is reported rather than resolved.
func reconcileCalibreBookFile(
	ctx context.Context,
	st calibreStore,
	root *os.Root,
	library ScannedLibrary,
	bookID string,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
	report *WatchedSyncReport,
) (bool, error) {
	files, err := st.ListBookFiles(
		ctx, library.ActorUserID, bookID, store.LibraryRoleManage)
	if err != nil {
		return false, fmt.Errorf("read files of book %q: %w", bookID, err)
	}
	var current store.BookFile
	live := 0
	for _, candidate := range files {
		if candidate.Availability == store.BookFileSuperseded {
			continue
		}
		live++
		if current.ID == "" {
			current = candidate
		}
	}
	if current.ID == "" || live != 1 || current.SourceRelativePath == nil {
		return false, nil
	}

	samePath := *current.SourceRelativePath == file.RelativePath
	if samePath && bookFileUnchanged(current, file) {
		report.Unchanged++
		return true, nil
	}
	digest, err := hashWatchedSource(
		ctx, root, file.RelativePath, opts.MaxFileBytes)
	if err != nil {
		if isRetryableWatchedFailure(err) {
			report.Failed++
			report.Scan.Complete = false
			return true, nil
		}
		return false, err
	}
	if digest == current.ContentSHA256 {
		if samePath {
			// A touched file. The observation this sweep already
			// recorded carries the new modification time.
			report.Rehashed++
			return true, nil
		}
		if err := st.RelocateWatchedFile(ctx, library.ID, current.ID,
			file.RelativePath, file.ModifiedAt, clock().UTC()); err != nil {
			return false, fmt.Errorf(
				"relocate file of book %q: %w", bookID, err)
		}
		report.Relocated++
		return true, nil
	}

	// Different bytes for a book Calibre still calls the same book: a
	// conversion or a replaced format. The catalog row stays and the
	// file behind it is replaced.
	if library.Storage != store.LibraryStorageInPlace {
		// The bytes would have to be copied into content-addressed
		// storage first, which is the ingest pipeline's job and not
		// this pass's. Leaving it to the path-based reconciliation
		// flags the book for review, which is a person deciding rather
		// than this pass guessing.
		return false, nil
	}
	return supersedeCalibreBookFile(
		ctx, st, root, library, bookID, current, file, opts, clock, report)
}

// supersedeCalibreBookFile records the replacement bytes as a new file
// on the same book.
//
// The replacement is validated exactly as an in-place ingest would
// validate it, because it is the same act — publishing bytes this server
// does not own — and an unreadable conversion must not take a readable
// book away. One that fails goes to review, where a person decides.
func supersedeCalibreBookFile(
	ctx context.Context,
	st calibreStore,
	root *os.Root,
	library ScannedLibrary,
	bookID string,
	current store.BookFile,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
	report *WatchedSyncReport,
) (bool, error) {
	read, err := readInPlaceReplacement(ctx, root, file.RelativePath, opts)
	if err != nil {
		if isRetryableWatchedFailure(err) || errors.Is(err, ErrSourceRaced) {
			report.Failed++
			report.Scan.Complete = false
			return true, nil
		}
		report.Review++
		if _, err := st.SetCatalogBookReview(ctx, library.ID, bookID,
			reviewContentChanged, clock().UTC()); err != nil {
			return false, err
		}
		return true, nil
	}
	at := clock().UTC()
	relativePath := file.RelativePath
	replacement := store.BookFile{
		ID:                 calibreReplacementFileID(library.ID, bookID, read.ContentSHA256),
		LibraryID:          library.ID,
		BookID:             bookID,
		Storage:            store.LibraryStorageInPlace,
		ContentSHA256:      read.ContentSHA256,
		ContentSizeBytes:   read.ContentSizeBytes,
		Source:             store.IngestScanned,
		SourceRelativePath: &relativePath,
		SourceModifiedAt:   read.SourceModifiedAt,
		OriginalFilename:   path.Base(relativePath),
		MediaType:          mediaTypeEPUB,
		Availability:       store.BookFileAvailable,
	}
	if _, err := st.SupersedeInPlaceBookFile(
		ctx, library.ID, current.ID, replacement, at); err != nil {
		return false, fmt.Errorf(
			"supersede file of book %q: %w", bookID, err)
	}
	report.Superseded++
	return true, nil
}

// calibreReplacementFileID derives a replacement's id from the book and
// the bytes, so a refresh that dies after the commit and runs again
// names the same row rather than a second one.
func calibreReplacementFileID(libraryID, bookID, digest string) string {
	return uuid.NewSHA1(promotionNS,
		[]byte("calibre-supersede|"+libraryID+"|"+bookID+"|"+digest)).String()
}

// bookFileUnchanged is watchedFileUnchanged for a catalog file row: the
// recorded size and modification time still describing what is on disk,
// without reading it.
func bookFileUnchanged(current store.BookFile, file ScannedFile) bool {
	if current.SourceModifiedAt == nil {
		return false
	}
	return current.ContentSizeBytes == file.SizeBytes &&
		current.SourceModifiedAt.Equal(file.ModifiedAt)
}
