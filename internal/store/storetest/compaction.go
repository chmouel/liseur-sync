package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCompactionHorizon: the backend-agnostic half of the compaction
// contract. A cutoff in the future makes every op old, so what survives
// is exactly the per-(work, device) heads; the horizon is the newest seq
// deleted; a cursor below it is told to resync; a cursor at or above it
// reads a complete, gap-free stream; heads are whole and seq is not
// renumbered. (The SQLite package keeps the day-boundary snapshot test,
// which needs raw control over received_at.)
func testCompactionHorizon(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "compact")
	w := MkWork(t, s, u, "w1", "abc123")
	future := time.Now().Add(time.Hour)

	push := func(dev string, n int) {
		var ops []store.Op
		for i := 0; i < n; i++ {
			ops = append(ops, store.Op{
				OpID: fmt.Sprintf("%s-%d", dev, i), WorkID: w.ID, ClientTS: time.Now(),
				Progression: float64(i) / 10, Origin: store.OriginNative,
			})
		}
		if _, err := s.AppendOps(ctx, u.ID, dev, ops); err != nil {
			t.Fatal(err)
		}
	}
	push("kobo", 3)  // seq 1..3
	push("phone", 2) // seq 4..5

	horizon, err := s.Compact(ctx, u.ID, future)
	if err != nil {
		t.Fatal(err)
	}
	// Heads 3 (kobo) and 5 (phone) stay; 1, 2 and 4 go.
	if horizon != 4 {
		t.Fatalf("horizon: want 4, got %d", horizon)
	}
	if h, err := s.CompactionHorizon(ctx, u.ID); err != nil || h != 4 {
		t.Fatalf("stored horizon: %d %v", h, err)
	}

	page, err := s.Changes(ctx, u.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !page.ResyncNeeded || page.HighWater != 5 {
		t.Fatalf("since=0 below the horizon: %+v", page)
	}
	page, err = s.Changes(ctx, u.ID, horizon, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.ResyncNeeded || len(page.Ops) != 1 || page.Ops[0].Seq != 5 {
		t.Fatalf("since=horizon: %+v", page)
	}
	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads.Ops) != 2 || heads.SnapshotSeq != 5 {
		t.Fatalf("heads after compaction: %+v", heads)
	}

	// Nothing to delete is a no-op that leaves the horizon alone.
	if h, err := s.Compact(ctx, u.ID, future); err != nil || h != 0 {
		t.Fatalf("idle compaction: %d %v", h, err)
	}
	if h, _ := s.CompactionHorizon(ctx, u.ID); h != 4 {
		t.Fatalf("idle compaction moved the horizon to %d", h)
	}
}

// testChangesIsASnapshot: the changes feed must never hand out a page
// with a hole in it. Read piecewise, the horizon check could pass and a
// compaction commit before the page is read, deleting rows the client
// then advances past without ever being told to resync. One goroutine
// appends and compacts as fast as it can; another reads pages and
// checks that a page it was not told to resync is contiguous and that
// the high water mark is never behind the last op on the page.
func testChangesIsASnapshot(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "snapshot")
	w := MkWork(t, s, u, "w1", "abc123")
	future := time.Now().Add(time.Hour)

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	var writeErr error
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, err := s.AppendOps(ctx, u.ID, "dev", []store.Op{{
				OpID: fmt.Sprintf("op-%d", i), WorkID: w.ID, ClientTS: time.Now(),
				Progression: 0.5, Origin: store.OriginNative,
			}})
			if err == nil {
				_, err = s.Compact(ctx, u.ID, future)
			}
			if err != nil {
				writeErr = err
				return
			}
		}
	}()

	var since int64
	for i := 0; i < 300; i++ {
		page, err := s.Changes(ctx, u.ID, since, 50)
		if err != nil {
			t.Fatal(err)
		}
		if page.ResyncNeeded {
			heads, err := s.HeadsFor(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			since = heads.SnapshotSeq
			continue
		}
		for j, o := range page.Ops {
			if o.Seq != since+int64(j)+1 {
				t.Fatalf("page has a hole: since=%d ops=%v", since, seqs(page.Ops))
			}
			if o.Seq > page.HighWater {
				t.Fatalf("high water %d behind op %d", page.HighWater, o.Seq)
			}
		}
		if n := len(page.Ops); n > 0 {
			since = page.Ops[n-1].Seq
		}
	}
	close(stop)
	writer.Wait()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
}

func seqs(ops []store.Op) []int64 {
	out := make([]int64, len(ops))
	for i, o := range ops {
		out[i] = o.Seq
	}
	return out
}
