package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testLibraryAxes pins ADR-0014's three axes as three independent
// columns rather than the single `kind` they replace: a library's
// source, its storage mode and its refresh policy all survive a round
// trip, and the refresh interval comes back as the duration that was
// written rather than as whatever the column defaults to.
func testLibraryAxes(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	now := time.Now().UTC().Truncate(time.Second)
	root := "/srv/books"

	managed := store.Library{
		ID: "lib-managed", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual,
		Name:    "Uploads", ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	directory := store.Library{
		ID: "lib-directory", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source:          store.LibraryDirectory,
		Storage:         store.LibraryStorageCAS,
		Refresh:         store.LibraryRefreshInterval,
		RefreshInterval: 42 * time.Minute,
		Name:            "Shelf", RootPath: &root, ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	for _, l := range []store.Library{managed, directory} {
		if err := s.CreateLibrary(ctx, l); err != nil {
			t.Fatalf("create %s: %v", l.ID, err)
		}
	}

	for _, want := range []store.Library{managed, directory} {
		got, err := s.AdminLibraryByID(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Source != want.Source || got.Storage != want.Storage ||
			got.Refresh != want.Refresh {
			t.Fatalf("%s came back as %s/%s/%s, want %s/%s/%s",
				want.ID, got.Source, got.Storage, got.Refresh,
				want.Source, want.Storage, want.Refresh)
		}
		if want.Refresh == store.LibraryRefreshInterval &&
			got.RefreshInterval != want.RefreshInterval {
			t.Fatalf("%s refreshes every %s, want %s",
				want.ID, got.RefreshInterval, want.RefreshInterval)
		}
	}

	// A managed library has no root to sweep, so it is never offered to
	// the scanner however its other axes are set.
	scannable, err := s.ListScannableLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scannable) != 1 || scannable[0].ID != directory.ID {
		t.Fatalf("scannable = %+v, want only %s", scannable, directory.ID)
	}
	if scannable[0].RefreshInterval != directory.RefreshInterval {
		t.Fatalf("the scanner was told %s, want %s",
			scannable[0].RefreshInterval, directory.RefreshInterval)
	}

	// The axes are constrained, not free text: a managed library with a
	// root, and a root-backed one without, are both refused by the
	// database rather than stored and puzzled over later.
	bad := managed
	bad.ID = "lib-bad-root"
	bad.RootPath = &root
	if err := s.CreateLibrary(ctx, bad); err == nil {
		t.Fatal("a managed library was allowed to name a root path")
	}
	bad = directory
	bad.ID = "lib-bad-no-root"
	bad.RootPath = nil
	if err := s.CreateLibrary(ctx, bad); err == nil {
		t.Fatal("a directory library was allowed with no root path")
	}
}
