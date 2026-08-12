package infer

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func op(workID, dev string, at time.Time, prog float64, seq int64) store.Op {
	return store.Op{
		OpID:        dev + "-" + time.Duration(seq).String(),
		WorkID:      workID,
		DeviceID:    dev,
		Progression: prog,
		ReceivedAt:  at,
		Seq:         seq,
	}
}

func TestGrouping(t *testing.T) {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	ops := []store.Op{
		op("w1", "kobo", base, 0.1, 1),
		op("w1", "kobo", base.Add(5*time.Minute), 0.12, 2),
		op("w1", "kobo", base.Add(10*time.Minute), 0.15, 3),
		// 20-minute gap -> new session.
		op("w1", "kobo", base.Add(30*time.Minute), 0.16, 4),
		// different device -> new session.
		op("w1", "phone", base.Add(31*time.Minute), 0.16, 5),
		// different work -> new session.
		op("w2", "kobo", base.Add(32*time.Minute), 0.5, 6),
	}
	groups := Group(ops, 15*time.Minute)
	if len(groups) != 4 {
		t.Fatalf("want 4 groups, got %d", len(groups))
	}
	if len(groups[0]) != 3 || len(groups[1]) != 1 || len(groups[2]) != 1 || len(groups[3]) != 1 {
		t.Fatalf("bad group sizes: %d %d %d %d",
			len(groups[0]), len(groups[1]), len(groups[2]), len(groups[3]))
	}
}

func TestClosedGroupsLateness(t *testing.T) {
	now := time.Now()
	ops := []store.Op{
		op("w1", "kobo", now.Add(-48*time.Hour), 0.1, 1),
		op("w1", "kobo", now.Add(-48*time.Hour+5*time.Minute), 0.2, 2),
		// Recent open group.
		op("w1", "kobo", now.Add(-30*time.Minute), 0.3, 3),
	}
	closed := ClosedGroups(ops, 15*time.Minute, now.Add(-24*time.Hour))
	if len(closed) != 1 || len(closed[0]) != 2 {
		t.Fatalf("want 1 closed group of 2, got %v", closed)
	}
}

func TestClosedGroupsKeepsGroupStraddlingCutoffOpen(t *testing.T) {
	cutoff := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	ops := []store.Op{
		op("w1", "kobo", cutoff.Add(-5*time.Minute), 0.1, 1),
		op("w1", "kobo", cutoff.Add(5*time.Minute), 0.2, 2),
	}
	if closed := ClosedGroups(ops, 15*time.Minute, cutoff); len(closed) != 0 {
		t.Fatalf("group straddling lateness cutoff was closed: %+v", closed)
	}
}

func TestMaterializeDeterministic(t *testing.T) {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	g := []store.Op{
		op("w1", "kobo", base, 0.1, 1),
		op("w1", "kobo", base.Add(10*time.Minute), 0.2, 2),
	}
	s1 := Materialize("u1", g)
	s2 := Materialize("u1", g)
	if s1.SessionID != s2.SessionID {
		t.Fatal("non-deterministic session id")
	}
	if s1.Origin != store.OriginInferred || s1.StartProg != 0.1 || s1.EndProg != 0.2 {
		t.Fatalf("bad materialization: %+v", s1)
	}
	if !s1.EndedAt.Equal(base.Add(10 * time.Minute)) {
		t.Fatalf("end: %v", s1.EndedAt)
	}
}

func TestGroupingPreservesAliasProvenance(t *testing.T) {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	firstAlias, secondAlias := "partial-md5:first", "partial-md5:second"
	first := op("w1", "kobo", base, 0.1, 1)
	first.OriginAlias = &firstAlias
	second := op("w1", "kobo", base.Add(time.Minute), 0.2, 2)
	second.OriginAlias = &secondAlias
	groups := Group([]store.Op{first, second}, 15*time.Minute)
	if len(groups) != 2 {
		t.Fatalf("different aliases grouped together: %+v", groups)
	}
	if got := Materialize("u1", groups[0]).OriginAlias; got == nil || *got != firstAlias {
		t.Fatalf("origin alias not preserved: %v", got)
	}
}
