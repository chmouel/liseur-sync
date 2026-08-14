package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCatalogAvailabilityReconciliation proves that catalog visibility
// follows the bytes: a file whose blob went missing stops being
// downloadable, its book stops being active, and both come back when the
// blob returns. It also pins the two states the pass must never touch —
// superseded files and trashed books.
func testCatalogAvailabilityReconciliation(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "availability-owner")
	now := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-availability", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Availability", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	type fixture struct {
		bookID   string
		status   store.BookStatus
		fileID   string
		blob     string
		presence store.BookFileAvailability
	}
	fixtures := []fixture{
		// The blob of this one disappears.
		{"book-lost", store.BookActive, "file-lost", "blob-lost", store.BookFileAvailable},
		// This one keeps its blob and must not move.
		{"book-kept", store.BookActive, "file-kept", "blob-kept", store.BookFileAvailable},
		// Superseded is not a statement about bytes, so losing the blob
		// must not rewrite it, and the book must still go missing.
		{"book-superseded", store.BookActive, "file-superseded", "blob-lost", store.BookFileSuperseded},
		// A trashed book is trashed regardless of its bytes.
		{"book-trashed", store.BookTrashed, "file-trashed", "blob-lost", store.BookFileAvailable},
	}
	// Sizes are keyed by blob label, not by loop index: three fixtures
	// share blob-lost, and a blob has exactly one size.
	blobSize := func(label string) int64 { return int64(len(label)) }
	for _, f := range fixtures {
		book := store.CatalogBook{
			ID: f.bookID, LibraryID: library.ID, Status: f.status,
			Title: f.bookID, CreatedAt: now, UpdatedAt: now,
		}
		if f.status == store.BookTrashed {
			trashed := now
			expires := now.Add(24 * time.Hour)
			book.TrashedAt = &trashed
			book.TrashExpiresAt = &expires
		}
		if err := s.CreateCatalogBook(ctx, owner.ID, book); err != nil {
			t.Fatal(err)
		}
		blob := ingestBlob(f.blob, blobSize(f.blob))
		file := store.BookFile{
			ID: f.fileID, LibraryID: library.ID, BookID: f.bookID,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: f.fileID + ".epub",
			MediaType:        "application/epub+zip",
			Availability:     f.presence,
			CreatedAt:        now, UpdatedAt: now,
		}
		if err := inserter.InsertBookFileForTest(ctx, file, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
	}

	lost := ingestBlob("blob-lost", blobSize("blob-lost"))
	kept := ingestBlob("blob-kept", blobSize("blob-kept"))

	// A book with no files at all is not evidence about the filesystem, so
	// the pass must leave it alone rather than declaring it missing.
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-fileless", LibraryID: library.ID, Status: store.BookActive,
		Title: "Fileless", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Nothing has been observed yet, so no file may move. book-superseded
	// is the exception on the book axis: its only file is superseded, so
	// it has nothing to serve regardless of what is on disk.
	initial, err := s.ReconcileCatalogAvailability(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if initial.FilesMarkedMissing != 0 || initial.FilesMarkedAvailable != 0 {
		t.Fatalf("files moved before any blob was observed: %+v", initial)
	}
	if initial.BooksMarkedMissing != 1 || initial.BooksMarkedActive != 0 {
		t.Fatalf("books before any blob was observed: %+v", initial)
	}
	if quiet, err := s.ReconcileCatalogAvailability(ctx, now, 100); err != nil {
		t.Fatal(err)
	} else if quiet.Changed() {
		t.Fatalf("settled state was not stable: %+v", quiet)
	}

	// The blob reconciliation pass observes one blob gone.
	if _, err := s.ReconcileBlob(ctx, lost, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileBlob(ctx, kept, true, now); err != nil {
		t.Fatal(err)
	}

	later := now.Add(time.Minute)
	gone, err := s.ReconcileCatalogAvailability(ctx, later, 100)
	if err != nil {
		t.Fatal(err)
	}
	// Only file-lost flips: file-superseded is excluded by state and
	// file-trashed's blob is also blob-lost, so it does flip too.
	if gone.FilesMarkedMissing != 2 || gone.FilesMarkedAvailable != 0 {
		t.Fatalf("files after loss: %+v", gone)
	}
	// book-lost loses its last available file. book-superseded is already
	// missing, and book-trashed is not active, so neither is eligible.
	if gone.BooksMarkedMissing != 1 || gone.BooksMarkedActive != 0 {
		t.Fatalf("books after loss: %+v", gone)
	}
	assertAvailability(t, s, owner.ID, "book-lost", "file-lost", store.BookFileMissing)
	assertAvailability(t, s, owner.ID, "book-kept", "file-kept", store.BookFileAvailable)
	assertAvailability(t, s, owner.ID, "book-superseded", "file-superseded", store.BookFileSuperseded)
	assertStatus(t, s, owner.ID, "book-lost", store.BookMissing)
	assertStatus(t, s, owner.ID, "book-kept", store.BookActive)
	assertStatus(t, s, owner.ID, "book-superseded", store.BookMissing)
	assertStillTrashed(t, s, owner.ID, library.ID, "book-trashed")
	assertStatus(t, s, owner.ID, "book-fileless", store.BookActive)

	// A second pass over unchanged state must be a no-op, or the pass
	// would spin forever in its caller's loop.
	repeat, err := s.ReconcileCatalogAvailability(ctx, later, 100)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Changed() {
		t.Fatalf("repeated pass was not idempotent: %+v", repeat)
	}

	// The blob comes back.
	if _, err := s.ReconcileBlob(ctx, lost, true, later); err != nil {
		t.Fatal(err)
	}
	back, err := s.ReconcileCatalogAvailability(ctx, later.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if back.FilesMarkedAvailable != 2 || back.FilesMarkedMissing != 0 {
		t.Fatalf("files after return: %+v", back)
	}
	if back.BooksMarkedActive != 1 || back.BooksMarkedMissing != 0 {
		t.Fatalf("books after return: %+v", back)
	}
	assertAvailability(t, s, owner.ID, "book-lost", "file-lost", store.BookFileAvailable)
	// Restoring the blob must not resurrect a superseded file.
	assertAvailability(t, s, owner.ID, "book-superseded", "file-superseded", store.BookFileSuperseded)
	assertStatus(t, s, owner.ID, "book-lost", store.BookActive)
	// Its only file is superseded, so it has nothing to serve and stays
	// missing.
	assertStatus(t, s, owner.ID, "book-superseded", store.BookMissing)
	assertStillTrashed(t, s, owner.ID, library.ID, "book-trashed")
	assertStatus(t, s, owner.ID, "book-fileless", store.BookActive)

	for _, limit := range []int{0, 501} {
		if _, err := s.ReconcileCatalogAvailability(ctx, later, limit); err != store.ErrInvalidTransition {
			t.Fatalf("limit %d accepted: %v", limit, err)
		}
	}
	if _, err := s.ReconcileCatalogAvailability(ctx, time.Time{}, 10); err != store.ErrInvalidTransition {
		t.Fatalf("zero timestamp accepted: %v", err)
	}
}

// testCatalogAvailabilityRespectsItsLimit proves the pass is bounded, so a
// large library cannot turn one maintenance tick into an unbounded write
// transaction.
func testCatalogAvailabilityRespectsItsLimit(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "availability-limit-owner")
	now := time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-availability-limit", OwnerUserID: owner.ID,
		QuotaUserID: owner.ID, Source: store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual,
		Name:    "Limit", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	const total = 5
	for i := range total {
		id := string(rune('a'+i)) + "-limit"
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: id, LibraryID: library.ID, Status: store.BookActive,
			Title: id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		blob := ingestBlob(id, int64(i+1))
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: id + "-file", LibraryID: library.ID, BookID: id,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: id + ".epub",
			MediaType:        "application/epub+zip",
			Availability:     store.BookFileAvailable,
			CreatedAt:        now, UpdatedAt: now,
		}, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReconcileBlob(ctx, blob, false, now); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.ReconcileCatalogAvailability(ctx, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.FilesMarkedMissing != 2 {
		t.Fatalf("limit not applied to files: %+v", first)
	}
	// Only the two files this pass hid can have made their books missing.
	if first.BooksMarkedMissing != 2 {
		t.Fatalf("books ran ahead of their files: %+v", first)
	}

	passes := 1
	for {
		result, err := s.ReconcileCatalogAvailability(ctx, now, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed() {
			break
		}
		passes++
		if passes > total+2 {
			t.Fatal("bounded passes did not converge")
		}
	}
	for i := range total {
		id := string(rune('a'+i)) + "-limit"
		assertStatus(t, s, owner.ID, id, store.BookMissing)
		assertAvailability(t, s, owner.ID, id, id+"-file", store.BookFileMissing)
	}
}

func assertAvailability(
	t *testing.T,
	s store.Store,
	userID, bookID, fileID string,
	want store.BookFileAvailability,
) {
	t.Helper()
	files, err := s.ListBookFiles(context.Background(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.ID == fileID {
			if file.Availability != want {
				t.Fatalf("%s availability: got %q want %q",
					fileID, file.Availability, want)
			}
			return
		}
	}
	t.Fatalf("%s not found among %d files", fileID, len(files))
}

// assertStillTrashed checks the trashed fixture through the trash view,
// because the catalog no longer admits a deleted book exists.
func assertStillTrashed(
	t *testing.T,
	s store.Store,
	userID, libraryID, bookID string,
) {
	t.Helper()
	books, err := s.ListTrashedBooks(context.Background(), userID, libraryID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.ID == bookID {
			if b.Status != store.BookTrashed {
				t.Fatalf("%s status: got %q", bookID, b.Status)
			}
			return
		}
	}
	t.Fatalf("%s left the trash", bookID)
}

func assertStatus(
	t *testing.T,
	s store.Store,
	userID, bookID string,
	want store.BookStatus,
) {
	t.Helper()
	book, err := s.CatalogBookByID(
		context.Background(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if book.Status != want {
		t.Fatalf("%s status: got %q want %q", bookID, book.Status, want)
	}
}
