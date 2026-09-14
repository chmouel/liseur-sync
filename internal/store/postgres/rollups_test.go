package postgres

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func openRollupStore(t *testing.T) *Store {
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
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRollupsRejectStaleEditionPageCount(t *testing.T) {
	s := openRollupStore(t)
	storetest.RollupsRejectStaleEditionPageCount(t, s, func(userID, sha string, pages *int64) error {
		_, err := s.db.ExecContext(t.Context(), q(`UPDATE editions SET page_count = ? WHERE user_id = ? AND sha256 = ?`),
			pages, userID, sha)
		return err
	})
}

func TestSessionPagesCachesNullableCountsAndPropagatesErrors(t *testing.T) {
	s := openRollupStore(t)
	ctx := t.Context()
	user := storetest.MkUser(t, s, "page-cache")
	storetest.MkWork(t, s, user, "page-cache-work", "page-cache-sha")
	for _, tc := range []struct {
		name  string
		pages *int64
		want  float64
	}{
		{"known-page-count", storetest.Ptr(int64(100)), 25},
		{"null-page-count", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, q(`UPDATE editions SET page_count = ? WHERE user_id = ? AND sha256 = ?`),
				tc.pages, user.ID, "page-cache-sha"); err != nil {
				t.Fatal(err)
			}
			cache := make(map[string]sql.NullInt64)
			ses := store.Session{EditionSHA: storetest.Ptr("page-cache-sha")}
			got, err := sessionPages(ctx, tx, user.ID, ses, 0.25, cache)
			if err != nil || got != tc.want || len(cache) != 1 {
				t.Fatalf("initial contribution: pages=%v cache=%v err=%v", got, cache, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			got, err = sessionPages(ctx, tx, user.ID, ses, 0.5, cache)
			if err != nil || got != 2*tc.want {
				t.Fatalf("cached contribution queried the closed transaction: pages=%v err=%v", got, err)
			}
			if _, err := sessionPages(ctx, tx, user.ID, ses, 0.25, make(map[string]sql.NullInt64)); !errors.Is(err, sql.ErrTxDone) {
				t.Fatalf("query failure was swallowed: %v", err)
			}
		})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cache := make(map[string]sql.NullInt64)
	ses := store.Session{EditionSHA: storetest.Ptr("missing-edition")}
	if _, err := sessionPages(ctx, tx, user.ID, ses, 0.25, cache); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing edition was silently treated as zero pages: %v", err)
	}
	if len(cache) != 0 {
		t.Fatalf("failed lookup was cached: %v", cache)
	}
}

// TestCurrentAccountTimezoneLocksTheRow proves the timezone guard is
// worth making. Reading the zone without locking it takes no lock under
// READ COMMITTED, so a settings change could commit between the check
// and the rollup, and the batch would be filed under a zone the account
// had already left. The lock makes that update wait.
func TestCurrentAccountTimezoneLocksTheRow(t *testing.T) {
	s := openRollupStore(t)
	ctx := t.Context()
	u := store.User{ID: "tz-lock", Name: "reader", Argon2Hash: "x", Timezone: "Europe/Paris", CreatedAt: time.Now()}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tz, err := currentAccountTimezone(ctx, tx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tz != "Europe/Paris" {
		t.Fatalf("read the wrong zone: %q", tz)
	}

	// A second connection may not move the zone while that read is held.
	other, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, `SET LOCAL lock_timeout = '500ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = other.ExecContext(ctx, q(`UPDATE users SET timezone = ? WHERE id = ?`),
		"America/New_York", u.ID)
	if err == nil {
		t.Fatal("the account zone moved while the rollup held it")
	}
	if err := other.Rollback(); err != nil {
		t.Fatal(err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// And may once it is released.
	if _, err := s.db.ExecContext(ctx, q(`UPDATE users SET timezone = ? WHERE id = ?`),
		"America/New_York", u.ID); err != nil {
		t.Fatalf("the zone stayed locked after the rollup finished: %v", err)
	}
}
