package storetest

import (
	"errors"
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

func opIDs(ops []store.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.OpID)
	}
	return out
}

func appendAt(t *testing.T, s store.Store, userID, workID string, ops ...store.Op) {
	t.Helper()
	for i := range ops {
		ops[i].WorkID = workID
		ops[i].ClientTS = time.Now()
		ops[i].Origin = store.OriginNative
	}
	if _, err := s.AppendOps(t.Context(), userID, "phone", ops); err != nil {
		t.Fatal(err)
	}
}

func testForgetFurthest(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "forgetful")
	other := MkUser(t, s, "neighbour")
	w := MkWork(t, s, u, "w1", "abc123")
	MkWork(t, s, other, "w1", "abc123")
	onlyMine := MkWork(t, s, u, "w2", "def456")
	appendAt(t, s, other.ID, w.ID, store.Op{OpID: "theirs", Progression: 0.9})
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "jump", Progression: 0.98},
		store.Op{OpID: "back", Progression: 0.3},
		store.Op{OpID: "reading", Progression: 0.31})

	snapshot, err := s.PositionSnapshot(ctx, u.ID, w.ID, 10)
	if err != nil || !reflect.DeepEqual(opIDs(snapshot.Furthest), []string{"jump"}) || snapshot.FurthestFloor != 0 {
		t.Fatalf("before forgetting: %+v %v", snapshot, err)
	}
	if heads, err := s.HeadsFor(ctx, u.ID); err != nil || len(heads.FurthestFloors) != 0 {
		t.Fatalf("a floor nobody set: %+v %v", heads, err)
	}

	// Clamped to the newest op the work holds, never above it.
	floor, err := s.ForgetFurthest(ctx, u.ID, w.ID, 1000)
	if err != nil || floor != 3 {
		t.Fatalf("forget: %d %v", floor, err)
	}
	snapshot, err = s.PositionSnapshot(ctx, u.ID, w.ID, 10)
	if err != nil || len(snapshot.Furthest) != 0 || snapshot.FurthestFloor != 3 ||
		!reflect.DeepEqual(opIDs(snapshot.Ops), []string{"reading", "back", "jump"}) {
		t.Fatalf("forgotten peak still ranks, or the log changed: %+v %v", snapshot, err)
	}
	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil || len(heads.Furthest) != 0 || len(heads.Ops) != 1 ||
		!reflect.DeepEqual(heads.FurthestFloors, map[string]int64{w.ID: 3}) {
		t.Fatalf("heads after forgetting: %+v %v", heads, err)
	}
	// Forgetting appends no op: a device already caught up on the feed
	// learns the floor from an empty changes page.
	page, err := s.Changes(ctx, u.ID, 3, 100)
	if err != nil || len(page.Ops) != 0 ||
		!reflect.DeepEqual(page.FurthestFloors, map[string]int64{w.ID: 3}) {
		t.Fatalf("changes after forgetting: %+v %v", page, err)
	}

	// Idempotent, and the floor never falls.
	for _, upto := range []int64{3, 1, 0} {
		if floor, err := s.ForgetFurthest(ctx, u.ID, w.ID, upto); err != nil || floor != 3 {
			t.Fatalf("forget upto %d: %d %v", upto, floor, err)
		}
	}

	// Reading after the forget counts again.
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "later", Progression: 0.4},
		store.Op{OpID: "lower", Progression: 0.35})
	snapshot, err = s.PositionSnapshot(ctx, u.ID, w.ID, 10)
	if err != nil || !reflect.DeepEqual(opIDs(snapshot.Furthest), []string{"later"}) {
		t.Fatalf("later reading did not count: %+v %v", snapshot, err)
	}

	// A work with no ops answers its current floor.
	if floor, err := s.ForgetFurthest(ctx, u.ID, onlyMine.ID, 0); err != nil || floor != 0 {
		t.Fatalf("empty work: %d %v", floor, err)
	}
	if floor, err := s.ForgetFurthest(ctx, u.ID, onlyMine.ID, 10); err != nil || floor != 0 {
		t.Fatalf("empty work clamp: %d %v", floor, err)
	}
	if _, err := s.ForgetFurthest(ctx, u.ID, w.ID, -1); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("negative upto_seq: %v", err)
	}
	for _, workID := range []string{onlyMine.ID, "no-such-work"} {
		if _, err := s.ForgetFurthest(ctx, other.ID, workID, 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("forgot a work that is not theirs (%s): %v", workID, err)
		}
	}
	// The same work id under another account keeps its own furthest.
	theirs, err := s.PositionSnapshot(ctx, other.ID, w.ID, 10)
	if err != nil || theirs.FurthestFloor != 0 || !reflect.DeepEqual(opIDs(theirs.Furthest), []string{"theirs"}) {
		t.Fatalf("another account's furthest moved: %+v %v", theirs, err)
	}
	if heads, err := s.HeadsFor(ctx, other.ID); err != nil || len(heads.FurthestFloors) != 0 {
		t.Fatalf("another account saw a floor: %+v %v", heads, err)
	}
}

func testForgetFurthestCompacts(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "compact-forgotten")
	w := MkWork(t, s, u, "w1", "abc123")
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "jump", Progression: 0.98},
		store.Op{OpID: "back", Progression: 0.2},
		store.Op{OpID: "reading", Progression: 0.3})
	if _, err := s.ForgetFurthest(ctx, u.ID, w.ID, 3); err != nil {
		t.Fatal(err)
	}
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "new-peak", Progression: 0.5},
		store.Op{OpID: "latest", Progression: 0.45})
	if _, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The forgotten peak is no longer pinned; the peak above the floor
	// and the head are.
	snapshot, err := s.PositionSnapshot(ctx, u.ID, w.ID, 10)
	if err != nil || !reflect.DeepEqual(opIDs(snapshot.Ops), []string{"latest", "new-peak"}) ||
		!reflect.DeepEqual(opIDs(snapshot.Furthest), []string{"new-peak"}) {
		t.Fatalf("compaction after forgetting: %+v %v", snapshot, err)
	}
}

func testForgetFurthestFollowsSplitAndMerge(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "split-forgotten")
	w := MkWork(t, s, u, "w1", "abc123")
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "edition-jump", EditionSHA: Ptr("abc123"), Progression: 0.9},
		store.Op{OpID: "loose-jump", Progression: 0.8})
	if _, err := s.ForgetFurthest(ctx, u.ID, w.ID, 2); err != nil {
		t.Fatal(err)
	}
	appendAt(t, s, u.ID, w.ID,
		store.Op{OpID: "edition-after", EditionSHA: Ptr("abc123"), Progression: 0.2},
		store.Op{OpID: "loose-after", Progression: 0.1})
	split := store.Work{ID: "split", UserID: u.ID, CreatedAt: time.Now()}
	if err := s.SplitWork(ctx, u.ID, w.ID, "abc123", nil, split); err != nil {
		t.Fatal(err)
	}
	check := func(workID string, floor int64, ids ...string) {
		t.Helper()
		snapshot, err := s.PositionSnapshot(ctx, u.ID, workID, 10)
		if err != nil || snapshot.FurthestFloor != floor || !reflect.DeepEqual(opIDs(snapshot.Furthest), ids) {
			t.Fatalf("work %s: floor %d furthest %v (%v), want %d %v",
				workID, snapshot.FurthestFloor, opIDs(snapshot.Furthest), err, floor, ids)
		}
	}
	check(split.ID, 2, "edition-after")
	check(w.ID, 2, "loose-after")

	// The survivor keeps the higher floor, whichever side held it.
	if floor, err := s.ForgetFurthest(ctx, u.ID, split.ID, 100); err != nil || floor != 3 {
		t.Fatalf("forget on split: %d %v", floor, err)
	}
	if err := s.MergeWorks(ctx, u.ID, split.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	check(w.ID, 3, "loose-after")
	other := MkWork(t, s, u, "w3", "def456")
	appendAt(t, s, u.ID, other.ID, store.Op{OpID: "other-edition", EditionSHA: Ptr("def456"), Progression: 0.6})
	if err := s.MergeWorks(ctx, u.ID, other.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	check(w.ID, 3, "other-edition", "loose-after")
	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil || !reflect.DeepEqual(heads.FurthestFloors, map[string]int64{w.ID: 3}) {
		t.Fatalf("heads floors after merge: %+v %v", heads, err)
	}
}
