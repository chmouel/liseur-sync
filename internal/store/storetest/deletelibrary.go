package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testDeleteInPlaceLibrary covers removing a library the server only
// reads: the catalog rows go at once and the row with them, because
// nothing is lost that re-adding the library would not find again. The
// reader's own directory is not this store's to touch and there is
// nothing here that could reach it — what the test can prove is that no
// quota was held to release and no blob was handed to the orphan sweep,
// because an in-place file's bytes were never the server's.
func testDeleteInPlaceLibrary(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "delete-inplace-owner")
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	root := "/srv/books"
	library := store.Library{
		ID: "lib-inplace-del", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshManual, Name: "Read where it is",
		RootPath: &root, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-inplace", LibraryID: library.ID, Status: store.BookActive,
		Title: "Somebody Else's File", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	path := "Author/Title.epub"
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-inplace", LibraryID: library.ID, BookID: "book-inplace",
		Source: store.IngestScanned, SourceRelativePath: &path,
		Storage:      store.LibraryStorageInPlace,
		MediaType:    "application/epub+zip",
		Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
	}, 0); err != nil {
		t.Fatal(err)
	}

	result, err := s.AdminDeleteLibrary(
		ctx, owner.ID, library.ID, now, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed {
		t.Fatalf("deleting an in-place library left it behind: %+v", result)
	}
	if result.Trashed != 0 {
		t.Fatalf("trashed %d books of a library whose files are not ours",
			result.Trashed)
	}
	if len(result.Purged.BookIDs) != 1 || result.Purged.FilesPurged != 1 {
		t.Fatalf("purge result %+v, want one book and one file", result.Purged)
	}
	// Nothing was charged and nothing was collected: the bytes are in a
	// directory this server reads and does not own.
	if result.Purged.ReservationsReleased != 0 ||
		result.Purged.BlobsOrphaned != 0 {
		t.Fatalf("purge %+v settled bytes that were never the server's",
			result.Purged)
	}
	if _, err := s.AdminLibraryByID(ctx, library.ID); err != store.ErrNotFound {
		t.Fatalf("library survived deletion: %v", err)
	}
	// A second attempt has nothing to find. Saying so is what lets a
	// caller tell "removed" from "was never there".
	if _, err := s.AdminDeleteLibrary(
		ctx, owner.ID, library.ID, now, now.Add(48*time.Hour),
	); err != store.ErrNotFound {
		t.Fatalf("deleting a deleted library: %v", err)
	}
}

// testDeleteUploadsLibraryTrashesFirst covers the case where the server
// holds the only copy. One press moves the books to the trash on the
// ordinary window and keeps the library, so the reader can still see
// them and put them back; the second press, once nothing is outside the
// trash, purges what is left and takes the row.
func testDeleteUploadsLibraryTrashesFirst(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "delete-cas-owner")
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-cas-del", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Uploads",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	blob := ingestBlob("delete-cas-blob", 17)
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-cas", LibraryID: library.ID, Status: store.BookActive,
		Title: "The Only Copy", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-cas", LibraryID: library.ID, BookID: "book-cas",
		BlobSHA256: blob.SHA256, Source: store.IngestUpload,
		OriginalFilename: "only.epub", MediaType: "application/epub+zip",
		Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
	}, blob.SizeBytes); err != nil {
		t.Fatal(err)
	}
	reserveForTest(t, s, owner.ID, blob)

	expires := now.Add(720 * time.Hour)
	first, err := s.AdminDeleteLibrary(ctx, owner.ID, library.ID, now, expires)
	if err != nil {
		t.Fatal(err)
	}
	if first.Removed {
		t.Fatalf("one press destroyed the only copy of somebody's uploads: %+v",
			first)
	}
	if first.Trashed != 1 || !first.TrashExpiresAt.Equal(expires.UTC()) {
		t.Fatalf("first press %+v, want one book trashed until %v",
			first, expires.UTC())
	}
	if _, err := s.AdminLibraryByID(ctx, library.ID); err != nil {
		t.Fatalf("library went with its books: %v", err)
	}
	// A trashed book is gone as far as every ordinary read is concerned.
	if _, err := s.CatalogBookByID(
		ctx, owner.ID, "book-cas", store.LibraryRoleRead,
	); err != store.ErrNotFound {
		t.Fatalf("a trashed book is still in the catalog: %v", err)
	}
	// The bytes are still charged and still reachable, which is what
	// makes the window worth having: restore is a relink, not a re-upload.
	assertReserved(t, s, owner.ID, blob.SHA256, true)
	assertOrphaned(t, s, blob.SHA256, false)
	trashed, err := s.ListTrashedBooks(ctx, owner.ID, library.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 {
		t.Fatalf("trash holds %d books, want 1", len(trashed))
	}

	second, err := s.AdminDeleteLibrary(ctx, owner.ID, library.ID, now, expires)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Removed {
		t.Fatalf("second press left the library behind: %+v", second)
	}
	if second.Trashed != 0 {
		t.Fatalf("second press trashed %d more books", second.Trashed)
	}
	if len(second.Purged.BookIDs) != 1 || second.Purged.FilesPurged != 1 {
		t.Fatalf("purge %+v, want the trashed book and its file",
			second.Purged)
	}
	// Dropping the library row and letting the cascade take the books
	// would leave the owner charged for bytes nothing references and no
	// sweep would ever collect.
	if second.Purged.ReservationsReleased != 1 ||
		second.Purged.BlobsOrphaned != 1 {
		t.Fatalf("purge %+v did not settle what the files were holding",
			second.Purged)
	}
	assertReserved(t, s, owner.ID, blob.SHA256, false)
	assertOrphaned(t, s, blob.SHA256, true)
	if _, err := s.AdminLibraryByID(ctx, library.ID); err != store.ErrNotFound {
		t.Fatalf("library survived its second deletion: %v", err)
	}
}

// testDeleteEmptyLibrary covers the ordinary case of a library nobody
// put anything in: one press, whatever its storage, because there is
// nothing to be careful with.
func testDeleteEmptyLibrary(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "delete-empty-owner")
	other := MkUser(t, s, "delete-empty-other")
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"lib-empty-del", "lib-empty-keep"} {
		if err := s.CreateLibrary(ctx, store.Library{
			ID: id, OwnerUserID: owner.ID, QuotaUserID: owner.ID,
			Source:  store.LibraryManaged,
			Storage: store.LibraryStorageCAS,
			Refresh: store.LibraryRefreshManual, Name: id,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.GrantLibraryAccess(ctx, owner.ID, "lib-empty-del",
		other.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}

	result, err := s.AdminDeleteLibrary(
		ctx, owner.ID, "lib-empty-del", now, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || result.Trashed != 0 ||
		len(result.Purged.BookIDs) != 0 {
		t.Fatalf("deleting an empty library: %+v", result)
	}
	// The grant went with it. A row naming a library that is gone is a
	// grant nobody can see and nobody can revoke.
	grants, err := s.AdminLibraryGrants(ctx, "lib-empty-del", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("%d grants outlived their library", len(grants))
	}
	// Only the named library. Deleting one is not a statement about the
	// account's others.
	if _, err := s.AdminLibraryByID(ctx, "lib-empty-keep"); err != nil {
		t.Fatalf("deleting one library disturbed another: %v", err)
	}

	if _, err := s.AdminDeleteLibrary(ctx, owner.ID, "lib-nonexistent",
		now, now.Add(48*time.Hour)); err != store.ErrNotFound {
		t.Fatalf("deleting a library that never existed: %v", err)
	}
	// A window that is not a window is not a deletion policy.
	if _, err := s.AdminDeleteLibrary(ctx, owner.ID, "lib-empty-keep",
		now, now); err != store.ErrInvalidTransition {
		t.Fatalf("expiry that is not after now: %v", err)
	}
}
