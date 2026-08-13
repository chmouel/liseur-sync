//go:build linux

package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type trashStoreFake struct {
	results []store.TrashPurgeResult
	err     error
	calls   int
	limits  []int
	times   []time.Time
}

func (f *trashStoreFake) PurgeExpiredTrash(
	_ context.Context,
	at time.Time,
	limit int,
) (store.TrashPurgeResult, error) {
	f.calls++
	f.limits = append(f.limits, limit)
	f.times = append(f.times, at)
	if f.err != nil {
		return store.TrashPurgeResult{}, f.err
	}
	if len(f.results) == 0 {
		return store.TrashPurgeResult{}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func bookIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "book"
	}
	return ids
}

func TestPurgeExpiredTrashDrainsFullPages(t *testing.T) {
	fake := &trashStoreFake{results: []store.TrashPurgeResult{
		{BookIDs: bookIDs(2), FilesPurged: 3, ReservationsReleased: 1},
		{BookIDs: bookIDs(2), FilesPurged: 2, BlobsOrphaned: 2},
		{BookIDs: bookIDs(1), FilesPurged: 1},
	}}
	at := time.Date(2026, time.November, 2, 3, 0, 0, 0, time.FixedZone("x", 3600))
	report, err := PurgeExpiredTrash(context.Background(), fake, at, 2)
	if err != nil {
		t.Fatal(err)
	}
	// A short page means there is nothing left to take, so the loop stops
	// there rather than asking again.
	if report.Passes != 3 || fake.calls != 3 {
		t.Fatalf("passes: %d calls: %d, want 3 and 3", report.Passes, fake.calls)
	}
	if len(report.BookIDs) != 5 || report.FilesPurged != 6 ||
		report.ReservationsReleased != 1 || report.BlobsOrphaned != 2 {
		t.Fatalf("totals not accumulated: %+v", report)
	}
	for _, at := range fake.times {
		if at.Location() != time.UTC {
			t.Fatalf("cutoff not normalised to UTC: %v", at)
		}
	}
}

func TestPurgeExpiredTrashStopsWhenNothingExpired(t *testing.T) {
	fake := &trashStoreFake{}
	report, err := PurgeExpiredTrash(
		context.Background(), fake, time.Now().UTC(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passes != 1 || len(report.BookIDs) != 0 {
		t.Fatalf("empty purge: %+v", report)
	}
}

// TestPurgeExpiredTrashIsBoundedPerTick: deletion stops at a ceiling
// rather than running to convergence. Whatever is left is still expired
// and the next tick takes it, and nothing is served from the trash in the
// meantime, so there is no reason to hold the database for an unbounded
// number of deletes.
func TestPurgeExpiredTrashIsBoundedPerTick(t *testing.T) {
	full := make([]store.TrashPurgeResult, maxTrashPurgePasses+50)
	for i := range full {
		full[i] = store.TrashPurgeResult{BookIDs: bookIDs(2)}
	}
	fake := &trashStoreFake{results: full}
	report, err := PurgeExpiredTrash(
		context.Background(), fake, time.Now().UTC(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passes != maxTrashPurgePasses ||
		fake.calls != maxTrashPurgePasses {
		t.Fatalf("unbounded purge: passes %d calls %d", report.Passes, fake.calls)
	}
}

func TestPurgeExpiredTrashRejectsBadInput(t *testing.T) {
	at := time.Now().UTC()
	cases := []struct {
		name  string
		st    trashPurgeStore
		at    time.Time
		limit int
	}{
		{"no store", nil, at, 50},
		{"zero cutoff", &trashStoreFake{}, time.Time{}, 50},
		{"limit too small", &trashStoreFake{}, at, 0},
		{"limit too large", &trashStoreFake{}, at, 501},
	}
	for _, tc := range cases {
		if _, err := PurgeExpiredTrash(
			context.Background(), tc.st, tc.at, tc.limit,
		); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestPurgeExpiredTrashReportsStoreFailure(t *testing.T) {
	sentinel := errors.New("boom")
	fake := &trashStoreFake{err: sentinel}
	if _, err := PurgeExpiredTrash(
		context.Background(), fake, time.Now().UTC(), 50,
	); !errors.Is(err, sentinel) {
		t.Fatalf("store failure not reported: %v", err)
	}
}
