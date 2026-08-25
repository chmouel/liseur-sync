package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// annWrite is one plausible highlight write against a work.
func annWrite(id, workID string, baseRev int64) store.AnnotationWrite {
	return store.AnnotationWrite{
		ID:          id,
		BaseRev:     baseRev,
		WorkID:      workID,
		Kind:        store.AnnotationHighlight,
		LocatorJSON: []byte(`{"href":"/ch1.xhtml","locations":{"progression":0.25}}`),
		Progression: Ptr(0.25),
		Excerpt:     "a passage worth keeping",
		Color:       "yellow",
		Body:        "and a thought about it",
		ClientTS:    time.Now(),
	}
}

func pushOne(t *testing.T, s store.Store, userID, deviceID string, w store.AnnotationWrite) store.AnnotationResult {
	t.Helper()
	res, err := s.PushAnnotations(context.Background(), userID, deviceID, []store.AnnotationWrite{w}, 2000)
	if err != nil {
		t.Fatal(err)
	}
	return res[0]
}

func testAnnotationPushIdempotencyAndRevConflict(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "annie")
	w := MkWork(t, s, u, "w1", "abc123")

	// Create.
	item := annWrite("a1", w.ID, 0)
	r := pushOne(t, s, u.ID, "d1", item)
	if r.Status != "applied" || r.Rev != 1 || r.Seq == 0 {
		t.Fatalf("create: %+v", r)
	}

	// The same annotation pushed twice is stored once.
	r = pushOne(t, s, u.ID, "d1", item)
	if r.Status != "duplicate" || r.Rev != 1 {
		t.Fatalf("retry of create: %+v", r)
	}
	if list, err := s.WorkAnnotations(ctx, u.ID, w.ID); err != nil || len(list) != 1 {
		t.Fatalf("stored more than once: %d %v", len(list), err)
	}

	// Edit with the rev the client last saw.
	edit := item
	edit.BaseRev, edit.Color, edit.Body = 1, "green", "reworded"
	r = pushOne(t, s, u.ID, "d1", edit)
	if r.Status != "applied" || r.Rev != 2 {
		t.Fatalf("edit: %+v", r)
	}
	editSeq := r.Seq
	r = pushOne(t, s, u.ID, "d1", edit)
	if r.Status != "duplicate" || r.Rev != 2 || r.Seq != editSeq {
		t.Fatalf("retry of edit: %+v", r)
	}

	// A push with a stale rev is a conflict carrying the server copy,
	// and changes nothing.
	stale := item
	stale.BaseRev, stale.Color = 1, "pink"
	r = pushOne(t, s, u.ID, "d1", stale)
	if r.Status != "conflict" || r.Server == nil || r.Server.Rev != 2 || r.Server.Color != "green" {
		t.Fatalf("stale edit: %+v", r)
	}
	list, err := s.WorkAnnotations(ctx, u.ID, w.ID)
	if err != nil || len(list) != 1 || list[0].Color != "green" || list[0].Rev != 2 {
		t.Fatalf("stale edit changed something: %+v %v", list, err)
	}

	// A delete with a stale rev is the same conflict.
	dr, err := s.DeleteAnnotation(ctx, u.ID, "a1", 1)
	if err != nil || dr.Status != "conflict" || dr.Server == nil || dr.Server.Rev != 2 {
		t.Fatalf("stale delete: %+v %v", dr, err)
	}

	// The matching delete writes the tombstone; deleting a tombstone is
	// already accepted.
	dr, err = s.DeleteAnnotation(ctx, u.ID, "a1", 2)
	if err != nil || dr.Status != "applied" || dr.Rev != 3 {
		t.Fatalf("delete: %+v %v", dr, err)
	}
	again, err := s.DeleteAnnotation(ctx, u.ID, "a1", 99)
	if err != nil || again.Status != "duplicate" || again.Rev != 3 || again.Seq != dr.Seq {
		t.Fatalf("delete of tombstone: %+v %v", again, err)
	}
	if _, err := s.DeleteAnnotation(ctx, u.ID, "nope", 1); err != store.ErrNotFound {
		t.Fatalf("delete unknown id: want ErrNotFound, got %v", err)
	}

	// The tombstone carries nothing but identity, rev, seq and when.
	page, err := s.AnnotationChanges(ctx, u.ID, 0, 100)
	if err != nil || len(page.Annotations) != 1 {
		t.Fatalf("changes: %+v %v", page, err)
	}
	tomb := page.Annotations[0]
	if !tomb.Deleted() || tomb.ID != "a1" || tomb.Rev != 3 {
		t.Fatalf("tombstone identity: %+v", tomb)
	}
	if tomb.LocatorJSON != nil || tomb.Progression != nil || tomb.EditionSHA != nil ||
		tomb.Excerpt != "" || tomb.Color != "" || tomb.Body != "" ||
		tomb.DeviceID != "" || !tomb.ClientTS.IsZero() {
		t.Fatalf("tombstone kept content: %+v", tomb)
	}

	// A deliberate, rev-matching write onto the tombstone recreates the
	// record: the reader saw the deletion and decided otherwise.
	revive := item
	revive.BaseRev = 3
	r = pushOne(t, s, u.ID, "d1", revive)
	if r.Status != "applied" || r.Rev != 4 {
		t.Fatalf("write onto tombstone: %+v", r)
	}
	if list, err := s.WorkAnnotations(ctx, u.ID, w.ID); err != nil || len(list) != 1 || list[0].Deleted() {
		t.Fatalf("revived record: %+v %v", list, err)
	}

	// One bad item fails alone.
	batch := []store.AnnotationWrite{
		annWrite("a2", w.ID, 0),
		annWrite("a3", "no-such-work", 0),
	}
	results, err := s.PushAnnotations(ctx, u.ID, "d1", batch, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "applied" || results[1].Status != "invalid" || results[1].Reason == "" {
		t.Fatalf("mixed batch: %+v", results)
	}
}

func testAnnotationCapPerWork(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "capped")
	w := MkWork(t, s, u, "w1", "abc123")

	const capN = 5
	var items []store.AnnotationWrite
	for i := 0; i < capN+1; i++ {
		items = append(items, annWrite(fmt.Sprintf("a-%d", i), w.ID, 0))
	}
	results, err := s.PushAnnotations(ctx, u.ID, "d1", items, capN)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < capN; i++ {
		if results[i].Status != "applied" {
			t.Fatalf("under cap refused: %+v", results[i])
		}
	}
	if results[capN].Status != "invalid" {
		t.Fatalf("over cap accepted: %+v", results[capN])
	}

	// An edit of an existing record is not a new live one.
	edit := items[0]
	edit.BaseRev, edit.Color = 1, "blue"
	res, err := s.PushAnnotations(ctx, u.ID, "d1", []store.AnnotationWrite{edit}, capN)
	if err != nil || res[0].Status != "applied" {
		t.Fatalf("edit at cap: %+v %v", res, err)
	}

	// A live edit naming an edition the work does not have is a
	// per-item invalid, and its neighbor is untouched by it — the
	// reference check runs before the composite FK ever could.
	badEdit := items[0]
	badEdit.BaseRev, badEdit.EditionSHA = 2, Ptr("no-such-edition")
	goodEdit := items[2]
	goodEdit.BaseRev, goodEdit.Color = 1, "pink"
	res, err = s.PushAnnotations(ctx, u.ID, "d1",
		[]store.AnnotationWrite{badEdit, goodEdit}, capN)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "invalid" || res[0].Reason == "" {
		t.Fatalf("edit to unknown edition: %+v", res[0])
	}
	if res[1].Status != "applied" {
		t.Fatalf("neighbor of bad edition edit: %+v", res[1])
	}
	kept, err := s.WorkAnnotations(ctx, u.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range kept {
		if a.ID == "a-0" && (a.Rev != 2 || a.EditionSHA != nil) {
			t.Fatalf("refused edit changed the record: %+v", a)
		}
	}

	// A deletion frees a slot.
	if _, err := s.DeleteAnnotation(ctx, u.ID, "a-1", 1); err != nil {
		t.Fatal(err)
	}
	res, err = s.PushAnnotations(ctx, u.ID, "d1",
		[]store.AnnotationWrite{annWrite("a-free", w.ID, 0)}, capN)
	if err != nil || res[0].Status != "applied" {
		t.Fatalf("push after freeing a slot: %+v %v", res, err)
	}
}

func testAnnotationCapUnderConcurrency(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cap-race")
	w := MkWork(t, s, u, "w1", "abc123")

	const capN = 10
	const writers = 4
	var wg sync.WaitGroup
	applied := make(chan int, writers)
	errs := make(chan error, writers)
	for d := 0; d < writers; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			var items []store.AnnotationWrite
			for i := 0; i < capN; i++ {
				items = append(items, annWrite(fmt.Sprintf("d%d-a%d", d, i), w.ID, 0))
			}
			results, err := s.PushAnnotations(ctx, u.ID, fmt.Sprintf("dev-%d", d), items, capN)
			if err != nil {
				errs <- err
				return
			}
			n := 0
			for _, r := range results {
				if r.Status == "applied" {
					n++
				} else if r.Status != "invalid" {
					errs <- fmt.Errorf("unexpected status %q", r.Status)
					return
				}
			}
			applied <- n
		}(d)
	}
	wg.Wait()
	close(errs)
	close(applied)
	for err := range errs {
		t.Fatal(err)
	}
	total := 0
	for n := range applied {
		total += n
	}
	if total != capN {
		t.Fatalf("concurrent creates squeezed past the cap: %d applied, cap %d", total, capN)
	}
	if list, err := s.WorkAnnotations(ctx, u.ID, w.ID); err != nil || len(list) != capN {
		t.Fatalf("live records: %d %v", len(list), err)
	}
}

func testAnnotationChangesFeed(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "feed")
	w := MkWork(t, s, u, "w1", "abc123")

	const n = 7
	for i := 0; i < n; i++ {
		item := annWrite(fmt.Sprintf("a-%d", i), w.ID, 0)
		item.Progression = Ptr(float64(i) / 10)
		if r := pushOne(t, s, u.ID, "d1", item); r.Status != "applied" {
			t.Fatalf("push %d: %+v", i, r)
		}
	}

	// Page through with the /v1/changes contract: advance since to the
	// last seq received; a pull since a cursor misses no committed
	// write and returns records in seq order.
	var seen []store.Annotation
	since := int64(0)
	for {
		page, err := s.AnnotationChanges(ctx, u.ID, since, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page.Annotations {
			if a.Seq <= since {
				t.Fatalf("out of order: seq %d after cursor %d", a.Seq, since)
			}
			since = a.Seq
			seen = append(seen, a)
		}
		if !page.HasMore {
			if page.HighWater != since {
				t.Fatalf("high water %d, cursor %d", page.HighWater, since)
			}
			break
		}
	}
	if len(seen) != n {
		t.Fatalf("feed lost records: %d of %d", len(seen), n)
	}

	// An edited record moves to the head of the stream with its current
	// content; the client keys by id and the newest rev wins.
	edit := annWrite("a-2", w.ID, 1)
	edit.Progression, edit.Color = Ptr(0.2), "purple"
	r := pushOne(t, s, u.ID, "d1", edit)
	if r.Status != "applied" {
		t.Fatalf("edit: %+v", r)
	}
	page, err := s.AnnotationChanges(ctx, u.ID, since, 100)
	if err != nil || len(page.Annotations) != 1 {
		t.Fatalf("edited record not at head: %+v %v", page, err)
	}
	if got := page.Annotations[0]; got.ID != "a-2" || got.Rev != 2 || got.Color != "purple" {
		t.Fatalf("edited record content: %+v", got)
	}
	since = page.Annotations[0].Seq

	// A tombstone is a feed record like any other.
	if _, err := s.DeleteAnnotation(ctx, u.ID, "a-0", 1); err != nil {
		t.Fatal(err)
	}
	page, err = s.AnnotationChanges(ctx, u.ID, since, 100)
	if err != nil || len(page.Annotations) != 1 || !page.Annotations[0].Deleted() {
		t.Fatalf("tombstone missing from feed: %+v %v", page, err)
	}
}

func testAnnotationTombstoneSweep(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "sweep")
	w := MkWork(t, s, u, "w1", "abc123")

	if r := pushOne(t, s, u.ID, "d1", annWrite("a1", w.ID, 0)); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	if r := pushOne(t, s, u.ID, "d1", annWrite("a2", w.ID, 0)); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	if _, err := s.DeleteAnnotation(ctx, u.ID, "a1", 1); err != nil {
		t.Fatal(err)
	}

	// A sweep with a cutoff before the deletion keeps the tombstone.
	n, err := s.SweepAnnotationTombstones(ctx, u.ID, time.Now().Add(-time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("early sweep: %d %v", n, err)
	}
	// Past the window it goes; the live record stays.
	n, err = s.SweepAnnotationTombstones(ctx, u.ID, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	page, err := s.AnnotationChanges(ctx, u.ID, 0, 100)
	if err != nil || len(page.Annotations) != 1 || page.Annotations[0].ID != "a2" {
		t.Fatalf("after sweep: %+v %v", page, err)
	}

	// After the sweep the id is simply unknown: pushing it again
	// creates a new record, rev starting over at 1.
	r := pushOne(t, s, u.ID, "d1", annWrite("a1", w.ID, 0))
	if r.Status != "applied" || r.Rev != 1 {
		t.Fatalf("push after sweep: %+v", r)
	}
	if _, err := s.DeleteAnnotation(ctx, u.ID, "a2", 99); err != nil {
		// a2 is live at rev 1; this stale delete must conflict, not error.
		t.Fatal(err)
	}
}

func testAnnotationSplitMergeReassignment(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mover")
	w := MkWork(t, s, u, "w1", "abc123")

	anchored := annWrite("with-edition", w.ID, 0)
	anchored.EditionSHA = Ptr("abc123")
	if r := pushOne(t, s, u.ID, "d1", anchored); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	loose := annWrite("no-edition", w.ID, 0)
	if r := pushOne(t, s, u.ID, "d1", loose); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	before, err := s.AnnotationChanges(ctx, u.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	cursor := before.HighWater

	// In a split, one with an edition_sha follows its edition; one
	// without stays with the surviving work.
	if err := s.SplitWork(ctx, u.ID, w.ID, "abc123", nil,
		store.Work{ID: "w2", UserID: u.ID, Title: "split", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	moved, err := s.WorkAnnotations(ctx, u.ID, "w2")
	if err != nil || len(moved) != 1 || moved[0].ID != "with-edition" || moved[0].Rev != 2 {
		t.Fatalf("split move: %+v %v", moved, err)
	}
	stayed, err := s.WorkAnnotations(ctx, u.ID, w.ID)
	if err != nil || len(stayed) != 1 || stayed[0].ID != "no-edition" || stayed[0].Rev != 1 {
		t.Fatalf("split keep: %+v %v", stayed, err)
	}

	// A moved annotation is a write like any other: it resurfaces in
	// the feed with its new work_id.
	page, err := s.AnnotationChanges(ctx, u.ID, cursor, 100)
	if err != nil || len(page.Annotations) != 1 {
		t.Fatalf("moved record not in feed: %+v %v", page, err)
	}
	if got := page.Annotations[0]; got.ID != "with-edition" || got.WorkID != "w2" {
		t.Fatalf("feed record after split: %+v", got)
	}
	cursor = page.HighWater

	// In a merge, all of them follow to the survivor.
	if err := s.MergeWorks(ctx, u.ID, "w2", w.ID); err != nil {
		t.Fatal(err)
	}
	all, err := s.WorkAnnotations(ctx, u.ID, w.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("merge: %+v %v", all, err)
	}
	page, err = s.AnnotationChanges(ctx, u.ID, cursor, 100)
	if err != nil || len(page.Annotations) != 1 || page.Annotations[0].ID != "with-edition" ||
		page.Annotations[0].WorkID != w.ID || page.Annotations[0].Rev != 3 {
		t.Fatalf("feed record after merge: %+v %v", page, err)
	}
}

func testAnnotationCrossUserIsolation(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "iso-alice")
	eve := MkUser(t, s, "iso-eve")
	w := MkWork(t, s, alice, "w1", "abc123")
	MkWork(t, s, eve, "w1", "def456") // same work id, different reader

	if r := pushOne(t, s, alice.ID, "d1", annWrite("a1", w.ID, 0)); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}

	// No route, ever, returns another reader's annotations.
	if list, err := s.WorkAnnotations(ctx, eve.ID, w.ID); err != nil || len(list) != 0 {
		t.Fatalf("cross-user work list: %+v %v", list, err)
	}
	if page, err := s.AnnotationChanges(ctx, eve.ID, 0, 100); err != nil ||
		len(page.Annotations) != 0 || page.HighWater != 0 {
		t.Fatalf("cross-user feed: %+v %v", page, err)
	}
	if _, err := s.DeleteAnnotation(ctx, eve.ID, "a1", 1); err != store.ErrNotFound {
		t.Fatalf("cross-user delete: want ErrNotFound, got %v", err)
	}
	// Eve pushing the same id creates her own record — it cannot see,
	// conflict with, or edit Alice's.
	r := pushOne(t, s, eve.ID, "d9", annWrite("a1", w.ID, 0))
	if r.Status != "applied" || r.Rev != 1 || r.Seq != 1 {
		t.Fatalf("same id, other user: %+v", r)
	}
	got, err := s.WorkAnnotations(ctx, alice.ID, w.ID)
	if err != nil || len(got) != 1 || got[0].DeviceID != "d1" {
		t.Fatalf("alice's record touched: %+v %v", got, err)
	}
}

func testAnnotationDeleteWorkCascade(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cascade")
	w := MkWork(t, s, u, "w1", "abc123")

	if r := pushOne(t, s, u.ID, "d1", annWrite("live", w.ID, 0)); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	if r := pushOne(t, s, u.ID, "d1", annWrite("gone", w.ID, 0)); r.Status != "applied" {
		t.Fatalf("push: %+v", r)
	}
	if _, err := s.DeleteAnnotation(ctx, u.ID, "gone", 1); err != nil {
		t.Fatal(err)
	}

	// DeleteWork removes the work's annotations and tombstones in the
	// same transaction as everything else.
	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.AnnotationChanges(ctx, u.ID, 0, 100)
	if err != nil || len(page.Annotations) != 0 {
		t.Fatalf("annotations survived DeleteWork: %+v %v", page, err)
	}
}

// testConcurrentAnnotationPush is the annotation counter's own
// concurrency property, with the weaker guarantee a state feed needs:
// seq values are unique and assigned in commit order — gaps are
// harmless. The op counter's gap-free property test is untouched.
func testConcurrentAnnotationPush(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "ann-prop")
	w := MkWork(t, s, u, "w1", "abc123")

	const devices = 8
	const perDevice = 25

	var wg sync.WaitGroup
	errs := make(chan error, devices)
	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			dev := fmt.Sprintf("dev-%d", d)
			for b := 0; b < perDevice; b += 5 {
				var batch []store.AnnotationWrite
				for i := 0; i < 5 && b+i < perDevice; i++ {
					batch = append(batch, annWrite(fmt.Sprintf("%s-a-%d", dev, b+i), w.ID, 0))
				}
				results, err := s.PushAnnotations(ctx, u.ID, dev, batch, 10_000)
				if err != nil {
					errs <- err
					return
				}
				for _, r := range results {
					if r.Status != "applied" {
						errs <- fmt.Errorf("%s: %s status %s", dev, r.ID, r.Status)
						return
					}
				}
			}
		}(d)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	page, err := s.AnnotationChanges(ctx, u.ID, 0, devices*perDevice+10)
	if err != nil {
		t.Fatal(err)
	}
	want := devices * perDevice
	if len(page.Annotations) != want {
		t.Fatalf("want %d records, got %d", want, len(page.Annotations))
	}
	seen := map[int64]bool{}
	last := int64(0)
	for _, a := range page.Annotations {
		if seen[a.Seq] {
			t.Fatalf("seq %d assigned twice", a.Seq)
		}
		seen[a.Seq] = true
		if a.Seq <= last {
			t.Fatalf("feed not in seq order: %d after %d", a.Seq, last)
		}
		last = a.Seq
	}
	if page.HighWater < last {
		t.Fatalf("high water %d below last seq %d", page.HighWater, last)
	}
}
