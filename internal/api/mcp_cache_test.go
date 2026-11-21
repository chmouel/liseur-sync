package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentTextBudgetCharge(t *testing.T) {
	budget := newTextBudget(2500)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const calls = 32
	var ready, done sync.WaitGroup
	ready.Add(calls)
	done.Add(calls)
	start := make(chan struct{})
	var accepted atomic.Int64
	for range calls {
		go func() {
			defer done.Done()
			if left, _ := budget.remaining("grant", now); left != 2500 {
				t.Errorf("initial allowance: %d", left)
			}
			ready.Done()
			<-start
			if ok, _ := budget.charge("grant", 1024, now); ok {
				accepted.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("accepted %d charges, want 2", accepted.Load())
	}
	if left, _ := budget.remaining("grant", now); left != 452 {
		t.Fatalf("remaining allowance: %d", left)
	}
	if left, _ := budget.remaining("another", now); left != 2500 {
		t.Fatalf("another grant's allowance: %d", left)
	}
	if left, _ := budget.remaining("grant", now.Add(time.Hour)); left != 2500 {
		t.Fatalf("renewed allowance: %d", left)
	}
}
