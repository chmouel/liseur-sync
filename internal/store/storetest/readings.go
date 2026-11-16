package storetest

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// testStatusOps: a status act is one more op in the same append-only
// log, idempotent and conflicting on its own fields (ADR-0066).
func testStatusOps(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "status")
	w := MkWork(t, s, u, "w1", "abc123")

	act := store.Op{
		OpID: "act", WorkID: w.ID, ClientTS: time.Now().UTC(), Progression: 0.4,
		Origin: store.OriginNative, Kind: store.OpKindStatus,
		ReadingStatus: store.ReadingStatusFinished,
	}
	results, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{act})
	if err != nil || len(results) != 1 || results[0].Status != "applied" {
		t.Fatalf("append status act: %+v %v", results, err)
	}
	stored := results[0].Seq

	results, err = s.AppendOps(ctx, u.ID, "phone", []store.Op{act})
	if err != nil || results[0].Status != "duplicate" || results[0].Seq != stored {
		t.Fatalf("replay status act: %+v %v", results, err)
	}
	// A position op that names no kind is the same payload as one that
	// spells "position" out: the column defaults to it.
	plain := act
	plain.Kind = store.OpKindPosition
	plain.ReadingStatus = ""
	if results, err = s.AppendOps(ctx, u.ID, "phone", []store.Op{plain}); err != nil ||
		results[0].Status != "conflict" {
		t.Fatalf("kind changed under the same id must conflict: %+v %v", results, err)
	}
	unread := act
	unread.ReadingStatus = store.ReadingStatusUnread
	if results, err = s.AppendOps(ctx, u.ID, "phone", []store.Op{unread}); err != nil ||
		results[0].Status != "conflict" {
		t.Fatalf("reading_status changed under the same id must conflict: %+v %v", results, err)
	}
	covered := act
	covered.OpID = "covered"
	covered.Kind = ""
	covered.ReadingStatus = ""
	covered.Coverage = store.CoveragePartial
	if results, err = s.AppendOps(ctx, u.ID, "phone", []store.Op{covered}); err != nil ||
		results[0].Status != "applied" {
		t.Fatalf("append partial position: %+v %v", results, err)
	}
	covered.Coverage = store.CoverageFull
	if results, err = s.AppendOps(ctx, u.ID, "phone", []store.Op{covered}); err != nil ||
		results[0].Status != "conflict" {
		t.Fatalf("coverage changed under the same id must conflict: %+v %v", results, err)
	}
}

// testStatusOpsSurviveSync: changes and heads carry status acts, so a
// resync can still recover the choice, and compaction keeps them while
// every position query ignores them.
func testStatusOpsSurviveSync(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "status-sync")
	w := MkWork(t, s, u, "w1", "abc123")

	ops := []store.Op{
		{OpID: "pos-1", WorkID: w.ID, ClientTS: time.Now().UTC(), Progression: 0.2,
			Origin: store.OriginNative},
		{OpID: "pos-peak", WorkID: w.ID, ClientTS: time.Now().UTC(), Progression: 0.8,
			Origin: store.OriginNative},
		{OpID: "act", WorkID: w.ID, ClientTS: time.Now().UTC(), Progression: 0.2,
			Origin: store.OriginNative, Kind: store.OpKindStatus,
			ReadingStatus: store.ReadingStatusUnread},
	}
	if _, err := s.AppendOps(ctx, u.ID, "phone", ops); err != nil {
		t.Fatal(err)
	}

	page, err := s.Changes(ctx, u.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]store.Op{}
	for _, op := range page.Ops {
		seen[op.OpID] = op
	}
	if got := seen["act"]; got.Kind != store.OpKindStatus || got.ReadingStatus != store.ReadingStatusUnread {
		t.Fatalf("changes lost the status act: %+v", got)
	}
	// A status act is not a move: it is neither the newest position nor
	// the furthest one.
	if newest, err := s.Positions(ctx, u.ID, w.ID, 1); err != nil || len(newest) != 1 ||
		newest[0].OpID != "pos-peak" {
		t.Fatalf("positions returned a status act: %+v %v", newest, err)
	}
	snap, err := s.PositionSnapshot(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Ops) != 2 {
		t.Fatalf("snapshot ops = %d, want the two positions", len(snap.Ops))
	}
	// The same read carries the newest status act, so a reader seeding a
	// book learns the choice without a second request.
	if len(snap.Status) != 1 || snap.Status[0].OpID != "act" ||
		snap.Status[0].ReadingStatus != store.ReadingStatusUnread {
		t.Fatalf("snapshot status = %+v, want the unread act", snap.Status)
	}
	for _, op := range snap.Furthest {
		if op.Kind != store.OpKindPosition {
			t.Fatalf("furthest ranked a status act: %+v", op)
		}
	}
	stats, err := s.StatisticsSnapshot(ctx, u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pos := stats.Positions[w.ID]; pos.OpID != "pos-peak" {
		t.Fatalf("statistics latest position = %+v", pos)
	}
	rd := stats.Readings[w.ID]
	if rd.Choice != store.ReadingStatusUnread {
		t.Fatalf("statistics readings = %+v", rd)
	}

	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range heads.Ops {
		if op.OpID == "act" && op.ReadingStatus == store.ReadingStatusUnread {
			found = true
		}
	}
	if !found {
		t.Fatalf("heads must return the newest op whatever its kind: %+v", heads.Ops)
	}

	future := time.Now().Add(time.Hour)
	if _, err := s.Compact(ctx, u.ID, future); err != nil {
		t.Fatal(err)
	}
	heads, err = s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, op := range heads.Ops {
		if op.OpID == "act" {
			found = op.Kind == store.OpKindStatus && op.ReadingStatus == store.ReadingStatusUnread
		}
	}
	if !found {
		t.Fatalf("compaction dropped the status act: %+v", heads.Ops)
	}
}

// testStatusActOutlivesAStalePosition: the newest status act is the
// choice, and a position op appended after it — an older device
// catching up — changes where the reader is, never what they decided.
func testStatusActOutlivesAStalePosition(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "status-order")
	w := MkWork(t, s, u, "w1", "abc123")

	push := func(opID string, prog float64, kind store.OpKind, status store.ReadingStatus) {
		t.Helper()
		if _, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{{
			OpID: opID, WorkID: w.ID, ClientTS: time.Now().UTC(), Progression: prog,
			Origin: store.OriginNative, Kind: kind, ReadingStatus: status,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	push("pos", 0.4, "", "")
	push("act", 0.4, store.OpKindStatus, store.ReadingStatusFinished)
	push("stale", 0.5, "", "")

	readings, err := s.WorkReadings(ctx, u.ID, []string{w.ID})
	if err != nil {
		t.Fatal(err)
	}
	rd := readings[w.ID]
	if rd.Choice != store.ReadingStatusFinished || !rd.HasPosition || rd.Progression != 0.5 {
		t.Fatalf("work reading = %+v", rd)
	}
	if got := rd.Resolve(); got != reading.StatusFinished {
		t.Fatalf("a stale position changed the resolution to %q", got)
	}
	// Reading every work of the account reads the same facts.
	all, err := s.WorkReadings(ctx, u.ID, nil)
	if err != nil || len(all) != 1 || all[w.ID].Choice != store.ReadingStatusFinished {
		t.Fatalf("account readings = %+v %v", all, err)
	}
	// Another account sees nothing of it.
	other := MkUser(t, s, "status-order-other")
	mine, err := s.WorkReadings(ctx, other.ID, nil)
	if err != nil || len(mine) != 0 {
		t.Fatalf("another account's readings = %+v %v", mine, err)
	}
}

// testStatusAndPositionHeadsAreIndependent: the newest position and the
// newest status act are two heads per (work, device), not one. If either
// could hide the other, a resync would lose it — and a same-day
// compaction would delete it, because neither is the group's newest seq
// (ADR-0066).
func testStatusAndPositionHeadsAreIndependent(t *testing.T, open OpenFunc) {
	cases := []struct {
		name string
		// ops are appended in this order, the last one first in seq.
		ops []store.Op
		// wantStatus, wantProgression and wantCoverage are the resolved
		// facts every step must preserve.
		wantStatus      reading.Status
		wantProgression float64
		wantCoverage    store.Coverage
	}{
		{
			// The choice is in the middle: the newest op is a position,
			// so a kind-blind head keeps the position and drops the act.
			name: "position, finished, position",
			ops: []store.Op{
				{OpID: "pos-1", Progression: 0.4},
				{OpID: "act", Progression: 0.4, Kind: store.OpKindStatus,
					ReadingStatus: store.ReadingStatusFinished},
				{OpID: "pos-2", Progression: 0.5, Coverage: store.CoverageFull},
			},
			wantStatus:      reading.StatusFinished,
			wantProgression: 0.5,
			wantCoverage:    store.CoverageFull,
		},
		{
			// The withdrawal is last: a kind-blind head keeps the act and
			// drops the position it has to derive from.
			name: "finished, position, automatic",
			ops: []store.Op{
				{OpID: "act-1", Progression: 0.2, Kind: store.OpKindStatus,
					ReadingStatus: store.ReadingStatusFinished},
				{OpID: "pos", Progression: 0.95, Coverage: store.CoveragePartial},
				{OpID: "act-2", Progression: 0.95, Kind: store.OpKindStatus,
					ReadingStatus: store.ReadingStatusAutomatic},
			},
			wantStatus:      reading.StatusReading,
			wantProgression: 0.95,
			wantCoverage:    store.CoveragePartial,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			u := MkUser(t, s, "heads-kind")
			w := MkWork(t, s, u, "w1", "abc123")

			ops := make([]store.Op, len(c.ops))
			for i, op := range c.ops {
				op.WorkID = w.ID
				op.ClientTS = time.Now().UTC()
				op.Origin = store.OriginNative
				ops[i] = op
			}
			if _, err := s.AppendOps(ctx, u.ID, "phone", ops); err != nil {
				t.Fatal(err)
			}

			// check asserts the resolution, the newest position and its
			// coverage, from the log and from a resync.
			check := func(stage string) {
				t.Helper()
				readings, err := s.WorkReadings(ctx, u.ID, []string{w.ID})
				if err != nil {
					t.Fatal(err)
				}
				rd := readings[w.ID]
				if got := rd.Resolve(); got != c.wantStatus {
					t.Fatalf("%s: resolved %q, want %q (reading %+v)", stage, got, c.wantStatus, rd)
				}
				if !rd.HasPosition || rd.Progression != c.wantProgression {
					t.Fatalf("%s: progression = %+v, want %v", stage, rd, c.wantProgression)
				}
				if rd.Coverage != c.wantCoverage {
					t.Fatalf("%s: coverage = %q, want %q", stage, rd.Coverage, c.wantCoverage)
				}

				// A resync is built from heads alone: the newest position
				// of the work and the newest act beside it must both be
				// there, and must reach the same facts.
				heads, err := s.HeadsFor(ctx, u.ID)
				if err != nil {
					t.Fatal(err)
				}
				var newestPos *store.Op
				var newestAct *store.Op
				for i, op := range heads.Ops {
					if op.WorkID != w.ID {
						continue
					}
					if op.IsPosition() {
						newestPos = &heads.Ops[i]
					} else {
						newestAct = &heads.Ops[i]
					}
				}
				if newestAct == nil {
					t.Fatalf("%s: heads returned no status act: %+v", stage, heads.Ops)
				}
				if newestPos == nil {
					t.Fatalf("%s: heads returned no position op: %+v", stage, heads.Ops)
				}
				replayed := store.WorkReading{
					WorkID: w.ID, Choice: newestAct.ReadingStatus, HasPosition: true,
					Progression: newestPos.Progression, Coverage: newestPos.Coverage,
				}
				if got := replayed.Resolve(); got != c.wantStatus {
					t.Fatalf("%s: heads-based resync resolved %q, want %q (reading %+v)",
						stage, got, c.wantStatus, replayed)
				}
				if replayed.Progression != c.wantProgression || replayed.Coverage != c.wantCoverage {
					t.Fatalf("%s: heads-based resync = %+v", stage, replayed)
				}
			}

			check("before compaction")
			horizon, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if horizon == 0 {
				t.Fatal("nothing was compacted, so this case proves nothing")
			}
			check("after compaction")
		})
	}
}

// testStatusOrderingAcrossDevices: the choice is the newest act by seq,
// never the newest clock. Two devices marking the same book differently
// while one is offline end on the act the reader made last, whichever
// client_ts it claims; a replayed delivery does not take the choice
// back; and an automatic act withdraws an earlier one (ADR-0066).
func testStatusOrderingAcrossDevices(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "status-devices")
	w := MkWork(t, s, u, "w1", "abc123")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	push := func(device, opID string, kind store.OpKind, status store.ReadingStatus, at time.Time) string {
		t.Helper()
		results, err := s.AppendOps(ctx, u.ID, device, []store.Op{{
			OpID: opID, WorkID: w.ID, ClientTS: at, Progression: 0.5,
			Origin: store.OriginNative, Kind: kind, ReadingStatus: status,
		}})
		if err != nil {
			t.Fatalf("%s: %v", opID, err)
		}
		return results[0].Status
	}
	choice := func(stage string) (store.ReadingStatus, reading.Status) {
		t.Helper()
		readings, err := s.WorkReadings(ctx, u.ID, []string{w.ID})
		if err != nil {
			t.Fatal(err)
		}
		return readings[w.ID].Choice, readings[w.ID].Resolve()
	}

	push("phone", "pos", "", "", t0)
	// The tablet's choice is the *later* clock and arrives first; the
	// phone's is the earlier clock and arrives after, so a server that
	// ordered by client_ts would pick the wrong act.
	if got := push("tablet", "act-unread", store.OpKindStatus, store.ReadingStatusUnread, t0.Add(time.Hour)); got != "applied" {
		t.Fatalf("tablet act: %s", got)
	}
	if got := push("phone", "act-finished", store.OpKindStatus, store.ReadingStatusFinished, t0); got != "applied" {
		t.Fatalf("phone act: %s", got)
	}
	if got, resolved := choice("offline act"); got != store.ReadingStatusFinished || resolved != reading.StatusFinished {
		t.Fatalf("the offline act did not win by seq: choice=%q status=%q", got, resolved)
	}
	// A replayed delivery of the losing act is a duplicate: it neither
	// appends nor reclaims the choice.
	if got := push("tablet", "act-unread", store.OpKindStatus, store.ReadingStatusUnread, t0.Add(time.Hour)); got != "duplicate" {
		t.Fatalf("replay: %s", got)
	}
	if got, _ := choice("after replay"); got != store.ReadingStatusFinished {
		t.Fatalf("a duplicate delivery reclaimed the choice: %q", got)
	}
	// Withdrawing is itself an act, and the position decides again.
	if got := push("phone", "act-auto", store.OpKindStatus, store.ReadingStatusAutomatic, t0.Add(2*time.Second)); got != "applied" {
		t.Fatalf("automatic act: %s", got)
	}
	if got, resolved := choice("withdrawn"); got != store.ReadingStatusAutomatic || resolved != reading.StatusReading {
		t.Fatalf("automatic did not withdraw the choice: choice=%q status=%q", got, resolved)
	}
	for _, status := range []store.ReadingStatus{
		store.ReadingStatusWantToRead, store.ReadingStatusReading,
		store.ReadingStatusPaused, store.ReadingStatusDidNotFinish,
	} {
		id := "act-" + string(status)
		if got := push("tablet", id, store.OpKindStatus, status, t0); got != "applied" {
			t.Fatalf("%s: %s", status, got)
		}
		push("phone", "pos-"+string(status), "", "", t0)
		if got, resolved := choice(id); got != status || string(resolved) != string(status) {
			t.Fatalf("position changed %s: choice=%q status=%q", status, got, resolved)
		}
		if got := push("tablet", id, store.OpKindStatus, status, t0); got != "duplicate" {
			t.Fatalf("%s replay: %s", status, got)
		}
	}
	push("phone", "last-auto", store.OpKindStatus, store.ReadingStatusAutomatic, t0)
	if got, resolved := choice("last automatic"); got != store.ReadingStatusAutomatic || resolved != reading.StatusReading {
		t.Fatalf("automatic did not withdraw extended choice: choice=%q status=%q", got, resolved)
	}
}
