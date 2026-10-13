package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// openForStamps opens a migrated, reset store, or skips.
func openForStamps(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reset(t, s)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestWorkTranslationStampSurvivesRowLoss proves a stamp is allocated
// above the user's high-water mark even when no translation row exists
// to derive it from: the rows are a cache that eviction and work
// deletion take freely, and a clock stepped back under a cursor another
// work's pull still holds would hide every later row behind it.
func TestWorkTranslationStampSurvivesRowLoss(t *testing.T) {
	s := openForStamps(t)
	u := storetest.MkUser(t, s, "pg-stamp-keeper")
	ctx := context.Background()

	stamp := func(now time.Time) time.Time {
		t.Helper()
		var at time.Time
		if err := s.withTx(ctx, func(tx *sql.Tx) error {
			stamped, err := stampWorkTranslationTx(ctx, tx, u.ID, now)
			at = stamped
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return at
	}

	first := stamp(time.Unix(0, 10_000).UTC())
	// The clock steps back and every row is gone; the mark is not.
	second := stamp(time.Unix(0, 5_000).UTC())
	if !second.After(first) {
		t.Fatalf("a stepped-back clock wrote under the high-water mark: %v then %v", first, second)
	}
}

// TestWorkTranslationStampPrecision proves the mark is compared at the
// precision PostgreSQL actually keeps: a now less than a microsecond
// past the mark would round back onto it, and a row stamped onto its
// own high-water mark is invisible to a cursor held there.
func TestWorkTranslationStampPrecision(t *testing.T) {
	s := openForStamps(t)
	u := storetest.MkUser(t, s, "pg-stamp-precision")
	ctx := context.Background()

	stamp := func(now time.Time) time.Time {
		t.Helper()
		var at time.Time
		if err := s.withTx(ctx, func(tx *sql.Tx) error {
			stamped, err := stampWorkTranslationTx(ctx, tx, u.ID, now)
			at = stamped
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return at
	}

	first := stamp(time.Unix(0, 10_000).UTC())
	// Half a microsecond later: within the same stored microsecond.
	second := stamp(first.Add(499 * time.Nanosecond))
	if !second.After(first) {
		t.Fatalf("a sub-microsecond step rounded onto the high-water mark: %v then %v", first, second)
	}
}
