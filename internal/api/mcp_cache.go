package api

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
	"unsafe"

	"github.com/chmouel/liseur-sync/internal/booktext"
)

// defaultBookTextBytes bounds the extracted text kept in memory. A novel
// is around a megabyte of text, so this holds a few dozen books.
const defaultBookTextBytes = 64 << 20

// BookTextCache keeps extracted book text per content digest, evicting
// the least recently used book once the byte budget is spent. A digest
// names exact bytes, so an entry never goes stale; a changed file is a
// new digest.
type BookTextCache struct {
	mu      sync.Mutex
	limit   int
	bytes   int
	entries map[string]*list.Element
	loading map[string]*bookTextLoad
	order   *list.List
}

type bookTextLoad struct {
	done chan struct{}
	book *booktext.Book
	err  error
}

type bookTextEntry struct {
	digest string
	book   *booktext.Book
	weight int
}

// NewBookTextCache returns a cache bounded to limit bytes of text, or
// defaultBookTextBytes when limit <= 0.
func NewBookTextCache(limit int) *BookTextCache {
	if limit <= 0 {
		limit = defaultBookTextBytes
	}
	return &BookTextCache{limit: limit, entries: map[string]*list.Element{}, loading: map[string]*bookTextLoad{}, order: list.New()}
}

func (c *BookTextCache) get(digest string) (*booktext.Book, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[digest]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*bookTextEntry).book, true
}

func (c *BookTextCache) load(ctx context.Context, digest string, extract func() (*booktext.Book, error)) (*booktext.Book, error) {
	for {
		c.mu.Lock()
		if el, ok := c.entries[digest]; ok {
			c.order.MoveToFront(el)
			book := el.Value.(*bookTextEntry).book
			c.mu.Unlock()
			return book, nil
		}
		if pending, ok := c.loading[digest]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending.done:
				if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
					continue
				}
				return pending.book, pending.err
			}
		}
		pending := &bookTextLoad{done: make(chan struct{}), err: errors.New("book text extraction failed")}
		c.loading[digest] = pending
		c.mu.Unlock()
		return c.finishLoad(digest, pending, extract)
	}
}

func (c *BookTextCache) finishLoad(digest string, pending *bookTextLoad, extract func() (*booktext.Book, error)) (*booktext.Book, error) {
	defer func() {
		c.mu.Lock()
		delete(c.loading, digest)
		close(pending.done)
		c.mu.Unlock()
	}()
	pending.book, pending.err = extract()
	if pending.err == nil {
		c.put(digest, pending.book)
	}
	return pending.book, pending.err
}

func (c *BookTextCache) put(digest string, b *booktext.Book) {
	weight := int(unsafe.Sizeof(*b)) + cap(b.Chapters)*int(unsafe.Sizeof(booktext.Chapter{}))
	for _, chapter := range b.Chapters {
		weight += len(chapter.Href) + len(chapter.Title) + len(chapter.Text)
	}
	if weight > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[digest]; ok {
		return
	}
	c.entries[digest] = c.order.PushFront(&bookTextEntry{digest: digest, book: b, weight: weight})
	c.bytes += weight
	for c.bytes > c.limit {
		el := c.order.Back()
		e := el.Value.(*bookTextEntry)
		c.order.Remove(el)
		delete(c.entries, e.digest)
		c.bytes -= e.weight
	}
}

// textBudget counts the book text each grant has read in the current
// hour. It lives in memory: a restart forgives an hour, which is a
// smaller cost than a database write per tool call.
type textBudget struct {
	mu      sync.Mutex
	perHour int64
	windows map[string]*budgetWindow
}

type budgetWindow struct {
	start time.Time
	used  int64
}

func newTextBudget(perHour int64) *textBudget {
	return &textBudget{perHour: perHour, windows: map[string]*budgetWindow{}}
}

// remaining reports how much text key may still read this hour and when
// its window resets.
func (b *textBudget) remaining(key string, now time.Time) (int64, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.window(key, now)
	return max(b.perHour-w.used, 0), w.start.Add(time.Hour)
}

func (b *textBudget) charge(key string, n int, now time.Time) (bool, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.window(key, now)
	reset := w.start.Add(time.Hour)
	if int64(n) > b.perHour-w.used {
		return false, reset
	}
	w.used += int64(n)
	return true, reset
}

func (b *textBudget) window(key string, now time.Time) *budgetWindow {
	w, ok := b.windows[key]
	if !ok || now.Sub(w.start) >= time.Hour {
		if len(b.windows) > 4096 {
			for k, old := range b.windows {
				if now.Sub(old.start) >= time.Hour {
					delete(b.windows, k)
				}
			}
		}
		w = &budgetWindow{start: now}
		b.windows[key] = w
	}
	return w
}
