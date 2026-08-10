package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestConcurrentAppendGapFreeSeq is the plan's property test: any
// interleaving of batched pushes from N devices yields a totally
// ordered, gap-free per-user seq.
func TestConcurrentAppendGapFreeSeq(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "prop")
	w := mkWork(t, s, u)

	const devices = 8
	const opsPerDevice = 25

	var wg sync.WaitGroup
	errs := make(chan error, devices)
	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			dev := fmt.Sprintf("dev-%d", d)
			for b := 0; b < opsPerDevice; b += 5 {
				var batch []store.Op
				for i := 0; i < 5 && b+i < opsPerDevice; i++ {
					n := b + i
					batch = append(batch, store.Op{
						OpID:        fmt.Sprintf("%s-op-%d", dev, n),
						WorkID:      w.ID,
						EditionSHA:  ptr("abc123"),
						ClientTS:    time.Now(),
						Progression: float64(n) / 100,
						Origin:      store.OriginNative,
					})
				}
				res, err := s.AppendOps(ctx, u.ID, dev, batch)
				if err != nil {
					errs <- err
					return
				}
				for _, r := range res {
					if r.Status != "applied" {
						errs <- fmt.Errorf("%s: op %s status %s", dev, r.OpID, r.Status)
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

	page, err := s.Changes(ctx, u.ID, 0, devices*opsPerDevice+10)
	if err != nil {
		t.Fatal(err)
	}
	want := devices * opsPerDevice
	if len(page.Ops) != want {
		t.Fatalf("want %d ops, got %d", want, len(page.Ops))
	}
	for i, o := range page.Ops {
		if o.Seq != int64(i+1) {
			t.Fatalf("gap at position %d: seq=%d", i, o.Seq)
		}
	}
}
