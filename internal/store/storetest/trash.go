package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

// testTrashRestoreAndPurge covers the whole deletion path: trashing keeps
// the bytes reachable and charged, restore relinks them, and only the
// permanent purge releases quota and hands the blob to the orphan sweep.
func testTrashRestoreAndPurge(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "trash-owner")
	reader := MkUser(t, s, "trash-reader")
	outsider := MkUser(t, s, "trash-outsider")
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-trash", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Trash", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now,
	); err != nil {
		t.Fatal(err)
	}
	blob := ingestBlob("trash-blob", 11)
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-trash", LibraryID: library.ID, Status: store.BookActive,
		Title: "Trash Me", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-trash", LibraryID: library.ID, BookID: "book-trash",
		BlobSHA256: blob.SHA256, Source: store.IngestUpload,
		OriginalFilename: "trash.epub", MediaType: "application/epub+zip",
		Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
	}, blob.SizeBytes); err != nil {
		t.Fatal(err)
	}
	// Promotion charges quota alongside the file; the shared inserter
	// only writes the reference, so the charge is added here.
	reserveForTest(t, s, owner.ID, blob)

	expires := now.Add(48 * time.Hour)
	// Only manage may destroy: read access and no access both look the
	// same from outside, so neither can discover the book exists.
	if _, err := s.TrashCatalogBook(
		ctx, reader.ID, "book-trash", now, expires); err != store.ErrNotFound {
		t.Fatalf("reader trashed a book: %v", err)
	}
	if _, err := s.TrashCatalogBook(
		ctx, outsider.ID, "book-trash", now, expires); err != store.ErrNotFound {
		t.Fatalf("outsider trashed a book: %v", err)
	}
	assertStatus(t, s, owner.ID, "book-trash", store.BookActive)

	trashed, err := s.TrashCatalogBook(ctx, owner.ID, "book-trash", now, expires)
	if err != nil {
		t.Fatal(err)
	}
	if trashed.Status != store.BookTrashed ||
		trashed.TrashedAt == nil || trashed.TrashExpiresAt == nil {
		t.Fatalf("trashed book: %+v", trashed)
	}
	if !trashed.TrashExpiresAt.Equal(expires.UTC()) {
		t.Fatalf("retention: got %v want %v", trashed.TrashExpiresAt, expires)
	}
	// Trash is not the catalog, but the book is still there to restore.
	books, err := s.ListCatalogBooks(ctx, owner.ID, library.ID, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 0 {
		t.Fatalf("trashed book still listed: %+v", books)
	}
	// The reference is retained, so the bytes stay reachable and charged.
	if files, err := s.ListBookFiles(
		ctx, owner.ID, "book-trash", store.LibraryRoleRead); err != nil ||
		len(files) != 1 {
		t.Fatalf("trash dropped the file reference: %v %v", files, err)
	}
	assertReserved(t, s, owner.ID, blob.SHA256, true)
	// A retained trash reference is a GC root: the blob must not be
	// orphan-marked while the book can still be restored.
	if _, err := s.ReconcileBlob(ctx, blob, true, now); err != nil {
		t.Fatal(err)
	}
	assertOrphaned(t, s, blob.SHA256, false)

	// Trashing twice would silently extend the retention window.
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-trash", now, expires,
	); err != store.ErrInvalidTransition {
		t.Fatalf("double trash: %v", err)
	}
	// Nothing has expired yet.
	if purged, err := s.PurgeExpiredTrash(ctx, now.Add(time.Hour), 50); err != nil ||
		len(purged.BookIDs) != 0 {
		t.Fatalf("purged before retention expired: %+v %v", purged, err)
	}

	restored, err := s.RestoreCatalogBook(ctx, owner.ID, "book-trash", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != store.BookActive ||
		restored.TrashedAt != nil || restored.TrashExpiresAt != nil {
		t.Fatalf("restored book: %+v", restored)
	}
	if books, err := s.ListCatalogBooks(
		ctx, owner.ID, library.ID, nil, 50); err != nil || len(books) != 1 {
		t.Fatalf("restore did not return the book to the catalog: %v %v", books, err)
	}
	// Restoring what is not in the trash is not a transition.
	if _, err := s.RestoreCatalogBook(
		ctx, owner.ID, "book-trash", now,
	); err != store.ErrInvalidTransition {
		t.Fatalf("restore of an active book: %v", err)
	}

	// Trash it again and let retention pass.
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-trash", now, expires); err != nil {
		t.Fatal(err)
	}
	// Past retention the book is waiting to be purged, so restore must
	// not hand back something the purge is about to delete.
	if _, err := s.RestoreCatalogBook(
		ctx, owner.ID, "book-trash", expires,
	); err != store.ErrInvalidTransition {
		t.Fatalf("restore after retention: %v", err)
	}

	purged, err := s.PurgeExpiredTrash(ctx, expires, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged.BookIDs) != 1 || purged.BookIDs[0] != "book-trash" {
		t.Fatalf("purged books: %+v", purged)
	}
	if purged.FilesPurged != 1 || purged.ReservationsReleased != 1 ||
		purged.BlobsOrphaned != 1 {
		t.Fatalf("purge result: %+v", purged)
	}
	if _, err := s.CatalogBookByID(
		ctx, owner.ID, "book-trash", store.LibraryRoleRead,
	); err != store.ErrNotFound {
		t.Fatalf("purged book still readable: %v", err)
	}
	assertReserved(t, s, owner.ID, blob.SHA256, false)
	assertOrphaned(t, s, blob.SHA256, true)

	// The pass must be idempotent: a second run finds nothing.
	if again, err := s.PurgeExpiredTrash(ctx, expires, 50); err != nil ||
		len(again.BookIDs) != 0 {
		t.Fatalf("second purge: %+v %v", again, err)
	}

	for _, limit := range []int{0, 501} {
		if _, err := s.PurgeExpiredTrash(ctx, expires, limit); err != store.ErrInvalidTransition {
			t.Fatalf("purge limit %d accepted: %v", limit, err)
		}
	}
	if _, err := s.PurgeExpiredTrash(ctx, time.Time{}, 10); err != store.ErrInvalidTransition {
		t.Fatalf("purge zero cutoff accepted: %v", err)
	}
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-trash", now, now,
	); err != store.ErrInvalidTransition {
		t.Fatalf("retention that expires immediately accepted: %v", err)
	}
}

// testPurgeKeepsQuotaForRemainingReferences pins the part of the quota
// rule that a naive implementation gets wrong: a principal is charged once
// for a deduplicated blob, so deleting one of their two references must
// not refund it, while another principal's charge is independent.
func testPurgeKeepsQuotaForRemainingReferences(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	alice := MkUser(t, s, "dedup-alice")
	bob := MkUser(t, s, "dedup-bob")
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	blob := ingestBlob("shared-blob", 17)

	// Alice holds the same blob twice, in two libraries she pays for.
	// Bob holds it once, charged separately.
	type ref struct {
		library, book, file, owner string
	}
	refs := []ref{
		{"lib-alice-1", "book-alice-1", "file-alice-1", alice.ID},
		{"lib-alice-2", "book-alice-2", "file-alice-2", alice.ID},
		{"lib-bob", "book-bob", "file-bob", bob.ID},
	}
	for _, r := range refs {
		if err := s.CreateLibrary(ctx, store.Library{
			ID: r.library, OwnerUserID: r.owner, QuotaUserID: r.owner,
			Kind: store.LibraryManaged, Name: r.library, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateCatalogBook(ctx, r.owner, store.CatalogBook{
			ID: r.book, LibraryID: r.library, Status: store.BookActive,
			Title: r.book, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: r.file, LibraryID: r.library, BookID: r.book,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: r.file + ".epub",
			MediaType:        "application/epub+zip",
			Availability:     store.BookFileAvailable,
			CreatedAt:        now, UpdatedAt: now,
		}, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		reserveForTest(t, s, r.owner, blob)
	}

	expires := now.Add(time.Hour)
	if _, err := s.TrashCatalogBook(
		ctx, alice.ID, "book-alice-1", now, expires); err != nil {
		t.Fatal(err)
	}
	purged, err := s.PurgeExpiredTrash(ctx, expires, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged.BookIDs) != 1 {
		t.Fatalf("purged: %+v", purged)
	}
	// Alice still holds the blob in her other library, so her single
	// charge stands and nothing is orphaned.
	if purged.ReservationsReleased != 0 || purged.BlobsOrphaned != 0 {
		t.Fatalf("refunded a charge that is still owed: %+v", purged)
	}
	assertReserved(t, s, alice.ID, blob.SHA256, true)
	assertReserved(t, s, bob.ID, blob.SHA256, true)
	assertOrphaned(t, s, blob.SHA256, false)

	// Alice's last reference goes; her charge is released, Bob's is not.
	if _, err := s.TrashCatalogBook(
		ctx, alice.ID, "book-alice-2", now, expires); err != nil {
		t.Fatal(err)
	}
	purged, err = s.PurgeExpiredTrash(ctx, expires, 50)
	if err != nil {
		t.Fatal(err)
	}
	if purged.ReservationsReleased != 1 {
		t.Fatalf("last reference did not release the charge: %+v", purged)
	}
	// Bob still references the blob, so it is not collectable.
	if purged.BlobsOrphaned != 0 {
		t.Fatalf("orphaned a blob another principal still uses: %+v", purged)
	}
	assertReserved(t, s, alice.ID, blob.SHA256, false)
	assertReserved(t, s, bob.ID, blob.SHA256, true)
	assertOrphaned(t, s, blob.SHA256, false)

	// Bob's reference is the last one anywhere.
	if _, err := s.TrashCatalogBook(
		ctx, bob.ID, "book-bob", now, expires); err != nil {
		t.Fatal(err)
	}
	purged, err = s.PurgeExpiredTrash(ctx, expires, 50)
	if err != nil {
		t.Fatal(err)
	}
	if purged.ReservationsReleased != 1 || purged.BlobsOrphaned != 1 {
		t.Fatalf("final purge: %+v", purged)
	}
	assertReserved(t, s, bob.ID, blob.SHA256, false)
	assertOrphaned(t, s, blob.SHA256, true)
}

// testPurgeRespectsItsLimit keeps one maintenance tick bounded.
func testPurgeRespectsItsLimit(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "purge-limit-owner")
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-purge-limit", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Limit", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour)
	const total = 5
	for i := range total {
		id := string(rune('a'+i)) + "-purge"
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: id, LibraryID: "lib-purge-limit", Status: store.BookActive,
			Title: id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TrashCatalogBook(ctx, owner.ID, id, now, expires); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	passes := 0
	for {
		purged, err := s.PurgeExpiredTrash(ctx, expires, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(purged.BookIDs) == 0 {
			break
		}
		if len(purged.BookIDs) > 2 {
			t.Fatalf("limit exceeded: %+v", purged)
		}
		for _, id := range purged.BookIDs {
			if seen[id] {
				t.Fatalf("purged %q twice", id)
			}
			seen[id] = true
		}
		passes++
		if passes > total {
			t.Fatal("bounded purge did not converge")
		}
	}
	if len(seen) != total {
		t.Fatalf("purged %d of %d books", len(seen), total)
	}
}

type quotaTestReader interface {
	ReservedBytesForTest(ctx context.Context, userID, sha256 string) (int64, bool, error)
	BlobOrphanedForTest(ctx context.Context, sha256 string) (bool, error)
	ReserveForTest(ctx context.Context, userID string, blob store.BlobInfo) error
}

func quotaReader(t *testing.T, s store.Store) quotaTestReader {
	t.Helper()
	reader, ok := s.(quotaTestReader)
	if !ok {
		t.Fatalf("%T cannot inspect quota for shared tests", s)
	}
	return reader
}

func assertReserved(
	t *testing.T, s store.Store, userID, sha256 string, want bool,
) {
	t.Helper()
	_, present, err := quotaReader(t, s).ReservedBytesForTest(
		context.Background(), userID, sha256)
	if err != nil {
		t.Fatal(err)
	}
	if present != want {
		t.Fatalf("reservation for %s present=%v want %v", userID, present, want)
	}
}

func assertOrphaned(t *testing.T, s store.Store, sha256 string, want bool) {
	t.Helper()
	orphaned, err := quotaReader(t, s).BlobOrphanedForTest(
		context.Background(), sha256)
	if err != nil {
		t.Fatal(err)
	}
	if orphaned != want {
		t.Fatalf("blob orphaned=%v want %v", orphaned, want)
	}
}

func reserveForTest(t *testing.T, s store.Store, userID string, blob store.BlobInfo) {
	t.Helper()
	if err := quotaReader(t, s).ReserveForTest(
		context.Background(), userID, blob); err != nil {
		t.Fatal(err)
	}
}

// testPurgeSparesBlobsHeldByIngest covers the race the orphan sweep would
// otherwise lose: an upload has staged bytes that deduplicate against a
// blob whose last catalog reference is being purged. The hold is a
// reference too, so the blob must not be handed to the sweep — deleting it
// would strand a job that is about to promote it.
func testPurgeSparesBlobsHeldByIngest(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "hold-owner")
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-hold", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Hold", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	blob := ingestBlob("held-blob", 23)
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-hold", LibraryID: "lib-hold", Status: store.BookActive,
		Title: "Held", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-hold", LibraryID: "lib-hold", BookID: "book-hold",
		BlobSHA256: blob.SHA256, Source: store.IngestUpload,
		OriginalFilename: "held.epub", MediaType: "application/epub+zip",
		Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
	}, blob.SizeBytes); err != nil {
		t.Fatal(err)
	}
	reserveForTest(t, s, owner.ID, blob)

	// A second upload of the same bytes is staged but not yet promoted.
	job := createIngestJob(t, s, owner.ID, "lib-hold", "job-hold", now)
	if _, err := s.CommitIngestStage(ctx, owner.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(job.ID),
			UpdatedAt:   now.Add(time.Minute),
		}); err != nil {
		t.Fatal(err)
	}

	expires := now.Add(time.Hour)
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-hold", now, expires); err != nil {
		t.Fatal(err)
	}
	purged, err := s.PurgeExpiredTrash(ctx, expires, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged.BookIDs) != 1 || purged.FilesPurged != 1 {
		t.Fatalf("purged: %+v", purged)
	}
	if purged.BlobsOrphaned != 0 {
		t.Fatalf("orphaned a blob an upload is holding: %+v", purged)
	}
	assertOrphaned(t, s, blob.SHA256, false)
}

// testRestoreReflectsWhatTheBytesSupport pins the corner where the trash
// meets availability: bytes can go missing while a book sits in the trash,
// and restore must hand back what actually exists rather than an active
// book advertising a download that would 404.
func testRestoreReflectsWhatTheBytesSupport(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "restore-owner")
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-restore", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Restore", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	blob := ingestBlob("restore-blob", 29)
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-restore", LibraryID: "lib-restore", Status: store.BookActive,
		Title: "Restore", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-restore", LibraryID: "lib-restore", BookID: "book-restore",
		BlobSHA256: blob.SHA256, Source: store.IngestUpload,
		OriginalFilename: "restore.epub", MediaType: "application/epub+zip",
		Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
	}, blob.SizeBytes); err != nil {
		t.Fatal(err)
	}
	reserveForTest(t, s, owner.ID, blob)

	expires := now.Add(48 * time.Hour)
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-restore", now, expires); err != nil {
		t.Fatal(err)
	}
	// The bytes disappear from the CAS while the book is in the trash.
	later := now.Add(time.Hour)
	if _, err := s.ReconcileBlob(ctx, blob, false, later); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileCatalogAvailability(ctx, later, 100); err != nil {
		t.Fatal(err)
	}
	files, err := s.ListBookFiles(ctx, owner.ID, "book-restore", store.LibraryRoleRead)
	if err != nil || len(files) != 1 ||
		files[0].Availability != store.BookFileMissing {
		t.Fatalf("file availability while trashed: %+v %v", files, err)
	}

	restored, err := s.RestoreCatalogBook(ctx, owner.ID, "book-restore", later)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != store.BookMissing {
		t.Fatalf("restored a book whose bytes are gone as %q", restored.Status)
	}
}

// testTrashedBooksLeaveTheCatalogAndAppearInTheTrash pins the visibility
// rule on both sides. A trashed book must be unreachable through ordinary
// catalog reads at every role — a delete that leaves the book fetchable by
// id is not a delete — and it must be reachable through the trash view, or
// the retention window is a delay rather than an undo.
func testTrashedBooksLeaveTheCatalogAndAppearInTheTrash(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "vis-owner")
	reader := MkUser(t, s, "vis-reader")
	outsider := MkUser(t, s, "vis-outsider")
	now := time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-vis", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Visibility", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now,
	); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"vis-kept", "vis-gone"} {
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: id, LibraryID: library.ID, Status: store.BookActive,
			Title: id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "vis-gone", now, now.Add(24*time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	// Gone from every catalog read, including the owner's own.
	for _, role := range []store.LibraryRole{
		store.LibraryRoleRead, store.LibraryRoleManage,
	} {
		if _, err := s.CatalogBookByID(
			ctx, owner.ID, "vis-gone", role,
		); err != store.ErrNotFound {
			t.Fatalf("owner reads trashed book at %s: %v", role, err)
		}
	}
	if _, err := s.CatalogBookByID(
		ctx, reader.ID, "vis-gone", store.LibraryRoleRead,
	); err != store.ErrNotFound {
		t.Fatalf("reader reads trashed book: %v", err)
	}
	// The book beside it is untouched.
	if _, err := s.CatalogBookByID(
		ctx, reader.ID, "vis-kept", store.LibraryRoleRead,
	); err != nil {
		t.Fatalf("live book became unreadable: %v", err)
	}

	// Present in the trash, and only there.
	trashed, err := s.ListTrashedBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || trashed[0].ID != "vis-gone" {
		t.Fatalf("trash listing = %+v", trashed)
	}
	if trashed[0].TrashedAt == nil || !trashed[0].TrashedAt.Equal(now) {
		t.Fatalf("trashed_at = %v", trashed[0].TrashedAt)
	}

	// A manager who is not the owner sees the trash too: managing a
	// library means being able to undo a deletion in it.
	manager := MkUser(t, s, "vis-manager")
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, manager.ID, store.LibraryRoleManage, now,
	); err != nil {
		t.Fatal(err)
	}
	if byManager, err := s.ListTrashedBooks(
		ctx, manager.ID, library.ID, 50,
	); err != nil || len(byManager) != 1 || byManager[0].ID != "vis-gone" {
		t.Fatalf("manager's trash listing = %+v %v", byManager, err)
	}

	// The trash is manage-only: a reader must not learn what was deleted,
	// and an outsider must not learn the library exists.
	if _, err := s.ListTrashedBooks(
		ctx, reader.ID, library.ID, 50,
	); err != store.ErrNotFound {
		t.Fatalf("reader lists trash: %v", err)
	}
	if _, err := s.ListTrashedBooks(
		ctx, outsider.ID, library.ID, 50,
	); err != store.ErrNotFound {
		t.Fatalf("outsider lists trash: %v", err)
	}
	// An unbounded listing is not a listing.
	if _, err := s.ListTrashedBooks(
		ctx, owner.ID, library.ID, 0,
	); err != store.ErrInvalidTransition {
		t.Fatalf("unbounded trash listing: %v", err)
	}

	// Restoring empties the trash and returns the book to the catalog.
	if _, err := s.RestoreCatalogBook(
		ctx, owner.ID, "vis-gone", now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if again, err := s.ListTrashedBooks(
		ctx, owner.ID, library.ID, 50,
	); err != nil || len(again) != 0 {
		t.Fatalf("trash after restore = %+v %v", again, err)
	}
	if _, err := s.CatalogBookByID(
		ctx, reader.ID, "vis-gone", store.LibraryRoleRead,
	); err != nil {
		t.Fatalf("restored book unreadable: %v", err)
	}
}
