//go:build linux

package content

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// refreshOptions are the limits a refresh pass runs under in these
// tests: enough to ingest a small EPUB, nothing else.
func refreshOptions(f *watchedFixture) WatchedSyncOptions {
	return WatchedSyncOptions{
		MaxFileBytes: 1 << 20,
		Patterns:     NewLibraryPatterns(f.store),
	}
}

// TestRefreshPassOnlyTouchesLibrariesThatAreDue is the schedule seen
// from the worker's side: a library that is not due is not swept, the
// same library is swept once its interval comes round, and what the
// sweep did is recorded on the library rather than only in a log.
func TestRefreshPassOnlyTouchesLibrariesThatAreDue(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("Dickens - Bleak House.epub", minimalEPUB(t))

	// Created a moment ago on a default interval: nothing is due, so a
	// pass finds no library and ingests nothing.
	report, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 || report.Ingested != 0 {
		t.Fatalf("refreshed %d libraries and ingested %d before anything was due",
			report.Libraries, report.Ingested)
	}

	// An interval later it is due, and the pass sweeps it exactly once:
	// the claim it made is also what stops the loop coming back for the
	// same library.
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 1 || report.Swept != 1 || report.Ingested != 1 {
		t.Fatalf("pass reported libraries=%d swept=%d ingested=%d, want 1/1/1",
			report.Libraries, report.Swept, report.Ingested)
	}

	lib, err := f.store.AdminLibraryByID(f.ctx, f.library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lib.LastRefreshAt == nil {
		t.Fatal("a refresh that worked recorded no success")
	}
	if lib.LastRefreshCode != store.RefreshCodeNone {
		t.Fatalf("a refresh that worked recorded the code %q", lib.LastRefreshCode)
	}

	// Immediately afterwards nothing is due again, however many times
	// the worker ticks.
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 {
		t.Fatalf("refreshed %d libraries straight after refreshing them",
			report.Libraries)
	}
}

// TestRefreshNowIsHonouredWithoutASchedule is the "Refresh now" button
// and the CLI subcommand behind it: a library with no interval at all
// is refreshed because somebody asked, once.
func TestRefreshNowIsHonouredWithoutASchedule(t *testing.T) {
	f := newWatchedFixture(t)
	if err := f.store.AdminSetLibraryConfig(
		f.ctx, f.library.OwnerUserID, f.library.ID, []byte(`{}`), f.now); err != nil {
		t.Fatal(err)
	}
	// Make it a library nothing schedules, over a root of its own, the
	// way an administrator who wants to index a directory once would.
	manualRoot := filepath.Join(t.TempDir(), "by-hand")
	if err := os.MkdirAll(manualRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manual := f.library
	manual.ID = "lib-manual"
	manual.Refresh = store.LibraryRefreshManual
	manual.Name = "Indexed by hand"
	manual.RootPath = &manualRoot
	if err := f.store.CreateLibrary(f.ctx, manual); err != nil {
		t.Fatal(err)
	}
	// The scheduled fixture library must not interfere, so put its next
	// refresh out of reach by claiming and finishing it here.
	claimAt := f.now.Add(store.DefaultRefreshInterval + time.Minute)
	if _, ok, err := f.store.ClaimLibraryRefresh(f.ctx, claimAt,
		store.RefreshLease{
			Owner: "test-claim",
			Until: claimAt.Add(store.DefaultRefreshLease),
		}); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("the fixture library was expected to be due")
	}

	if err := os.WriteFile(filepath.Join(manualRoot, "Woolf - The Waves.epub"),
		minimalEPUB(t), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 {
		t.Fatalf("a manual library was refreshed by the schedule (%d libraries)",
			report.Libraries)
	}

	if err := f.store.AdminRequestLibraryRefresh(
		f.ctx, manual.OwnerUserID, manual.ID, f.now); err != nil {
		t.Fatal(err)
	}
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 1 || report.Ingested != 1 {
		t.Fatalf("the requested refresh reported libraries=%d ingested=%d, want 1/1",
			report.Libraries, report.Ingested)
	}

	// One request, one refresh.
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 {
		t.Fatalf("one request refreshed %d times", report.Libraries+1)
	}
}

// TestRefreshRecordsAnUnreachableRootAgainstItsLibrary is what the
// admin panel reads. A root that has gone away used to be a log line
// and nothing else; now the library says so, and says so without
// touching what it already holds.
func TestRefreshRecordsAnUnreachableRootAgainstItsLibrary(t *testing.T) {
	f := newWatchedFixture(t)
	gone := f.root + "-not-here"
	if err := f.store.AdminSetLibraryConfig(
		f.ctx, f.library.OwnerUserID, f.library.ID, []byte(`{}`), f.now); err != nil {
		t.Fatal(err)
	}
	missing := f.library
	missing.ID = "lib-missing"
	missing.Name = "Unplugged"
	missing.RootPath = &gone
	if err := f.store.CreateLibrary(f.ctx, missing); err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatalf("one unreachable root failed the whole pass: %v", err)
	}
	if report.Unavailable != 1 {
		t.Fatalf("pass reported %d unavailable roots, want 1", report.Unavailable)
	}
	// The other library was still swept: one flaky disk is not a broken
	// server.
	if report.Libraries != 2 {
		t.Fatalf("pass refreshed %d libraries, want both", report.Libraries)
	}

	lib, err := f.store.AdminLibraryByID(f.ctx, missing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lib.LastRefreshCode != store.RefreshCodeRootUnavailable {
		t.Fatalf("an unreachable root recorded %q on its library, "+
			"want %q", lib.LastRefreshCode, store.RefreshCodeRootUnavailable)
	}
	if lib.LastRefreshAt != nil {
		t.Fatalf("a refresh that never read the root claimed success at %s",
			lib.LastRefreshAt)
	}
}

// TestARefreshInProgressIsNotClaimedBySecondWorker is the exclusion
// ADR-0014 asks for. A refresh that outlives its interval, or a "refresh
// now" arriving while one runs, must not put two workers on one library:
// the claim is a lease with an owner, and only an expired one is taken
// over.
func TestARefreshInProgressIsNotClaimedBySecondWorker(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("Dickens - Bleak House.epub", minimalEPUB(t))
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)

	held := store.RefreshLease{
		Owner: "worker-one",
		Until: f.now.Add(store.DefaultRefreshLease),
	}
	if _, ok, err := f.store.ClaimLibraryRefresh(
		f.ctx, f.now, held); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("the library was expected to be due")
	}

	// An administrator asks for a refresh while that one is running,
	// which is what makes the library due again immediately.
	if err := f.store.AdminRequestLibraryRefresh(
		f.ctx, f.library.OwnerUserID, f.library.ID, f.now); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	report, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 {
		t.Fatalf("a second worker claimed a library somebody holds: %+v",
			report)
	}

	// The holder is what decides when it is over. A dispossessed worker
	// cannot record its outcome, so the library keeps the state of the
	// pass that is actually running.
	if err := f.store.FinishLibraryRefresh(f.ctx, f.library.ID, "worker-two",
		f.now, store.RefreshCodeNone); !errors.Is(
		err, store.ErrRefreshLeaseLost) {
		t.Fatalf("a stranger finished somebody's refresh: %v", err)
	}

	// Once the lease expires, the library is taken over rather than
	// stranded: a worker that was killed must not hold it forever.
	f.now = f.now.Add(store.DefaultRefreshLease + time.Minute)
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 1 || report.Ingested != 1 {
		t.Fatalf("an expired lease was not taken over: %+v", report)
	}
	lib, err := f.store.AdminLibraryByID(f.ctx, f.library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lib.RefreshLeaseOwner != "" || lib.LastRefreshCode != store.RefreshCodeNone {
		t.Fatalf("a finished refresh left %+v", lib)
	}
}
