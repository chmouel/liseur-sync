package api

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/booktext"
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

func TestBookTextCacheCountsChapterMetadata(t *testing.T) {
	cache := NewBookTextCache(2048)
	book := &booktext.Book{Chapters: make([]booktext.Chapter, 12)}
	for i := range book.Chapters {
		book.Chapters[i] = booktext.Chapter{Href: strings.Repeat("h", 40), Title: strings.Repeat("t", 40)}
	}
	cache.put("first", book)
	if _, ok := cache.get("first"); !ok {
		t.Fatal("first book not cached")
	}
	cache.put("second", book)
	if _, ok := cache.get("first"); ok {
		t.Fatal("metadata-heavy book was not evicted")
	}
	if _, ok := cache.get("second"); !ok {
		t.Fatal("second book not cached")
	}
}
