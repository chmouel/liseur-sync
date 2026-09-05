// Package live carries transient change notifications from a write
// that committed to the clients currently connected (ADR-0034).
//
// Nothing here is durable, and nothing here says what changed. A
// subscriber learns which topics to re-read and answers by reading the
// feeds it already knows. Losing every notification in this package
// costs latency and nothing else, which is why one process's memory is
// the right place for it.
package live

import (
	"errors"
	"sync"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ErrTooManyStreams is returned when an account already holds as many
// subscriptions as it is allowed. The caller answers 429.
var ErrTooManyStreams = errors.New("too many live streams for this account")

// ErrNoTopics is returned for a credential that could never receive
// anything. It is refused rather than parked on a silent stream.
var ErrNoTopics = errors.New("no live topics for this credential")

// topics is a set held as a bitmask so that coalescing is a union and
// draining allocates once. The canonical order below is what every
// subscriber sees, so two clients told the same thing read the same
// frame.
type topics uint8

var known = []store.Topic{
	store.TopicPositions,
	store.TopicAnnotations,
	store.TopicInsights,
}

func bit(t store.Topic) topics {
	for i, k := range known {
		if k == t {
			return 1 << i
		}
	}
	return 0
}

func setOf(list []store.Topic) topics {
	var s topics
	for _, t := range list {
		s |= bit(t)
	}
	return s
}

func (s topics) list() []store.Topic {
	if s == 0 {
		return nil
	}
	out := make([]store.Topic, 0, len(known))
	for i, k := range known {
		if s&(1<<i) != 0 {
			out = append(out, k)
		}
	}
	return out
}

// Hub fans committed changes out to the subscriptions of one account.
// It is safe for concurrent use and never blocks its caller: a write
// path notifies while a request is still open.
type Hub struct {
	maxPerUser int

	mu     sync.Mutex
	subs   map[string]map[*Subscription]struct{}
	closed bool
}

// NewHub returns a hub allowing maxPerUser concurrent subscriptions per
// account. A non-positive limit means one: a reader with several tabs
// is still a reader, but an unbounded count is a way to hold file
// descriptors.
func NewHub(maxPerUser int) *Hub {
	if maxPerUser < 1 {
		maxPerUser = 1
	}
	return &Hub{maxPerUser: maxPerUser, subs: map[string]map[*Subscription]struct{}{}}
}

// Subscribe registers a subscription for the topics this credential may
// receive. Register before reading anything the first invalidation
// provokes: a change committed between that read and the registration
// is the one a client would otherwise never hear about.
func (h *Hub) Subscribe(userID string, allowed []store.Topic) (*Subscription, error) {
	set := setOf(allowed)
	if userID == "" || set == 0 {
		return nil, ErrNoTopics
	}
	sub := &Subscription{
		hub:     h,
		userID:  userID,
		allowed: set,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		// Shutting down. Hand back a subscription that is already over
		// rather than a nil and a special case at the call site.
		sub.finish()
		return sub, nil
	}
	if len(h.subs[userID]) >= h.maxPerUser {
		return nil, ErrTooManyStreams
	}
	if h.subs[userID] == nil {
		h.subs[userID] = map[*Subscription]struct{}{}
	}
	h.subs[userID][sub] = struct{}{}
	return sub, nil
}

// Notify implements store.ChangeNotifier. It takes no socket and does
// no I/O: it marks each interested subscription and pokes it awake.
func (h *Hub) Notify(userID string, list ...store.Topic) {
	set := setOf(list)
	if set == 0 {
		return
	}
	h.mu.Lock()
	subs := make([]*Subscription, 0, len(h.subs[userID]))
	for sub := range h.subs[userID] {
		subs = append(subs, sub)
	}
	h.mu.Unlock()
	// Outside the lock: a subscription's own mutex is taken here, and a
	// slow client must never be able to stall a commit.
	for _, sub := range subs {
		sub.raise(set)
	}
}

// Close ends every subscription, for shutdown. Handlers see Done close
// and finish their responses.
func (h *Hub) Close() {
	h.mu.Lock()
	subs := h.subs
	h.subs = map[string]map[*Subscription]struct{}{}
	h.closed = true
	h.mu.Unlock()
	for _, byUser := range subs {
		for sub := range byUser {
			sub.finish()
		}
	}
}

// Subscription is one connected client. It holds the set of topics it
// owes and a single wake-up slot: notifications arriving during one
// slow write collapse into one frame naming everything they mentioned,
// which is as much as the client needs to know.
type Subscription struct {
	hub     *Hub
	userID  string
	allowed topics
	wake    chan struct{}

	mu      sync.Mutex
	pending topics

	doneOnce sync.Once
	done     chan struct{}
}

func (s *Subscription) raise(set topics) {
	set &= s.allowed
	if set == 0 {
		return
	}
	s.mu.Lock()
	before := s.pending
	s.pending |= set
	changed := s.pending != before
	s.mu.Unlock()
	if !changed {
		// Already owed, and the client has not read the last frame:
		// another one would say the same thing.
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Raise queues topics without going through the hub. The handler uses
// it for the invalidation every connection opens with.
func (s *Subscription) Raise(list ...store.Topic) { s.raise(setOf(list)) }

// Wake fires when something is owed.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Done fires when the hub shuts the subscription down.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Allowed lists the topics this subscription may ever receive, in
// canonical order.
func (s *Subscription) Allowed() []store.Topic { return s.allowed.list() }

// Take drains what is owed. An empty result is a spurious wake-up, not
// an empty frame: the caller sends nothing.
func (s *Subscription) Take() []store.Topic {
	s.mu.Lock()
	set := s.pending
	s.pending = 0
	s.mu.Unlock()
	return set.list()
}

// Close unregisters the subscription. Calling it twice is safe.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	if byUser, ok := h.subs[s.userID]; ok {
		delete(byUser, s)
		if len(byUser) == 0 {
			delete(h.subs, s.userID)
		}
	}
	h.mu.Unlock()
	s.finish()
}

func (s *Subscription) finish() { s.doneOnce.Do(func() { close(s.done) }) }
