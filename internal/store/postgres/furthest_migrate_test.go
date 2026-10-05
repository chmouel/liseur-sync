package postgres

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestFurthestUpgradePreservesReading(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	ctx := t.Context()
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reset(t, s)
	through, ok := migrationsThrough("furthestPositions")
	if !ok {
		t.Fatal("furthestPositions is no longer a known migration")
	}
	for i, migration := range through {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES ($1, $2)`,
			i+1, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	u := storetest.MkUser(t, s, "existing-reader")
	w := storetest.MkWork(t, s, u, "w1", "abc123")
	if _, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{
		{OpID: "peak", WorkID: w.ID, Progression: 0.7, ClientTS: time.Now(), Origin: store.OriginNative},
		{OpID: "current", WorkID: w.ID, Progression: 0.31, ClientTS: time.Now(), Origin: store.OriginNative},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		after, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("migration changed reading: before=%+v after=%+v err=%v", before, after, err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'ops_furthest'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("furthest index: count=%d err=%v", count, err)
	}
}
