package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testReferencedBlobsAreWhatABackupMustHold pins what "referenced" means
// for backup verification. A blob is referenced when a book file points
// at it, including a trashed book's file — the whole promise of the trash
// is that the book can come back, and it cannot come back from a backup
// that skipped its bytes. A blob nothing points at is not a backup's
// problem; it is the orphan sweep's.
func testReferencedBlobsAreWhatABackupMustHold(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "backup-owner")
	now := time.Date(2026, time.December, 1, 8, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-backup", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Backup", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	live := ingestBlob("backup-live", 11)
	trashed := ingestBlob("backup-trashed", 22)
	unreferenced := ingestBlob("backup-unreferenced", 33)

	for i, spec := range []struct {
		bookID, fileID string
		blob           store.BlobInfo
		trash          bool
	}{
		{"book-live", "file-live", live, false},
		{"book-trashed", "file-trashed", trashed, true},
	} {
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: spec.bookID, LibraryID: library.ID, Status: store.BookActive,
			Title: spec.bookID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: spec.fileID, LibraryID: library.ID, BookID: spec.bookID,
			BlobSHA256: spec.blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: "b.epub", MediaType: "application/epub+zip",
			Availability: store.BookFileAvailable,
			CreatedAt:    now.Add(time.Duration(i) * time.Minute),
			UpdatedAt:    now,
		}, spec.blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		if spec.trash {
			if _, err := s.TrashCatalogBook(
				ctx, owner.ID, spec.bookID, now, now.Add(24*time.Hour),
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A blob the database knows about but nothing points at.
	if _, err := s.ReconcileBlob(ctx, unreferenced, true, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListReferencedBlobs(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		live.SHA256: live.SizeBytes, trashed.SHA256: trashed.SizeBytes,
	}
	if len(got) != len(want) {
		t.Fatalf("referenced blobs = %+v, want %d", got, len(want))
	}
	previous := ""
	for _, blob := range got {
		size, ok := want[blob.SHA256]
		if !ok {
			t.Fatalf("unreferenced blob %q reported as referenced", blob.SHA256)
		}
		if blob.SizeBytes != size {
			t.Fatalf("%q size = %d, want %d", blob.SHA256, blob.SizeBytes, size)
		}
		// Digest order is what makes the cursor work at all.
		if blob.SHA256 <= previous {
			t.Fatalf("out of order: %q after %q", blob.SHA256, previous)
		}
		previous = blob.SHA256
	}

	// Paging must not skip or repeat: a verifier that misses a page
	// reports a broken backup as sound.
	first, err := s.ListReferencedBlobs(ctx, "", 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %+v %v", first, err)
	}
	second, err := s.ListReferencedBlobs(ctx, first[0].SHA256, 1)
	if err != nil || len(second) != 1 ||
		second[0].SHA256 == first[0].SHA256 {
		t.Fatalf("second page = %+v %v", second, err)
	}
	if last, err := s.ListReferencedBlobs(
		ctx, second[0].SHA256, 1,
	); err != nil || len(last) != 0 {
		t.Fatalf("page past the end = %+v %v", last, err)
	}

	// An unbounded listing, or a cursor that is not a digest, are both
	// programming errors rather than empty results.
	if _, err := s.ListReferencedBlobs(
		ctx, "", 0,
	); err != store.ErrInvalidTransition {
		t.Fatalf("zero limit: %v", err)
	}
	if _, err := s.ListReferencedBlobs(ctx, "not-a-digest", 50); err == nil {
		t.Fatal("a cursor that is not a digest was accepted")
	}
}
