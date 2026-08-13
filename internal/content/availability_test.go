//go:build linux

package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type availabilityStoreFake struct {
	results []store.CatalogAvailabilityResult
	err     error
	calls   int
	limits  []int
	times   []time.Time
}

func (f *availabilityStoreFake) ReconcileCatalogAvailability(
	_ context.Context,
	at time.Time,
	limit int,
) (store.CatalogAvailabilityResult, error) {
	f.calls++
	f.limits = append(f.limits, limit)
	f.times = append(f.times, at)
	if f.err != nil {
		return store.CatalogAvailabilityResult{}, f.err
	}
	if len(f.results) == 0 {
		return store.CatalogAvailabilityResult{}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestReconcileCatalogAvailabilityRunsToConvergence(t *testing.T) {
	fake := &availabilityStoreFake{results: []store.CatalogAvailabilityResult{
		{FilesMarkedMissing: 2, BooksMarkedMissing: 1},
		{FilesMarkedAvailable: 1, BooksMarkedActive: 3},
		{},
	}}
	at := time.Date(2026, time.September, 4, 8, 0, 0, 0, time.FixedZone("x", 3600))
	report, err := ReconcileCatalogAvailability(
		context.Background(), fake, at, 25)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passes != 3 {
		t.Fatalf("passes: got %d want 3", report.Passes)
	}
	if report.FilesMarkedMissing != 2 || report.FilesMarkedAvailable != 1 ||
		report.BooksMarkedMissing != 1 || report.BooksMarkedActive != 3 {
		t.Fatalf("totals not accumulated across passes: %+v", report)
	}
	for _, limit := range fake.limits {
		if limit != 25 {
			t.Fatalf("page size not forwarded: %v", fake.limits)
		}
	}
	for _, got := range fake.times {
		if got.Location() != time.UTC {
			t.Fatalf("timestamp not normalized to UTC: %v", got)
		}
	}
}

func TestReconcileCatalogAvailabilityStopsOnAStoreThatNeverSettles(t *testing.T) {
	// A store that always reports work would otherwise pin the maintenance
	// goroutine forever.
	fake := &availabilityStoreFake{}
	forever := make([]store.CatalogAvailabilityResult, maxAvailabilityPasses+5)
	for i := range forever {
		forever[i] = store.CatalogAvailabilityResult{FilesMarkedMissing: 1}
	}
	fake.results = forever
	_, err := ReconcileCatalogAvailability(
		context.Background(), fake, time.Now(), 10)
	if !errors.Is(err, store.ErrInvariantViolation) {
		t.Fatalf("non-converging store: got %v", err)
	}
	if fake.calls != maxAvailabilityPasses {
		t.Fatalf("calls: got %d want %d", fake.calls, maxAvailabilityPasses)
	}
}

func TestReconcileCatalogAvailabilityRejectsBadArguments(t *testing.T) {
	fake := &availabilityStoreFake{}
	at := time.Now()
	cases := []struct {
		name  string
		st    catalogAvailabilityStore
		at    time.Time
		limit int
	}{
		{"nil store", nil, at, 10},
		{"zero time", fake, time.Time{}, 10},
		{"limit too small", fake, at, 0},
		{"limit too large", fake, at, 501},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReconcileCatalogAvailability(
				context.Background(), tc.st, tc.at, tc.limit,
			); !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("got %v", err)
			}
			if fake.calls != 0 {
				t.Fatal("store called with invalid arguments")
			}
		})
	}
}

func TestReconcileCatalogAvailabilityPropagatesStoreErrors(t *testing.T) {
	sentinel := errors.New("boom")
	fake := &availabilityStoreFake{err: sentinel}
	if _, err := ReconcileCatalogAvailability(
		context.Background(), fake, time.Now(), 10,
	); !errors.Is(err, sentinel) {
		t.Fatalf("got %v", err)
	}
}
