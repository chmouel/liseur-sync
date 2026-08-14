package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testLibraryRefreshSchedule pins the claim protocol of ADR-0014's
// third axis. A refresh is due either because an interval elapsed or
// because somebody asked; claiming it is what stops two sweeps of one
// root; and what a refresh recorded — the time it worked, the error it
// did not — is on the library, where the admin panel reads it.
func testLibraryRefreshSchedule(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	now := time.Now().UTC().Truncate(time.Second)
	scheduledRoot, manualRoot := "/srv/scheduled", "/srv/manual"

	managed := store.Library{
		ID: "lib-managed", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source: store.LibraryManaged, Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual,
		Name:    "Uploads", ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	scheduled := store.Library{
		ID: "lib-scheduled", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source: store.LibraryDirectory, Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshInterval, RefreshInterval: time.Hour,
		Name: "Shelf", RootPath: &scheduledRoot, ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	manual := store.Library{
		ID: "lib-manual", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source: store.LibraryDirectory, Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshManual,
		Name:    "Archive", RootPath: &manualRoot, ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	for _, l := range []store.Library{managed, scheduled, manual} {
		if err := s.CreateLibrary(ctx, l); err != nil {
			t.Fatalf("create %s: %v", l.ID, err)
		}
	}

	// Nothing is due yet: the scheduled library was created a moment
	// ago and its interval is an hour, and the other two have no
	// schedule at all.
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q, but nothing was due yet", lib.ID)
	}

	// An hour later the scheduled one is, and the claim hands back the
	// library rather than just its id, because the caller is about to
	// sweep its root.
	due := now.Add(time.Hour + time.Second)
	claimed, ok, err := s.ClaimLibraryRefresh(ctx, due)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || claimed.ID != scheduled.ID {
		t.Fatalf("claimed %q/%v, want %q", claimed.ID, ok, scheduled.ID)
	}
	if claimed.RootPath == nil || *claimed.RootPath != scheduledRoot {
		t.Fatalf("claim came back without the root to sweep: %+v", claimed.RootPath)
	}
	// The claim is the exclusion: a second claimer at the same instant
	// gets nothing, because the first one stamped the attempt.
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, due); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q twice; the attempt stamp is not exclusive", lib.ID)
	}

	// A refresh that failed leaves the last success where it was: it
	// did not refresh anything.
	if err := s.FinishLibraryRefresh(
		ctx, scheduled.ID, due, "root is not readable"); err != nil {
		t.Fatal(err)
	}
	after, err := s.AdminLibraryByID(ctx, scheduled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRefreshAt != nil {
		t.Fatalf("a failed refresh recorded a success at %s", after.LastRefreshAt)
	}
	if after.LastRefreshError == nil || *after.LastRefreshError != "root is not readable" {
		t.Fatalf("last error is %v, want the failure text", after.LastRefreshError)
	}
	if after.LastRefreshAttemptAt == nil || !after.LastRefreshAttemptAt.Equal(due) {
		t.Fatalf("attempt recorded as %v, want %s", after.LastRefreshAttemptAt, due)
	}

	// The failure backs the library off by its own interval rather than
	// being retried immediately, which is the whole point of scheduling
	// from the attempt.
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, due.Add(time.Minute)); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q a minute after it failed; a failing root would spin", lib.ID)
	}

	// A refresh that worked clears the error and stamps the success.
	next := due.Add(time.Hour + time.Second)
	if _, ok, err := s.ClaimLibraryRefresh(ctx, next); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("the scheduled library was not due an hour after its last attempt")
	}
	if err := s.FinishLibraryRefresh(ctx, scheduled.ID, next, ""); err != nil {
		t.Fatal(err)
	}
	after, err = s.AdminLibraryByID(ctx, scheduled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRefreshError != nil {
		t.Fatalf("a successful refresh left the error %q behind", *after.LastRefreshError)
	}
	if after.LastRefreshAt == nil || !after.LastRefreshAt.Equal(next) {
		t.Fatalf("success recorded as %v, want %s", after.LastRefreshAt, next)
	}

	// A library with no schedule is refreshed by asking, and the ask is
	// what makes it due — whatever its policy says.
	if err := s.AdminRequestLibraryRefresh(ctx, alice.ID, manual.ID, next); err != nil {
		t.Fatal(err)
	}
	requested, err := s.AdminLibraryByID(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.RefreshRequestedAt == nil {
		t.Fatal("the request was not recorded on the library")
	}
	claimed, ok, err = s.ClaimLibraryRefresh(ctx, next.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || claimed.ID != manual.ID {
		t.Fatalf("claimed %q/%v, want the library that was asked for", claimed.ID, ok)
	}
	// Claiming clears the request, so honouring it once is honouring it
	// once.
	if claimed.RefreshRequestedAt != nil {
		t.Fatal("the claim handed back a request it had just cleared")
	}
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, next.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q again; one request refreshed twice", lib.ID)
	}

	// A managed library has no source, so it can never be asked for and
	// can never be claimed.
	if err := s.AdminRequestLibraryRefresh(
		ctx, alice.ID, managed.ID, next); err != store.ErrNotFound {
		t.Fatalf("requesting a refresh of a managed library gave %v, want ErrNotFound", err)
	}

	// A refresh error longer than the column allows is trimmed rather
	// than refused: the failure is what matters, not all of its output.
	long := make([]byte, store.MaxRefreshErrorLen*3)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.FinishLibraryRefresh(
		ctx, manual.ID, next, store.TruncateRefreshError(string(long))); err != nil {
		t.Fatal(err)
	}
	trimmed, err := s.AdminLibraryByID(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.LastRefreshError == nil ||
		len(*trimmed.LastRefreshError) > store.MaxRefreshErrorLen {
		t.Fatalf("stored error is %d bytes, want at most %d",
			len(*trimmed.LastRefreshError), store.MaxRefreshErrorLen)
	}

	if err := s.FinishLibraryRefresh(
		ctx, "no-such-library", next, ""); err != store.ErrNotFound {
		t.Fatalf("finishing a refresh of nothing gave %v, want ErrNotFound", err)
	}
}
