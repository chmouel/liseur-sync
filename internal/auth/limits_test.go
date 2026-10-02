package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPasswordRequestsShareCapacityAndRecover(t *testing.T) {
	first := BeginPasswordRequest()
	if first == nil {
		t.Fatal("first request refused")
	}
	defer first()
	second := BeginPasswordRequest()
	if second == nil {
		t.Fatal("second request refused")
	}
	refused := BeginPasswordRequest()
	if refused != nil {
		refused()
		second()
		t.Fatal("third request admitted")
	}
	second()
	again := BeginPasswordRequest()
	if again == nil {
		t.Fatal("released slot not reusable")
	}
	again()
}

func TestRateLimiterCapacityAdmitsNewClients(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(2, time.Minute)
	for i := range maxRateLimitBuckets {
		if !rl.allowAt(fmt.Sprint(i), now) {
			t.Fatalf("refused key %d", i)
		}
	}
	if !rl.allowAt("0", now) {
		t.Fatal("existing budget changed")
	}
	// Refresh this exhausted client while other addresses churn the table.
	// Its limit must hold while newcomers can still attempt a login.
	for i := range 100 {
		if rl.allowAt("0", now) {
			t.Fatal("active client's budget reset")
		}
		if !rl.allowAt(fmt.Sprintf("new-%d", i), now) {
			t.Fatal("new client locked out by full table")
		}
	}
	if _, ok := rl.buckets["1"]; ok {
		t.Fatal("least recently used key was not evicted")
	}
	if len(rl.buckets) != maxRateLimitBuckets || rl.recent.Len() != maxRateLimitBuckets {
		t.Fatal("limiter storage exceeded capacity")
	}
	if !rl.allowAt("after-expiry", now.Add(time.Minute)) || len(rl.buckets) != 1 || rl.recent.Len() != 1 {
		t.Fatal("expired entries not reclaimed from both indexes")
	}
}

func TestRateLimiterSweepPreservesUnexpiredKeys(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(1, time.Minute)
	rl.allowAt("old", now)
	rl.allowAt("live", now.Add(30*time.Second))
	if rl.allowAt("live", now.Add(time.Minute)) {
		t.Fatal("sweep reset a live budget")
	}
	if _, ok := rl.buckets["old"]; ok {
		t.Fatal("expired key survived sweep")
	}
	if !rl.allowAt("live", now.Add(90*time.Second)) {
		t.Fatal("expired budget did not reset")
	}
}

func TestRateLimiterConcurrentBudget(t *testing.T) {
	rl := NewRateLimiter(10, time.Minute)
	now := time.Now()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if rl.allowAt("same", now) {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := accepted.Load(); got != 10 {
		t.Fatalf("accepted %d, want 10", got)
	}
}

func TestPasswordRequestsConcurrentAdmission(t *testing.T) {
	results := make(chan bool, 100)
	finish := make(chan struct{})
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(finish)
	for range 100 {
		wg.Go(func() {
			release := BeginPasswordRequest()
			results <- release != nil
			if release != nil {
				defer release()
				<-finish
			}
		})
	}
	accepted := 0
	for range 100 {
		select {
		case ok := <-results:
			if ok {
				accepted++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("admission queued requests instead of rejecting them")
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted %d concurrent requests, want 2", accepted)
	}
}
