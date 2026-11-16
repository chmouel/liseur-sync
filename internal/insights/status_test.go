package insights

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TestWorkStatusResolvesTheOneRule: the snapshot's per-work status is
// the resolution rule (ADR-0066), never a threshold of its own, and
// current_progression stays the newest position.
func TestWorkStatusResolvesTheOneRule(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	active := int64(600000)
	cases := []struct {
		name       string
		position   store.Op
		reading    store.WorkReading
		wantStatus reading.Status
		wantCovers bool
	}{
		{
			name:       "explicit finished at 0.4",
			position:   store.Op{Progression: 0.4},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.4, Choice: store.ReadingStatusFinished},
			wantStatus: reading.StatusFinished,
			wantCovers: true,
		},
		{
			name:       "explicit unread above the threshold is unread, not ready to read",
			position:   store.Op{Progression: 0.99},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.99, Choice: store.ReadingStatusUnread},
			wantStatus: reading.StatusUnread,
			wantCovers: true,
		},
		{
			name:       "partial recording at 0.998 is not finished",
			position:   store.Op{Progression: 0.998, Coverage: store.CoveragePartial},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.998, Coverage: store.CoveragePartial},
			wantStatus: reading.StatusReading,
			wantCovers: false,
		},
		{
			name:       "full recording at 0.998 is finished",
			position:   store.Op{Progression: 0.998, Coverage: store.CoverageFull},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.998, Coverage: store.CoverageFull},
			wantStatus: reading.StatusFinished,
			wantCovers: true,
		},
		{
			name:       "a historical position carries no coverage and keeps its classification",
			position:   store.Op{Progression: 0.99},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.99},
			wantStatus: reading.StatusFinished,
			wantCovers: true,
		},
		{
			name:       "0.96 is reading",
			position:   store.Op{Progression: 0.96},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.96},
			wantStatus: reading.StatusReading,
			wantCovers: true,
		},
		{
			name:       "0.97 is finished",
			position:   store.Op{Progression: 0.97},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.97},
			wantStatus: reading.StatusFinished,
			wantCovers: true,
		},
		{
			// ADR-0067: the newer choices reach the snapshot too, so a
			// server-only statistics row and the shelf agree.
			name:       "explicit want to read",
			position:   store.Op{Progression: 0.4},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.4, Choice: store.ReadingStatusWantToRead},
			wantStatus: reading.StatusWantToRead,
			wantCovers: true,
		},
		{
			name:       "explicit reading before any position",
			position:   store.Op{},
			reading:    store.WorkReading{Choice: store.ReadingStatusReading},
			wantStatus: reading.StatusReading,
			wantCovers: true,
		},
		{
			// Paused at 0.99: the position would say finished, the
			// reader says otherwise, and the snapshot must not count
			// this work as read.
			name:       "paused above the threshold is not finished",
			position:   store.Op{Progression: 0.99},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.99, Choice: store.ReadingStatusPaused},
			wantStatus: reading.StatusPaused,
			wantCovers: true,
		},
		{
			name:       "did not finish above the threshold is not finished",
			position:   store.Op{Progression: 0.99},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.99, Choice: store.ReadingStatusDidNotFinish},
			wantStatus: reading.StatusDidNotFinish,
			wantCovers: true,
		},
		{
			name:       "automatic hands a new choice back to the position",
			position:   store.Op{Progression: 0.5},
			reading:    store.WorkReading{HasPosition: true, Progression: 0.5, Choice: store.ReadingStatusAutomatic},
			wantStatus: reading.StatusReading,
			wantCovers: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := store.StatsSnapshot{
				Timezone:  "UTC",
				Works:     []store.Work{{ID: "book"}},
				Positions: map[string]store.Op{"book": c.position},
				Readings:  map[string]store.WorkReading{"book": c.reading},
				Sessions: []store.Session{{
					SessionID: "s", WorkID: "book", StartedAt: now.Add(-time.Hour), EndedAt: now,
					StartProg: 0, EndProg: c.position.Progression, ActiveMs: &active,
					Origin: store.OriginNative,
				}},
			}
			got, err := Build(snap, Window{}, now)
			if err != nil {
				t.Fatal(err)
			}
			work := got.ByWork["book"]
			if work.Status != c.wantStatus {
				t.Errorf("status = %q, want %q", work.Status, c.wantStatus)
			}
			if work.CoversNarrative != c.wantCovers {
				t.Errorf("covers_narrative = %t, want %t", work.CoversNarrative, c.wantCovers)
			}
			if work.CurrentProgression != c.position.Progression {
				t.Errorf("current_progression = %v, want %v",
					work.CurrentProgression, c.position.Progression)
			}
		})
	}
}
