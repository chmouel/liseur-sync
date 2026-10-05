package storetest

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testFurthestSurvivesRereading(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "furthest")
	w := MkWork(t, s, u, "w1", "abc123")
	peak := store.Op{
		OpID: "peak", WorkID: w.ID, EditionSHA: Ptr("abc123"),
		Progression: 0.7, LocatorJSON: []byte(`{"href":"chapter7.xhtml","locations":{"totalProgression":0.7}}`),
		ClientTS: time.Now().UTC(), Origin: store.OriginNative,
	}
	ops := []store.Op{peak}
	for i := range 220 {
		op := peak
		op.OpID = fmt.Sprintf("reread-%d", i)
		op.Progression = 0.31
		op.LocatorJSON = []byte(`{"href":"chapter3.xhtml","locations":{"totalProgression":0.31}}`)
		ops = append(ops, op)
	}
	if _, err := s.AppendOps(ctx, u.ID, "phone", ops); err != nil {
		t.Fatal(err)
	}
	before, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Ops) != 1 || before.Ops[0].OpID != "reread-219" ||
		len(before.Furthest) != 1 || before.Furthest[0].OpID != peak.OpID {
		t.Fatalf("latest and furthest must be independent: %+v", before)
	}
	if _, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("compaction changed the snapshot: before=%+v after=%+v", before, after)
	}
	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if heads.SnapshotSeq != 221 || !reflect.DeepEqual(heads.Ops, after.Ops) ||
		!reflect.DeepEqual(heads.Furthest, after.Furthest) {
		t.Fatalf("recovery lost furthest: %+v", heads)
	}
	replay, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{peak})
	if err != nil || len(replay) != 1 || replay[0].Status != "duplicate" || replay[0].Seq != 1 {
		t.Fatalf("retained peak must remain idempotent: %+v %v", replay, err)
	}
	// Equal fractions keep the original locator; a single tiny advance counts.
	equal := peak
	equal.OpID = "same-fraction"
	equal.LocatorJSON = []byte(`{"href":"another-anchor.xhtml"}`)
	if _, err := s.AppendOps(ctx, u.ID, "browser", []store.Op{equal}); err != nil {
		t.Fatal(err)
	}
	same, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
	if err != nil || !reflect.DeepEqual(same.Furthest, before.Furthest) {
		t.Fatalf("equal fraction replaced the original: %+v %v", same, err)
	}
	equal.OpID = "one-small-page"
	equal.Progression += 0.00001
	if _, err := s.AppendOps(ctx, u.ID, "browser", []store.Op{equal}); err != nil {
		t.Fatal(err)
	}
	advanced, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
	if err != nil || len(advanced.Furthest) != 1 || advanced.Furthest[0].OpID != equal.OpID {
		t.Fatalf("small advance was lost: %+v %v", advanced, err)
	}
}

func testFurthestOwnership(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "owner")
	other := MkUser(t, s, "other")
	w := MkWork(t, s, u, "w1", "abc123")
	otherWork := MkWork(t, s, other, "other-w", "abc123")
	ops := []store.Op{
		{OpID: "edition-peak", EditionSHA: Ptr("abc123"), Progression: 0.7},
		{OpID: "alias-peak", OriginAlias: Ptr("partial-md5:md5-w1"), Progression: 0.8, ForeignPos: Ptr("/body/peak")},
		{OpID: "unattributed-peak", Progression: 0.9},
		{OpID: "zero", EditionSHA: Ptr("abc123"), Progression: 0},
	}
	for i := range ops {
		ops[i].WorkID = w.ID
		ops[i].ClientTS = time.Now()
		ops[i].Origin = store.OriginNative
		if ops[i].OriginAlias != nil {
			ops[i].Origin = store.OriginKosync
		}
	}
	if _, err := s.AppendOps(ctx, u.ID, "phone", ops); err != nil {
		t.Fatal(err)
	}
	// A second account's zero is a real furthest candidate, not missing data.
	if _, err := s.AppendOps(ctx, other.ID, "phone", []store.Op{{
		OpID: "zero", WorkID: otherWork.ID, Progression: 0,
		ClientTS: time.Now(), Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	split := store.Work{ID: "split", UserID: u.ID, CreatedAt: time.Now()}
	if err := s.SplitWork(ctx, u.ID, w.ID, "abc123",
		[]store.Identifier{{Kind: "partial-md5", Value: "md5-w1"}}, split); err != nil {
		t.Fatal(err)
	}
	check := func(workID string, ids ...string) {
		t.Helper()
		snapshot, err := s.PositionSnapshot(ctx, u.ID, workID, 1)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(snapshot.Furthest))
		for _, op := range snapshot.Furthest {
			if op.WorkID != workID || op.UserID != u.ID {
				t.Fatalf("wrong owner: %+v", op)
			}
			got = append(got, op.OpID)
		}
		if !reflect.DeepEqual(got, ids) {
			t.Fatalf("work %s: furthest %v, want %v", workID, got, ids)
		}
	}
	check(w.ID, "unattributed-peak")
	check(split.ID, "alias-peak", "edition-peak")
	if err := s.MergeWorks(ctx, u.ID, split.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	check(w.ID, "unattributed-peak", "alias-peak", "edition-peak")
	foreign, err := s.PositionSnapshot(ctx, other.ID, w.ID, 1)
	if err != nil || len(foreign.Ops) != 0 || len(foreign.Furthest) != 0 {
		t.Fatalf("another user's work leaked: %+v %v", foreign, err)
	}
	heads, err := s.HeadsFor(ctx, other.ID)
	if err != nil || len(heads.Furthest) != 1 || heads.Furthest[0].Progression != 0 ||
		heads.Furthest[0].WorkID != otherWork.ID {
		t.Fatalf("zero or tenant isolation failed: %+v %v", heads, err)
	}
	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.HeadsFor(ctx, u.ID)
	if err != nil || len(deleted.Ops) != 0 || len(deleted.Furthest) != 0 {
		t.Fatalf("deleted work retained reading: %+v %v", deleted, err)
	}
}

func testFurthestSnapshot(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "snapshot-furthest")
	w := MkWork(t, s, u, "w1", "abc123")
	done := make(chan error, 1)
	go func() {
		for i := range 60 {
			_, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{{
				OpID: fmt.Sprintf("op-%d", i), WorkID: w.ID,
				Progression: float64(i) / 100, ClientTS: time.Now(), Origin: store.OriginNative,
			}})
			if err != nil {
				done <- err
				return
			}

		}
		done <- nil
	}()
	// Always join the writer before the store is closed, including on failure.
	defer func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for range 60 {
		heads, err := s.HeadsFor(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(heads.Furthest) == 0 {
			if heads.SnapshotSeq != 0 {
				t.Fatalf("nonempty snapshot without furthest: %+v", heads)
			}
		} else if heads.Furthest[0].Seq != heads.SnapshotSeq ||
			!reflect.DeepEqual(heads.Ops, heads.Furthest) {
			t.Fatalf("heads and furthest came from different snapshots: %+v", heads)
		}
		snapshot, err := s.PositionSnapshot(ctx, u.ID, w.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot.Ops, snapshot.Furthest) {
			t.Fatalf("history and furthest came from different snapshots: %+v", snapshot)
		}
	}
}

func testFurthestReplacementKeepsHorizon(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "horizon-furthest")
	w := MkWork(t, s, u, "w1", "abc123")
	for i, fraction := range []float64{0.7, 0.2, 0.3} {
		if _, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{{
			OpID: fmt.Sprintf("old-%d", i), WorkID: w.ID, Progression: fraction,
			ClientTS: time.Now(), Origin: store.OriginNative,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now()
	horizon, err := s.Compact(ctx, u.ID, cutoff)
	if err != nil || horizon != 2 {
		t.Fatalf("first compaction: %d %v", horizon, err)
	}
	if _, err := s.AppendOps(ctx, u.ID, "phone", []store.Op{{
		OpID: "new-peak", WorkID: w.ID, Progression: 0.8,
		ClientTS: time.Now(), Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	// Only old seq 1 is newly disposable: seq 3 remains the old daily
	// snapshot, and seq 4 is newer than the cutoff.
	horizon, err = s.Compact(ctx, u.ID, cutoff)
	if err != nil || horizon != 2 {
		t.Fatalf("replacing a peak moved the horizon backwards: %d %v", horizon, err)
	}
	page, err := s.Changes(ctx, u.ID, 1, 100)
	if err != nil || !page.ResyncNeeded {
		t.Fatalf("cursor behind a removed op was accepted: %+v %v", page, err)
	}
}
