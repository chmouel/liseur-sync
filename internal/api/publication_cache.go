package api

import (
	"container/list"
	"sync"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// defaultPublicationIndexEntries bounds PublicationIndexCache. An index
// holds a manifest, a position list and per-entry ZIP coordinates — a few
// KiB for a small book, more for one with thousands of positions — so this
// is sized generously for one deployment rather than made configurable.
const defaultPublicationIndexEntries = 32

// PublicationIndexCache holds a parsed epub.Index per content digest, so a
// chapter, image, stylesheet or font request does not repeat preflightZIP,
// package parsing and manifest construction for every resource fetched out
// of the same publication. It is a cache and nothing else: a miss costs a
// full reparse and nothing more, and any entry may be dropped (Evict) the
// moment a resource request proves it no longer matches the archive.
type PublicationIndexCache struct {
	mu      sync.Mutex
	limit   int
	entries map[string]*list.Element
	order   *list.List // front = most recently used
}

type publicationIndexEntry struct {
	digest string
	index  *epub.Index
}

// NewPublicationIndexCache returns a cache bounded to the given number of
// entries, or defaultPublicationIndexEntries when limit <= 0.
func NewPublicationIndexCache(limit int) *PublicationIndexCache {
	if limit <= 0 {
		limit = defaultPublicationIndexEntries
	}
	return &PublicationIndexCache{limit: limit, entries: make(map[string]*list.Element), order: list.New()}
}

// Get returns the cached index for digest, if any, moving it to the front
// of the eviction order.
func (c *PublicationIndexCache) Get(digest string) (*epub.Index, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[digest]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*publicationIndexEntry).index, true
}

// Put caches idx under digest, evicting the least recently used entry if
// the cache is already at its limit.
func (c *PublicationIndexCache) Put(digest string, idx *epub.Index) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[digest]; ok {
		el.Value.(*publicationIndexEntry).index = idx
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&publicationIndexEntry{digest: digest, index: idx})
	c.entries[digest] = el
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*publicationIndexEntry).digest)
	}
}

// Evict drops digest from the cache. A resource request that finds the
// archive no longer matches the cached index calls this so the next
// request reparses instead of repeating the same stale answer.
func (c *PublicationIndexCache) Evict(digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[digest]
	if !ok {
		return
	}
	c.order.Remove(el)
	delete(c.entries, digest)
}
