package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// TestCompaction verifies the plan's compaction contract: daily
// last-op-per-(work, device) snapshots survive, heads survive, seq is
// never renumbered, and clients below the horizon get resync_required
// while /v1/heads still returns complete state.
func TestCompaction(t *testing.T) {
	st := openStore(t)
	s := st.(*Store)
	ctx := context.Background()
	u := storetest.MkUser(t, st, "henry")
	w := storetest.MkWork(t, st, u, "w1", "abc123")

	// Two devices, three days of ops (received_at is server-side; we
	// cheat by inserting directly to control it).
	old := time.Now().Add(-400 * 24 * time.Hour)
	insertRaw := func(seq int64, opID, dev string, at time.Time, prog float64) {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO ops (user_id, seq, op_id, work_id, edition_sha, device_id,
			                  client_ts, progression, origin, received_at)
			 VALUES (?, ?, ?, ?, NULL, ?, ?, ?, 'kosync', ?)`,
			u.ID, seq, opID, w.ID, dev, formatTime(at), prog, formatTime(at))
		if err != nil {
			t.Fatal(err)
		}
	}
	// Day 1: three ops on kobo.
	insertRaw(1, "d1-a", "kobo", old, 0.1)
	insertRaw(2, "d1-b", "kobo", old.Add(time.Hour), 0.2)
	insertRaw(3, "d1-c", "kobo", old.Add(2*time.Hour), 0.3)
	// Day 2: one op on kobo, one on phone.
	insertRaw(4, "d2-a", "kobo", old.Add(24*time.Hour), 0.4)
	insertRaw(5, "d2-b", "phone", old.Add(24*time.Hour), 0.35)
	// Recent head ops (inside retention).
	insertRaw(6, "head-kobo", "kobo", time.Now(), 0.9)
	insertRaw(7, "head-phone", "phone", time.Now(), 0.88)

	horizon, err := s.Compact(ctx, u.ID, time.Now().Add(-180*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Deletable ops are seq 1 and 2 (non-snapshot, non-head); horizon is
	// the max deleted seq.
	if horizon != 2 {
		t.Fatalf("horizon: want 2, got %d", horizon)
	}

	// Survivors: daily snapshots (seq 3 day1 kobo, seq 4 day2 kobo,
	// seq 5 day2 phone) + heads (6, 7). 1, 2 compacted away.
	page, err := s.Changes(ctx, u.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !page.ResyncNeeded {
		t.Fatal("since=0 below horizon should require resync")
	}
	page, err = s.Changes(ctx, u.ID, horizon, 100)
	if err != nil {
		t.Fatal(err)
	}
	// From the horizon forward the stream is complete: snapshots 3,4,5
	// plus heads 6,7.
	if len(page.Ops) != 5 || page.Ops[0].Seq != 3 {
		t.Fatalf("post-horizon ops: %+v", page.Ops)
	}

	// Heads intact.
	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads.Ops) != 2 || heads.SnapshotSeq != 7 {
		t.Fatalf("heads: %+v", heads)
	}

	// Snapshots still visible in history.
	pos, err := s.Positions(ctx, u.ID, w.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 5 {
		t.Fatalf("want 5 surviving ops, got %d", len(pos))
	}
	var _ = store.Op{}
}
