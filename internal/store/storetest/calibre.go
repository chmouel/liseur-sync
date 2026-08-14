package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCalibreLibraryIdentity pins the Calibre half of ADR-0014: a
// library whose books are identified by Calibre's own ids, deleted when
// Calibre forgets them, and skipped entirely when its inventory digest
// has not moved.
func testCalibreLibraryIdentity(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "calibre")
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	root := "/srv/calibre"

	library := store.Library{
		ID: "calibre-lib", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryCalibre,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, RefreshInterval: time.Hour,
		Name: "Calibre", RootPath: &root, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatalf("a calibre library could not be created: %v", err)
	}

	first := commitCalibreBook(t, s, user.ID, library.ID,
		"calibre-book-1", "Pratchett/Small Gods (1)/Small Gods.epub", now)
	second := commitCalibreBook(t, s, user.ID, library.ID,
		"calibre-book-2", "Pratchett/Good Omens (2)/Good Omens.epub",
		now.Add(time.Minute))

	if err := s.MapCalibreBook(
		ctx, library.ID, 1, first, now); err != nil {
		t.Fatalf("map calibre book: %v", err)
	}
	if err := s.MapCalibreBook(
		ctx, library.ID, 2, second, now); err != nil {
		t.Fatalf("map calibre book: %v", err)
	}
	// A refresh that sees the same books maps them again, and that has
	// to be free rather than a duplicate-key failure.
	if err := s.MapCalibreBook(
		ctx, library.ID, 1, first, now.Add(time.Hour)); err != nil {
		t.Fatalf("remapping the same book failed: %v", err)
	}

	mappings, err := s.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		t.Fatalf("read mappings: %v", err)
	}
	if len(mappings) != 2 || mappings[1] != first || mappings[2] != second {
		t.Fatalf("mappings = %v", mappings)
	}

	// Another library's mapping is not this one's, whatever the ids.
	otherRoot := "/srv/other-calibre"
	other := library
	other.ID, other.Name, other.RootPath = "calibre-other", "Other", &otherRoot
	if err := s.CreateLibrary(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherMappings, err := s.CalibreBookMappings(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherMappings) != 0 {
		t.Fatalf("another library's mappings leaked: %v", otherMappings)
	}

	// A book Calibre no longer knows about leaves the catalog. Nothing
	// was the server's to unlink, and nothing else moves.
	result, err := s.DeleteCalibreBooks(
		ctx, library.ID, []int64{2}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("delete calibre book: %v", err)
	}
	if len(result.BookIDs) != 1 || result.BookIDs[0] != second {
		t.Fatalf("delete result = %+v", result)
	}
	if result.BlobsOrphaned != 0 || result.ReservationsReleased != 0 {
		t.Fatalf("an in-place deletion touched the server's own storage: %+v",
			result)
	}
	if _, err := s.CatalogBookByID(
		ctx, user.ID, second, store.LibraryRoleRead,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a deleted book is still readable: %v", err)
	}
	if _, err := s.CatalogBookByID(
		ctx, user.ID, first, store.LibraryRoleRead); err != nil {
		t.Fatalf("the surviving book went with it: %v", err)
	}
	// The mapping cascades with the book it named, so a Calibre id that
	// comes back is a new book rather than a dangling row.
	mappings, err = s.CalibreBookMappings(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[1] != first {
		t.Fatalf("mappings after deletion = %v", mappings)
	}

	// A Calibre id this server never catalogued is nothing to delete,
	// not an error: the refresh that noticed it gone is right either way.
	if _, err := s.DeleteCalibreBooks(
		ctx, library.ID, []int64{9999}, now.Add(3*time.Hour)); err != nil {
		t.Fatalf("deleting an unmapped calibre id: %v", err)
	}

	// The change gate is stored on the library and read back with it —
	// by the worker holding the lease, and by nobody else.
	gate := now.Add(4 * time.Hour)
	holder, ok, err := s.ClaimLibraryRefresh(ctx, gate, leaseFor(gate))
	if err != nil || !ok || holder.ID != library.ID {
		t.Fatalf("claim for the digest: %v %v %q", ok, err, holder.ID)
	}
	if err := s.SetLibraryInventoryDigest(
		ctx, library.ID, holder.RefreshLeaseOwner, "abc123", gate); err != nil {
		t.Fatalf("set inventory digest: %v", err)
	}
	if err := s.FinishLibraryRefresh(ctx, library.ID,
		holder.RefreshLeaseOwner, gate, store.RefreshCodeNone); err != nil {
		t.Fatalf("finish: %v", err)
	}
	stored, err := s.AdminLibraryByID(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastInventoryDigest != "abc123" {
		t.Fatalf("inventory digest = %q", stored.LastInventoryDigest)
	}
	if stored.Source != store.LibraryCalibre {
		t.Fatalf("source = %q, want calibre", stored.Source)
	}
	// And a claim hands the digest to whoever runs the refresh, which is
	// the only reason it is stored.
	claimed, ok, err := s.ClaimLibraryRefresh(
		ctx, now.Add(5*time.Hour), leaseFor(now.Add(5*time.Hour)))
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if claimed.ID == library.ID && claimed.LastInventoryDigest != "abc123" {
		t.Fatalf("a claimed library lost its digest: %+v", claimed)
	}

	if err := s.SetLibraryInventoryDigest(
		ctx, "no-such-library", "owner", "abc", now,
	); !errors.Is(err, store.ErrRefreshLeaseLost) {
		t.Fatalf("digest of a library that does not exist: %v", err)
	}
	if err := s.MapCalibreBook(
		ctx, library.ID, 0, first, now); !errors.Is(
		err, store.ErrInvalidTransition) {
		t.Fatalf("a calibre id of zero was accepted: %v", err)
	}
}

// commitCalibreBook puts one in-place book in a library, the way a
// Calibre refresh does, and returns its catalog id.
func commitCalibreBook(
	t *testing.T,
	s store.Store,
	userID, libraryID, name, relativePath string,
	at time.Time,
) string {
	t.Helper()
	ctx := context.Background()
	job := inPlaceJob(t, s, userID, libraryID, name+"-job", relativePath, at)
	blob := ingestBlob(name, 2048)
	committed, err := s.CommitInPlaceBook(ctx, userID, job.ID,
		inPlaceRequest(job, blob, name, name+"-file", at.Add(time.Second)))
	if err != nil {
		t.Fatalf("commit calibre book %q: %v", name, err)
	}
	return committed.Book.ID
}
