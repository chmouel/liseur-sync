//go:build linux

package content

import (
	"slices"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// A book only reaches a reader's shelf once it is joined to that
// reader's own work, and until this the join happened the first time a
// client opened the book. That made a freshly filled library look
// broken: covers and a Read button for nothing in it. Mapping is a
// work-graph write and so not this package's to do, but knowing who
// needs it is, and a pass that ingested is the only thing that knows.
func TestRefreshPassNamesOwnersOfNewlyIngestedBooks(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("Dickens - Bleak House.epub", minimalEPUB(t))
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)

	report, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Ingested != 1 {
		t.Fatalf("ingested %d books, want 1", report.Ingested)
	}
	if want := []string{f.library.OwnerUserID}; !slices.Equal(report.IngestedOwners, want) {
		t.Fatalf("IngestedOwners = %v, want %v", report.IngestedOwners, want)
	}

	// A pass with nothing due has nobody to map. Reporting an owner
	// anyway would mean a walk of that reader's whole catalog on every
	// tick, for every library, forever.
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 0 {
		t.Fatalf("swept %d libraries straight after sweeping them",
			report.Libraries)
	}
	if len(report.IngestedOwners) != 0 {
		t.Fatalf("IngestedOwners = %v after a pass that ingested nothing",
			report.IngestedOwners)
	}
}

// One owner appearing twice in a pass is one owner to map. Backfill
// walks a reader's whole catalog, so a duplicate is a second full walk
// that can only find what the first one already mapped.
func TestIngestedOwnersAreRecordedOnce(t *testing.T) {
	var report WatchedScanReport
	report.noteIngestedOwner("u1")
	report.noteIngestedOwner("u2")
	report.noteIngestedOwner("u1")
	report.noteIngestedOwner("")

	if want := []string{"u1", "u2"}; !slices.Equal(report.IngestedOwners, want) {
		t.Fatalf("IngestedOwners = %v, want %v", report.IngestedOwners, want)
	}
}
