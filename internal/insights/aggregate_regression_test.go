package insights

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func aggregateNear(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

func TestAggregateMeasuredTimeIsNotCappedToWallTime(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	active, zero, pages := int64(30*60*1000), int64(0), int64(200)
	sha := "edition"
	snap := store.StatsSnapshot{
		Timezone: "UTC",
		Works: []store.Work{
			{ID: "measured", Title: "Measured book", Author: "Writer"},
			{ID: "inferred"}, {ID: "unread"},
		},
		Editions:  map[string]store.Edition{sha: {PageCount: &pages}},
		Positions: map[string]store.Op{"measured": {Progression: 0.5}},
		Sessions: []store.Session{
			{
				SessionID: "measured", WorkID: "measured", EditionSHA: &sha,
				StartedAt: now.Add(-10 * time.Minute), EndedAt: now,
				IdleMs: 5 * 60 * 1000, ActiveMs: &active,
				StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
			},
			{
				SessionID: "zero", WorkID: "measured",
				StartedAt: now.Add(-time.Hour), EndedAt: now, ActiveMs: &zero,
				Origin: store.OriginNative,
			},
			{
				SessionID: "inferred", WorkID: "inferred",
				StartedAt: now.Add(-time.Hour), EndedAt: now,
				StartProg: 0, EndProg: 0.8, Origin: store.OriginInferred,
			},
		},
	}
	got, err := Build(snap, Window{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || got.Summary.Sessions != 3 || len(got.Works) != 2 {
		t.Fatalf("unexpected aggregate: %+v", got)
	}
	aggregateNear(t, "total minutes", got.Summary.TotalActiveMinutes, 90)
	aggregateNear(t, "pages", got.Summary.TotalPages, 20)
	aggregateNear(t, "measured-only pace", got.Summary.SpeedProgPerHour, 0.2)
	work := got.ByWork["measured"]
	aggregateNear(t, "measured minutes", work.TotalActiveMinutes, 30)
	if work.ETASeconds == nil {
		t.Fatal("measured progression must yield an ETA")
	}
	aggregateNear(t, "ETA seconds", *work.ETASeconds, 9000)
	if work.Sessions != 2 || work.Title != "Measured book" || work.Author != "Writer" ||
		work.LastReadAt == nil || !work.LastReadAt.Equal(now) {
		t.Errorf("work metadata/count: %+v", work)
	}
	if got.Works[0].WorkID != "inferred" || got.Works[1].WorkID != "measured" {
		t.Errorf("works not ordered by active time: %+v", got.Works)
	}
	if want := []Day{{Date: "2026-03-30", Minutes: 90, Pages: 20, Sessions: 3}}; !reflect.DeepEqual(got.Days, want) {
		t.Errorf("days: got %+v, want %+v", got.Days, want)
	}
}

func TestAggregateEndDayClippingMidnightAndZeroDuration(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, paris)
	midnight := time.Date(2026, 3, 29, 0, 0, 0, 0, paris)
	next := midnight.AddDate(0, 0, 1) // This day is only 23 hours long.
	active, zero := int64(30*60*1000), int64(0)
	snap := store.StatsSnapshot{
		Timezone: "Europe/Paris", Works: []store.Work{{ID: "book"}},
		Sessions: []store.Session{
			{SessionID: "before", WorkID: "book", StartedAt: midnight.Add(-time.Hour), EndedAt: midnight.Add(-time.Nanosecond), ActiveMs: &active},
			{SessionID: "ends-at-midnight", WorkID: "book", StartedAt: midnight.Add(-10 * time.Minute), EndedAt: midnight, ActiveMs: &active},
			{SessionID: "zero-at-midnight", WorkID: "book", StartedAt: midnight, EndedAt: midnight, ActiveMs: &zero},
			{SessionID: "ends-next-midnight", WorkID: "book", StartedAt: next.Add(-time.Hour), EndedAt: next, ActiveMs: &active},
		},
		Rollups: []store.SessionRollup{
			{WorkID: "book", Day: "2026-03-27", ActiveSeconds: 60, SessionCount: 1, Timezone: "Europe/Paris", AttributionVersion: 2},
			{WorkID: "book", Day: "2026-03-29", ActiveSeconds: 600, SessionCount: 2, Timezone: "Europe/Paris", AttributionVersion: 2},
		},
	}
	got, err := Build(snap, DayWindow(midnight, midnight, paris), now)
	if err != nil {
		t.Fatal(err)
	}
	aggregateNear(t, "end-day minutes, without proportional clipping", got.Summary.TotalActiveMinutes, 40)
	if got.Summary.Sessions != 4 || len(got.Days) != 1 || got.Days[0].Date != "2026-03-29" ||
		got.Days[0].Sessions != 4 || got.Days[0].Minutes != 40 {
		t.Errorf("midnight/zero-duration counts: %+v", got)
	}
	if got.Summary.StreakDays != 4 || got.FirstActivityDay == nil || *got.FirstActivityDay != "2026-03-27" {
		t.Errorf("all-history streak and first day must ignore display window: %+v", got)
	}
}

func TestAggregateZeroActivityDoesNotCreateAStreak(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	zero := int64(0)
	snap := store.StatsSnapshot{
		Timezone: "UTC", Works: []store.Work{{ID: "book"}},
		Sessions: []store.Session{
			{WorkID: "book", StartedAt: now, EndedAt: now, ActiveMs: &zero},
			{WorkID: "book", StartedAt: now.Add(-time.Minute), EndedAt: now, IdleMs: 60_000},
		},
	}
	got, err := Build(snap, Window{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Sessions != 2 || got.Summary.TotalActiveMinutes != 0 ||
		got.Summary.StreakDays != 0 || len(got.ActiveDays) != 0 || got.FirstActivityDay != nil {
		t.Fatalf("zero-active sessions count, but are not active days: %+v", got)
	}
}

func TestAggregateLegacyAndForeignTimezoneRollupsRemainVisible(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		version int
		zone    string
	}{
		{name: "legacy"},
		{name: "different timezone", version: 2, zone: "Europe/Paris"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := store.StatsSnapshot{
				Timezone: "UTC", Works: []store.Work{{ID: "book"}},
				Positions: map[string]store.Op{"book": {Progression: 0.5}},
				Rollups: []store.SessionRollup{{
					WorkID: "book", Day: "2026-03-29", ActiveSeconds: 600, Pages: 12,
					SessionCount: 2, ProgDelta: 0.2, Timezone: tc.zone, AttributionVersion: tc.version,
				}},
			}
			got, err := Build(snap, Window{}, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Complete || got.IncompleteReason == "" {
				t.Errorf("unverifiable attribution claimed complete: %+v", got)
			}
			aggregateNear(t, "fallback minutes", got.Summary.TotalActiveMinutes, 10)
			aggregateNear(t, "fallback pages", got.Summary.TotalPages, 12)
			if got.Summary.Sessions != 2 || got.Summary.StreakDays != 1 ||
				len(got.Days) != 1 || got.Days[0].Date != "2026-03-29" ||
				got.ByWork["book"].ETASeconds != nil || got.Summary.SpeedProgPerHour != 0 {
				t.Errorf("fallback loses totals or fabricates pace: %+v", got)
			}
		})
	}
}

func TestAggregateV2RollupsPreserveMeasuredPaceAndStableOrder(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	snap := store.StatsSnapshot{
		Timezone: "UTC", Works: []store.Work{{ID: "b"}, {ID: "a"}},
		Positions: map[string]store.Op{"a": {Progression: 0.5}},
		Rollups: []store.SessionRollup{
			{WorkID: "b", Day: "2026-03-30", ActiveSeconds: 1800, SessionCount: 1, Timezone: "UTC", AttributionVersion: 2},
			{WorkID: "a", Day: "2026-03-29", ActiveSeconds: 1800, Pages: 20, SessionCount: 2,
				Timezone: "UTC", AttributionVersion: 2, MeasuredActiveSeconds: 600, MeasuredProgDelta: 0.1},
		},
	}
	got, err := Build(snap, Window{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || len(got.Works) != 2 || got.Works[0].WorkID != "a" ||
		got.Works[1].WorkID != "b" || len(got.Days) != 2 || got.Days[0].Date != "2026-03-29" {
		t.Fatalf("v2 aggregate/order: %+v", got)
	}
	aggregateNear(t, "measured rollup pace", got.Summary.SpeedProgPerHour, 0.6)
	if got.ByWork["a"].ETASeconds == nil {
		t.Fatal("v2 measured rollup lost ETA")
	}
	aggregateNear(t, "rollup ETA", *got.ByWork["a"].ETASeconds, 3000)
}

func TestAggregateRejectsIncoherentMetadata(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	sha := "missing-edition"
	for _, tc := range []struct {
		name string
		edit func(*store.StatsSnapshot)
	}{
		{"missing edition", func(s *store.StatsSnapshot) { s.Sessions[0].EditionSHA = &sha }},
		{"invalid timezone", func(s *store.StatsSnapshot) { s.Timezone = "Not/A_Timezone" }},
		{"invalid rollup day", func(s *store.StatsSnapshot) {
			s.Rollups = []store.SessionRollup{{WorkID: "book", Day: "2026-02-30"}}
		}},
		{"negative pages", func(s *store.StatsSnapshot) { p := -1.0; s.Sessions[0].ReportedPages = &p }},
		{"NaN pages", func(s *store.StatsSnapshot) { p := math.NaN(); s.Sessions[0].ReportedPages = &p }},
		{"infinite pages", func(s *store.StatsSnapshot) { p := math.Inf(1); s.Sessions[0].ReportedPages = &p }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := store.StatsSnapshot{
				Timezone: "UTC", Works: []store.Work{{ID: "book"}},
				Sessions: []store.Session{{SessionID: "session", WorkID: "book", StartedAt: now.Add(-time.Minute), EndedAt: now, EndProg: 0.2}},
			}
			tc.edit(&snap)
			if _, err := Build(snap, Window{}, now); err == nil {
				t.Fatal("incoherent metadata must fail, not return a plausible zero-page total")
			}
		})
	}
}

func TestAggregateReportedPagesAndUnknownPageCounts(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	sha, reported := "edition", 7.0
	snap := store.StatsSnapshot{
		Timezone: "UTC", Works: []store.Work{{ID: "book"}},
		Editions: map[string]store.Edition{sha: {}},
		Sessions: []store.Session{
			{WorkID: "book", StartedAt: now.Add(-time.Minute), EndedAt: now, EndProg: 0.2, EditionSHA: &sha},
			{WorkID: "book", StartedAt: now.Add(-time.Minute), EndedAt: now, EndProg: 0.2, ReportedPages: &reported},
			{WorkID: "book", StartedAt: now.Add(-time.Minute), EndedAt: now, StartProg: 0.8, EndProg: 0.2},
		},
	}
	got, err := Build(snap, Window{}, now)
	if err != nil {
		t.Fatal(err)
	}
	aggregateNear(t, "only reported pages are known", got.Summary.TotalPages, 7)
	aggregateNear(t, "backwards progression does not subtract pace", got.Summary.SpeedProgPerHour, 8)
}
