package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testDuplicateContentIsReportedNotResolved covers duplicate detection.
// Uploading the same file twice is allowed on purpose, so the server owes
// the user the one thing it can say for certain: these two entries are
// the same bytes. It must say it without deciding which one to keep.
func testDuplicateContentIsReportedNotResolved(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "dup-owner")
	reader := MkUser(t, s, "dup-reader")
	outsider := MkUser(t, s, "dup-outsider")
	now := time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-dup", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Duplicates", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	// A second library holding the very same bytes: content is
	// deduplicated across the whole store, so a blob shared with somewhere
	// the user cannot see must not be reported as a duplicate here.
	other := store.Library{
		ID: "lib-dup-other", OwnerUserID: outsider.ID, QuotaUserID: outsider.ID,
		Kind: store.LibraryManaged, Name: "Elsewhere", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now,
	); err != nil {
		t.Fatal(err)
	}

	shared := ingestBlob("dup-shared", 40)
	lonely := ingestBlob("dup-lonely", 41)
	add := func(bookID, title string, blob store.BlobInfo, libraryID string,
		status store.BookStatus, at time.Time,
	) {
		t.Helper()
		actor := owner.ID
		if libraryID == other.ID {
			actor = outsider.ID
		}
		if err := s.CreateCatalogBook(ctx, actor, store.CatalogBook{
			ID: bookID, LibraryID: libraryID, Status: store.BookActive,
			Title: title, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: "file-" + bookID, LibraryID: libraryID, BookID: bookID,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: title + ".epub", MediaType: "application/epub+zip",
			Availability: store.BookFileAvailable, CreatedAt: at, UpdatedAt: at,
		}, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		if status == store.BookTrashed {
			expires := at.Add(48 * time.Hour)
			if _, err := s.TrashCatalogBook(
				ctx, owner.ID, bookID, at, expires,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	add("book-dup-a", "Morning Star", shared, library.ID,
		store.BookActive, now)
	add("book-dup-b", "Morning Star (again)", shared, library.ID,
		store.BookActive, now.Add(time.Minute))
	add("book-dup-alone", "Only Copy", lonely, library.ID,
		store.BookActive, now.Add(2*time.Minute))
	add("book-dup-elsewhere", "Someone Else's", shared, other.ID,
		store.BookActive, now.Add(3*time.Minute))

	duplicates, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicates) != 2 {
		t.Fatalf("duplicates = %+v, want the two books sharing bytes", duplicates)
	}
	if duplicates[0].Book.ID != "book-dup-a" ||
		duplicates[1].Book.ID != "book-dup-b" {
		t.Fatalf("duplicates are not grouped oldest first: %+v", duplicates)
	}
	for _, duplicate := range duplicates {
		if duplicate.SHA256 != shared.SHA256 {
			t.Fatalf("duplicate %q reported under %q, want %q",
				duplicate.Book.ID, duplicate.SHA256, shared.SHA256)
		}
	}

	// A reader may see the coincidence; resolving it is a deletion, which
	// read access cannot do. Someone with no access sees nothing at all.
	if readerView, err := s.ListDuplicateContentBooks(
		ctx, reader.ID, library.ID, 50,
	); err != nil || len(readerView) != 2 {
		t.Fatalf("reader view = %+v, err = %v", readerView, err)
	}
	if _, err := s.ListDuplicateContentBooks(
		ctx, outsider.ID, library.ID, 50,
	); err == nil {
		t.Fatal("an outsider listed another library's duplicates")
	}

	// Deleting one copy resolves it: the trashed book keeps its file, so
	// the pair would still look shared to a query that forgot to exclude
	// it, and the user would be told to resolve what they just resolved.
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-dup-b", now.Add(time.Hour),
		now.Add(49*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	after, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("deleting one copy left duplicates reported: %+v", after)
	}

	// Restoring brings the coincidence back, because it brings the second
	// entry back.
	if _, err := s.RestoreCatalogBook(
		ctx, owner.ID, "book-dup-b", now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	restored, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("restore did not bring the duplicate back: %+v", restored)
	}

	for _, limit := range []int{0, -1, 501} {
		if _, err := s.ListDuplicateContentBooks(
			ctx, owner.ID, library.ID, limit,
		); err == nil {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
}
