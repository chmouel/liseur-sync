package sqlite

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestRollupsRejectStaleEditionPageCount(t *testing.T) {
	s := openStore(t).(*Store)
	storetest.RollupsRejectStaleEditionPageCount(t, s, func(userID, sha string, pages *int64) error {
		_, err := s.db.ExecContext(t.Context(), `UPDATE editions SET page_count = ? WHERE user_id = ? AND sha256 = ?`,
			pages, userID, sha)
		return err
	})
}

func TestSessionPagesCachesNullableCountsAndPropagatesErrors(t *testing.T) {
	s := openStore(t).(*Store)
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
			if _, err := tx.ExecContext(ctx, `UPDATE editions SET page_count = ? WHERE user_id = ? AND sha256 = ?`,
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
