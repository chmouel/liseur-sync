package insights_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestStatisticsRevisionHasStableFloatingPointTotals(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	snapshot := store.StatsSnapshot{Timezone: "UTC"}
	var expected float64
	for i := range 32 {
		active := int64(1)
		if i == 0 {
			active = 9_000_000_000_000_000
		}
		id := fmt.Sprintf("work-%02d", i)
		snapshot.Works = append(snapshot.Works, store.Work{ID: id})
		snapshot.Sessions = append(snapshot.Sessions, store.Session{
			SessionID: id, WorkID: id, StartedAt: now.Add(-time.Second),
			EndedAt: now, ActiveMs: &active, Origin: store.OriginNative,
		})
		expected += float64(active) / 1000 / 60
	}
	for range 100 {
		result, err := insights.Build(snapshot, insights.Window{}, now)
		if err != nil {
			t.Fatal(err)
		}
		if result.Summary.TotalActiveMinutes != expected {
			t.Fatalf("unchanged snapshot changed numeric identity: got %.17g, want %.17g",
				result.Summary.TotalActiveMinutes, expected)
		}
	}
}
