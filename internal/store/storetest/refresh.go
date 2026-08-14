package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testLibraryRefreshSchedule pins the claim protocol of ADR-0014's
// third axis. A refresh is due either because an interval elapsed or
// because somebody asked; claiming it takes a lease that is what stops
// two sweeps of one root; and what a refresh recorded — the time it
// worked, the bounded code for why it did not — is on the library,
// where the admin panel reads it.
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
	if lib, ok, err := s.ClaimLibraryRefresh(
		ctx, now.Add(time.Minute), leaseFor(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q, but nothing was due yet", lib.ID)
	}

	// An hour later the scheduled one is, and the claim hands back the
	// library rather than just its id, because the caller is about to
	// sweep its root.
	due := now.Add(time.Hour + time.Second)
	claimed, ok, err := s.ClaimLibraryRefresh(ctx, due, leaseFor(due))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || claimed.ID != scheduled.ID {
		t.Fatalf("claimed %q/%v, want %q", claimed.ID, ok, scheduled.ID)
	}
	if claimed.RootPath == nil || *claimed.RootPath != scheduledRoot {
		t.Fatalf("claim came back without the root to sweep: %+v", claimed.RootPath)
	}
	if claimed.RefreshLeaseOwner == "" || claimed.RefreshLeaseUntil == nil {
		t.Fatalf("the claim took no lease: %+v", claimed)
	}
	// The claim is the exclusion: a second claimer at the same instant
	// gets nothing, because the first one stamped the attempt and holds
	// the lease.
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, due, leaseFor(due)); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q twice; the attempt stamp is not exclusive", lib.ID)
	}

	// A worker that no longer holds the library writes nothing: not the
	// outcome, not a renewal, not the inventory digest.
	if err := s.FinishLibraryRefresh(ctx, scheduled.ID, "somebody-else",
		due, store.RefreshCodeNone); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a stranger finished the refresh: %v", err)
	}
	if err := s.SetLibraryInventoryDigest(ctx, scheduled.ID, "somebody-else",
		"deadbeef", due); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a stranger advanced the inventory digest: %v", err)
	}
	if err := s.RenewLibraryRefreshLease(ctx, scheduled.ID,
		store.RefreshLease{Owner: "somebody-else", Until: due.Add(time.Minute)},
		due); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a stranger renewed the lease: %v", err)
	}
	if err := s.CheckLibraryRefreshLease(
		ctx, scheduled.ID, "somebody-else", due); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a stranger holds the lease: %v", err)
	}
	// The holder does hold it, and renewing moves the expiry.
	if err := s.CheckLibraryRefreshLease(
		ctx, scheduled.ID, claimed.RefreshLeaseOwner, due); err != nil {
		t.Fatalf("the holder lost its own lease: %v", err)
	}
	renewed := due.Add(10 * time.Minute)
	if err := s.RenewLibraryRefreshLease(ctx, scheduled.ID,
		store.RefreshLease{
			Owner: claimed.RefreshLeaseOwner, Until: renewed,
		}, due); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// A refresh that failed leaves the last success where it was: it
	// did not refresh anything.
	if err := s.FinishLibraryRefresh(ctx, scheduled.ID,
		claimed.RefreshLeaseOwner, due,
		store.RefreshCodeRootUnavailable); err != nil {
		t.Fatal(err)
	}
	after, err := s.AdminLibraryByID(ctx, scheduled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRefreshAt != nil {
		t.Fatalf("a failed refresh recorded a success at %s", after.LastRefreshAt)
	}
	if after.LastRefreshCode != store.RefreshCodeRootUnavailable {
		t.Fatalf("last code is %q, want the failure code", after.LastRefreshCode)
	}
	// Finishing releases the lease, whatever the outcome was.
	if after.RefreshLeaseOwner != "" {
		t.Fatalf("a finished refresh kept the lease %q", after.RefreshLeaseOwner)
	}
	if after.LastRefreshAttemptAt == nil || !after.LastRefreshAttemptAt.Equal(due) {
		t.Fatalf("attempt recorded as %v, want %s", after.LastRefreshAttemptAt, due)
	}

	// The failure backs the library off by its own interval rather than
	// being retried immediately, which is the whole point of scheduling
	// from the attempt.
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, due.Add(time.Minute),
		leaseFor(due.Add(time.Minute))); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("claimed %q a minute after it failed; a failing root would spin", lib.ID)
	}

	// A refresh that worked clears the error and stamps the success.
	next := due.Add(time.Hour + time.Second)
	held, ok, err := s.ClaimLibraryRefresh(ctx, next, leaseFor(next))
	if err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("the scheduled library was not due an hour after its last attempt")
	}
	if err := s.FinishLibraryRefresh(ctx, scheduled.ID,
		held.RefreshLeaseOwner, next, store.RefreshCodeNone); err != nil {
		t.Fatal(err)
	}
	after, err = s.AdminLibraryByID(ctx, scheduled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRefreshCode != store.RefreshCodeNone {
		t.Fatalf("a successful refresh left the code %q behind", after.LastRefreshCode)
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
	claimed, ok, err = s.ClaimLibraryRefresh(
		ctx, next.Add(time.Second), leaseFor(next.Add(time.Second)))
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
	if lib, ok, err := s.ClaimLibraryRefresh(ctx, next.Add(2*time.Second),
		leaseFor(next.Add(2*time.Second))); err != nil {
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

	// A code the panel has no wording for is refused rather than
	// stored: it would reach a page that cannot render it.
	if err := s.FinishLibraryRefresh(ctx, manual.ID,
		claimed.RefreshLeaseOwner, next,
		store.RefreshCode("something went wrong in /srv/manual"),
	); err != store.ErrInvalidTransition {
		t.Fatalf("an unbounded refresh code was accepted: %v", err)
	}
	if err := s.FinishLibraryRefresh(ctx, manual.ID,
		claimed.RefreshLeaseOwner, next,
		store.RefreshCodeIncompleteScan); err != nil {
		t.Fatal(err)
	}
	recorded, err := s.AdminLibraryByID(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.LastRefreshCode != store.RefreshCodeIncompleteScan {
		t.Fatalf("stored code is %q", recorded.LastRefreshCode)
	}

	if err := s.FinishLibraryRefresh(ctx, "no-such-library", "owner", next,
		store.RefreshCodeNone); err != store.ErrRefreshLeaseLost {
		t.Fatalf("finishing a refresh of nothing gave %v, want ErrRefreshLeaseLost", err)
	}
}

// leaseFor is a claim's lease in these tests: a token nobody else uses
// and an expiry a few minutes out, which is what a worker takes.
func leaseFor(now time.Time) store.RefreshLease {
	return store.RefreshLease{
		Owner: "worker-" + now.UTC().Format(time.RFC3339Nano),
		Until: now.Add(store.DefaultRefreshLease),
	}
}

// testLibraryRefreshLeaseExpires is the killed-process case: a lease
// nobody released stops holding the library once it lapses, and the
// worker that left it behind can no longer write anything.
func testLibraryRefreshLeaseExpires(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	now := time.Now().UTC().Truncate(time.Second)
	root := "/srv/abandoned"
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-lease", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source: store.LibraryDirectory, Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshInterval, RefreshInterval: time.Hour,
		Name: "Shelf", RootPath: &root, ConfigJSON: []byte(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	due := now.Add(time.Hour + time.Second)
	killed, ok, err := s.ClaimLibraryRefresh(ctx, due, leaseFor(due))
	if err != nil || !ok {
		t.Fatalf("claim: %v/%v", ok, err)
	}

	// The process holding it dies. Nothing releases the lease, so the
	// library is locked out only until the lease lapses — after which
	// the next interval claims it, with a new owner.
	lapsed := due.Add(time.Hour + store.DefaultRefreshLease + time.Second)
	taken, ok, err := s.ClaimLibraryRefresh(ctx, lapsed, leaseFor(lapsed))
	if err != nil || !ok {
		t.Fatalf("an expired lease still held the library: %v/%v", ok, err)
	}
	if taken.RefreshLeaseOwner == killed.RefreshLeaseOwner {
		t.Fatal("the takeover kept the dead worker's owner token")
	}

	// And the worker that was taken over writes nothing, whatever it
	// had already committed.
	if err := s.SetLibraryInventoryDigest(ctx, "lib-lease",
		killed.RefreshLeaseOwner, "deadbeef", lapsed,
	); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a dispossessed worker advanced the digest: %v", err)
	}
	if err := s.FinishLibraryRefresh(ctx, "lib-lease",
		killed.RefreshLeaseOwner, lapsed, store.RefreshCodeNone,
	); err != store.ErrRefreshLeaseLost {
		t.Fatalf("a dispossessed worker recorded completion: %v", err)
	}
	after, err := s.AdminLibraryByID(ctx, "lib-lease")
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRefreshAt != nil {
		t.Fatalf("a dispossessed worker recorded a success at %s", after.LastRefreshAt)
	}
	if after.LastInventoryDigest != "" {
		t.Fatalf("a dispossessed worker left the digest %q", after.LastInventoryDigest)
	}
}
