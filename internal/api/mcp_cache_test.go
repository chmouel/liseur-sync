package api

import (
	"context"
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

func TestBookTextCacheCoalescesConcurrentLoads(t *testing.T) {
	cache := NewBookTextCache(2048)
	book := &booktext.Book{Chapters: []booktext.Chapter{{Text: "shared"}}}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	extract := func() (*booktext.Book, error) {
		calls.Add(1)
		close(started)
		<-release
		return book, nil
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.load(context.Background(), "digest", extract)
			if err != nil || got != book {
				t.Errorf("load: %p %v", got, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("extractions: %d", calls.Load())
	}
}

func TestBookTextCacheRetriesCanceledLoad(t *testing.T) {
	cache := NewBookTextCache(2048)
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.load(ctx, "digest", func() (*booktext.Book, error) {
			close(started)
			<-release
			return nil, ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	close(release)
	if err := <-done; err != context.Canceled {
		t.Fatalf("canceled load: %v", err)
	}
	book := &booktext.Book{}
	if got, err := cache.load(context.Background(), "digest", func() (*booktext.Book, error) { return book, nil }); err != nil || got != book {
		t.Fatalf("retry: %v %v", got, err)
	}
}

func TestBookTextScanRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := scanBookText(ctx, &booktext.Book{}, []string{"test"}, false, nil, 1)
	if err != context.Canceled {
		t.Fatalf("canceled scan: %v", err)
	}
}

func TestBookTextScanWaitHonorsCancellation(t *testing.T) {
	bookTextScan <- struct{}{}
	defer func() { <-bookTextScan }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := scanBookText(ctx, &booktext.Book{}, []string{"test"}, false, nil, 1)
	if err != context.Canceled {
		t.Fatalf("scan while busy: %v", err)
	}
}
