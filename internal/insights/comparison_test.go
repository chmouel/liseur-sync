package insights

import (
	"math"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestSpanMinutesProratesAndRoundsLikeKotlin(t *testing.T) {
	win, ok := NewComparisonWindow("2026-09-01", "2026-09-02", "12:00:00", time.UTC)
	if !ok {
		t.Fatal("comparison window did not parse")
	}
	active := int64(1001)
	straddlingActive := int64(1400)
	exactCutoffActive := int64(250)
	snap := store.StatsSnapshot{
		Timezone: "UTC",
		Sessions: []store.Session{
			{
				StartedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
				EndedAt:   time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC),
				ActiveMs:  &active,
			},
			{
				StartedAt: time.Date(2026, 9, 1, 23, 59, 59, 0, time.UTC),
				EndedAt:   time.Date(2026, 9, 2, 0, 0, 1, 0, time.UTC),
				IdleMs:    500,
			},
			{
				StartedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
				EndedAt:   time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC),
				ActiveMs:  &straddlingActive,
			},
			{
				StartedAt: time.Date(2026, 9, 2, 11, 59, 59, 0, time.UTC),
				EndedAt:   time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
				ActiveMs:  &exactCutoffActive,
			},
			{
				StartedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
				EndedAt:   time.Date(2026, 9, 2, 12, 1, 0, 0, time.UTC),
				ActiveMs:  &active,
			},
		},
	}
	got, exact := SpanMinutes(snap, win, time.UTC)
	if !exact {
		t.Fatal("raw sessions should be exact")
	}
	// 1001 ms at a half-way cutoff rounds to 501 ms, and the fallback
	// session contributes its full 1500 ms. The sitting ending after
	// midnight still contributes the 100 ms earned before the cutoff.
	if want := float64(2351) / 60_000; math.Abs(got-want) > 1e-12 {
		t.Fatalf("minutes: got %.12f, want %.12f", got, want)
	}
}

func TestComparisonWindowUsesJavaDSTResolution(t *testing.T) {
	paris := mustLoad("Europe/Paris")
	repeat, ok := NewComparisonWindow("2026-10-25", "2026-10-25", "02:30:00", paris)
	if !ok {
		t.Fatal("repeated wall time did not parse")
	}
	if want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC); !repeat.cutoff.Equal(want) {
		t.Fatalf("repeat chose %s, want earlier occurrence %s", repeat.cutoff, want)
	}
	repeatActive := int64(time.Hour / time.Millisecond)
	repeatMinutes, exact := SpanMinutes(store.StatsSnapshot{
		Timezone: "Europe/Paris",
		Sessions: []store.Session{{
			StartedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC),
			ActiveMs:  &repeatActive,
		}},
	}, repeat, paris)
	if !exact || repeatMinutes != 30 {
		t.Fatalf("repeat proration: got %v exact=%v, want 30", repeatMinutes, exact)
	}

	newYork := mustLoad("America/New_York")
	gap, ok := NewComparisonWindow("2026-03-08", "2026-03-08", "02:30:00", newYork)
	if !ok {
		t.Fatal("skipped wall time did not parse")
	}
	if want := time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC); !gap.cutoff.Equal(want) {
		t.Fatalf("gap resolved to %s, want shifted-forward instant %s", gap.cutoff, want)
	}
	if gap.Through() != "02:30:00.000" {
		t.Fatalf("cutoff was not normalized to milliseconds: %q", gap.Through())
	}
	gapActive := int64(2 * time.Hour / time.Millisecond)
	gapMinutes, exact := SpanMinutes(store.StatsSnapshot{
		Timezone: "America/New_York",
		Sessions: []store.Session{{
			StartedAt: time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC),
			EndedAt:   time.Date(2026, 3, 8, 8, 30, 0, 0, time.UTC),
			ActiveMs:  &gapActive,
		}},
	}, gap, newYork)
	if !exact || gapMinutes != 60 {
		t.Fatalf("gap proration: got %v exact=%v, want 60", gapMinutes, exact)
	}
}

func TestComparisonWindowPreservesDateWhenMidnightIsSkipped(t *testing.T) {
	santiago := mustLoad("America/Santiago")
	win, ok := NewComparisonWindow("2026-09-06", "2026-09-06", "01:30", santiago)
	if !ok {
		t.Fatal("midnight-gap comparison did not parse")
	}
	if win.FromDay() != "2026-09-06" || win.ToDay() != "2026-09-06" {
		t.Fatalf("date labels moved across midnight gap: %s..%s", win.FromDay(), win.ToDay())
	}
	want := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	if !win.from.Equal(want) {
		t.Fatalf("day start resolved to %s, want first valid instant %s", win.from, want)
	}
	headline := DayWindow(
		time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC),
		santiago,
	)
	if headline.FromDay() != "2026-09-06" || headline.ToDay() != "2026-09-06" {
		t.Fatalf("headline date labels moved across midnight gap: %s..%s", headline.FromDay(), headline.ToDay())
	}
	if from, _ := headline.SessionBounds(time.Now(), santiago); !from.Equal(want) {
		t.Fatalf("headline day start resolved to %s, want %s", from, want)
	}
}

func TestSpanMinutesRollupEvidence(t *testing.T) {
	win, ok := NewComparisonWindow("2026-09-01", "2026-09-03", "12:00:00.250", time.UTC)
	if !ok {
		t.Fatal("comparison window did not parse")
	}
	exact := int64(60_001)
	base := store.StatsSnapshot{
		Timezone: "UTC",
		Rollups: []store.SessionRollup{{
			Day: "2026-09-02", Timezone: "UTC", AttributionVersion: 2,
			ActiveSeconds: 60.001, ComparisonActiveMs: &exact,
		}},
	}
	got, complete := SpanMinutes(base, win, time.UTC)
	if !complete || math.Abs(got-60.001/60) > 1e-12 {
		t.Fatalf("whole rollup day: got %v exact=%v", got, complete)
	}
	for _, tc := range []struct {
		name   string
		rollup store.SessionRollup
	}{
		{
			name: "partial last day",
			rollup: store.SessionRollup{
				Day: "2026-09-03", Timezone: "UTC", AttributionVersion: 2,
				ActiveSeconds: 60, ComparisonActiveMs: &exact,
			},
		},
		{
			name: "later day may hide midnight straddler",
			rollup: store.SessionRollup{
				Day: "2026-09-04", Timezone: "UTC", AttributionVersion: 2,
				ActiveSeconds: 60, ComparisonActiveMs: &exact,
			},
		},
		{
			name: "legacy rollup",
			rollup: store.SessionRollup{
				Day: "2026-09-02", ActiveSeconds: 60,
			},
		},
		{
			name: "different timezone",
			rollup: store.SessionRollup{
				Day: "2026-09-02", Timezone: "Europe/Paris", AttributionVersion: 2, ActiveSeconds: 60,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := base
			snap.Rollups = append(append([]store.SessionRollup{}, base.Rollups...), tc.rollup)
			if _, exact := SpanMinutes(snap, win, time.UTC); exact {
				t.Fatal("comparison claimed exact without timestamp evidence")
			}
		})
	}
	withoutExact := base
	withoutExact.Rollups = append([]store.SessionRollup(nil), base.Rollups...)
	withoutExact.Rollups[0].ComparisonActiveMs = nil
	if _, exact := SpanMinutes(withoutExact, win, time.UTC); exact {
		t.Fatal("pre-migration rollup claimed exact millisecond evidence")
	}
	withIrrelevantLegacy := base
	withIrrelevantLegacy.Rollups = append(append([]store.SessionRollup{}, base.Rollups...), store.SessionRollup{
		Day: "2020-01-01", ActiveSeconds: 60,
	})
	if got, exact := SpanMinutes(withIrrelevantLegacy, win, time.UTC); !exact || math.Abs(got-60.001/60) > 1e-12 {
		t.Fatalf("irrelevant legacy history disabled comparison: got %v exact=%v", got, exact)
	}
}

func TestSpanMinutesDoesNotSkipIntersectingOldTimezoneDay(t *testing.T) {
	kiritimati := mustLoad("Pacific/Kiritimati")
	win, ok := NewComparisonWindow("2026-09-02", "2026-09-02", "12:00", kiritimati)
	if !ok {
		t.Fatal("comparison window did not parse")
	}
	exact := int64(60_000)
	snap := store.StatsSnapshot{
		Timezone: "Pacific/Kiritimati",
		Rollups: []store.SessionRollup{{
			Day: "2026-09-01", Timezone: "UTC", AttributionVersion: 2,
			ActiveSeconds: 60, ComparisonActiveMs: &exact,
		}},
	}
	if _, complete := SpanMinutes(snap, win, kiritimati); complete {
		t.Fatal("old-timezone day intersecting the comparison was skipped by its stored date")
	}
}

func TestComparisonWindowRejectsSubMillisecondAndMalformedTimes(t *testing.T) {
	for _, through := range []string{"", "8:00:00", "24:00:00", "12:60:00", "12:00:60", "12:00:00.0000"} {
		if _, ok := NewComparisonWindow("2026-09-01", "2026-09-02", through, time.UTC); ok {
			t.Errorf("accepted through=%q", through)
		}
	}
	for raw, want := range map[string]string{
		"08:48":        "08:48:00.000",
		"08:48:42":     "08:48:42.000",
		"08:48:42.1":   "08:48:42.100",
		"08:48:42.12":  "08:48:42.120",
		"08:48:42.123": "08:48:42.123",
	} {
		win, ok := NewComparisonWindow("2026-09-01", "2026-09-02", raw, time.UTC)
		if !ok || win.Through() != want {
			t.Errorf("through=%q: got %q ok=%v, want %q", raw, win.Through(), ok, want)
		}
	}
}
