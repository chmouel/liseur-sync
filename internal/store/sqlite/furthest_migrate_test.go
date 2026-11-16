package sqlite

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestFurthestUpgradePreservesReading(t *testing.T) {
	ctx := t.Context()
	s, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	through, ok := migrationsThrough("furthestPositions")
	if !ok {
		t.Fatal("furthestPositions is no longer a known migration")
	}
	for i, migration := range through {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			i+1, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	u := storetest.MkUser(t, s, "existing-reader")
	w := storetest.MkWork(t, s, u, "w1", "abc123")
	// The ops are written with the columns this schema has: today's
	// AppendOps names the kind column, which is a later migration.
	for i, reading := range []struct {
		opID string
		prog float64
	}{{"peak", 0.7}, {"current", 0.31}} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
			                  progression, origin, received_at)
			 VALUES (?, ?, ?, ?, 'phone', ?, ?, 'native', ?)`,
			u.ID, i+1, reading.opID, w.ID, formatTime(time.Now()),
			reading.prog, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO seq_counters (user_id, next_seq) VALUES (?, 3)
		 ON CONFLICT(user_id) DO UPDATE SET next_seq = 3`, u.ID); err != nil {
		t.Fatal(err)
	}
	// Read through Positions: today's snapshot also reads the furthest
	// floor a later migration adds (ADR-0054) and the op kind (ADR-0066),
	// which this schema lacks, so the read happens after Migrate.
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		before, err := s.Positions(ctx, u.ID, w.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
		if err != nil || !reflect.DeepEqual(before, after.Ops) || len(after.Furthest) != 1 ||
			after.Furthest[0].OpID != "peak" || after.FurthestFloor != 0 {
			t.Fatalf("migration changed reading: before=%+v after=%+v err=%v", before, after, err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'ops_furthest'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("furthest index: count=%d err=%v", count, err)
	}
}
