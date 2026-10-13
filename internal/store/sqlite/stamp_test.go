package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// TestWorkTranslationStampSurvivesRowLoss proves a stamp is allocated
// above the user's high-water mark even when no translation row exists
// to derive it from: the rows are a cache that eviction and work
// deletion take freely, and a clock stepped back under a cursor another
// work's pull still holds would hide every later row behind it.
func TestWorkTranslationStampSurvivesRowLoss(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := st
	u := storetest.MkUser(t, st, "stamp-keeper")
	ctx := context.Background()

	stamp := func(now time.Time) time.Time {
		t.Helper()
		var at time.Time
		if err := s.withTx(ctx, func(tx *sql.Tx) error {
			stamped, err := stampWorkTranslationTx(ctx, tx, u.ID, now)
			at, _ = parseTime(stamped)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return at
	}

	first := stamp(time.Unix(0, 10_000))
	// The clock steps back and every row is gone; the mark is not.
	second := stamp(time.Unix(0, 5_000))
	if !second.After(first) {
		t.Fatalf("a stepped-back clock wrote under the high-water mark: %v then %v", first, second)
	}
}
