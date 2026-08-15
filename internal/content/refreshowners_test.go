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

	// A sweep meets every path again on every pass. Until this, a path
	// with no catalog file was counted as a fresh arrival each time, so a
	// book the server had already refused — or one still being published
	// — kept the owner in this list and bought a full catalog walk every
	// tick, forever.
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err = RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Swept != 1 {
		t.Fatalf("swept %d libraries, want 1", report.Swept)
	}
	if report.Ingested != 0 {
		t.Fatalf("re-ingested %d books nothing had changed about",
			report.Ingested)
	}
	if len(report.IngestedOwners) != 0 {
		t.Fatalf("IngestedOwners = %v after a pass that ingested nothing",
			report.IngestedOwners)
	}
}

// A file the server will not publish is met again by every sweep. It is
// counted apart from arrivals, and it is why a library can be short a
// book with nothing going wrong: the operator's log says refused rather
// than saying nothing at all.
func TestRefusedFilesAreNotCountedAsArrivals(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("Nobody - Not An EPUB.epub", []byte("this is not a zip archive"))
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)

	first, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	second, err := RunRefreshPass(
		f.ctx, f.store, f.cas, refreshOptions(f), f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if second.Swept != 1 {
		t.Fatalf("swept %d libraries, want 1", second.Swept)
	}
	if second.Ingested != 0 {
		t.Fatalf("counted %d arrivals on a re-sweep of one refused file "+
			"(first pass: ingested=%d refused=%d)",
			second.Ingested, first.Ingested, first.Refused)
	}
	if len(second.IngestedOwners) != 0 {
		t.Fatalf("IngestedOwners = %v for a file that was never published",
			second.IngestedOwners)
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
