package live

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func waited(t *testing.T, sub *Subscription) []store.Topic {
	t.Helper()
	select {
	case <-sub.Wake():
		return sub.Take()
	case <-time.After(2 * time.Second):
		t.Fatal("no wake-up")
		return nil
	}
}

func quiet(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case <-sub.Wake():
		t.Fatalf("unexpected wake-up carrying %v", sub.Take())
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNotifyReachesOnlyItsOwnAccount(t *testing.T) {
	h := NewHub(4)
	mine, err := h.Subscribe("u1", []store.Topic{store.TopicPositions})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := h.Subscribe("u2", []store.Topic{store.TopicPositions})
	if err != nil {
		t.Fatal(err)
	}
	h.Notify("u1", store.TopicPositions)
	if got := waited(t, mine); len(got) != 1 || got[0] != store.TopicPositions {
		t.Fatalf("own account got %v", got)
	}
	quiet(t, theirs)
}

func TestTopicsCoalesceByUnion(t *testing.T) {
	h := NewHub(4)
	sub, err := h.Subscribe("u1", known)
	if err != nil {
		t.Fatal(err)
	}
	// A burst while nobody is reading must not lose the earlier topics
	// by being replaced with the later one.
	h.Notify("u1", store.TopicPositions)
	h.Notify("u1", store.TopicAnnotations)
	h.Notify("u1", store.TopicInsights)
	got := waited(t, sub)
	if len(got) != 3 || got[0] != store.TopicPositions ||
		got[1] != store.TopicAnnotations || got[2] != store.TopicInsights {
		t.Fatalf("coalesced to %v", got)
	}
	// Drained: nothing further is owed.
	if rest := sub.Take(); rest != nil {
		t.Fatalf("still owed %v", rest)
	}
}

func TestUnpermittedTopicIsNeverDelivered(t *testing.T) {
	h := NewHub(4)
	sub, err := h.Subscribe("u1", []store.Topic{store.TopicPositions})
	if err != nil {
		t.Fatal(err)
	}
	h.Notify("u1", store.TopicInsights)
	quiet(t, sub)

	h.Notify("u1", store.TopicInsights, store.TopicPositions)
	if got := waited(t, sub); len(got) != 1 || got[0] != store.TopicPositions {
		t.Fatalf("filtered set was %v", got)
	}
}

func TestSubscribeRefusals(t *testing.T) {
	h := NewHub(1)
	if _, err := h.Subscribe("u1", nil); err != ErrNoTopics {
		t.Fatalf("no topics: %v", err)
	}
	if _, err := h.Subscribe("", known); err != ErrNoTopics {
		t.Fatalf("no user: %v", err)
	}
	first, err := h.Subscribe("u1", known)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Subscribe("u1", known); err != ErrTooManyStreams {
		t.Fatalf("over the limit: %v", err)
	}
	// Closing one makes room again, so a reconnecting client is not
	// locked out by its own previous stream.
	first.Close()
	if _, err := h.Subscribe("u1", known); err != nil {
		t.Fatalf("after close: %v", err)
	}
}

func TestCloseEndsEverySubscription(t *testing.T) {
	h := NewHub(4)
	sub, err := h.Subscribe("u1", known)
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	select {
	case <-sub.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not end the subscription")
	}
	// Subscribing during shutdown yields something already over rather
	// than a nil to special-case.
	late, err := h.Subscribe("u1", known)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-late.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("late subscription was not already over")
	}
	sub.Close() // idempotent
}

func TestNotifyDoesNotBlockOnAnIdleSubscriber(t *testing.T) {
	h := NewHub(4)
	if _, err := h.Subscribe("u1", known); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			h.Notify("u1", store.TopicPositions, store.TopicAnnotations)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a client that never reads stalled the write path")
	}
}

func TestConcurrentNotifyAndSubscribe(t *testing.T) {
	h := NewHub(64)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				h.Notify("u1", store.TopicPositions)
			}
		}
	}()
	for range 50 {
		sub, err := h.Subscribe("u1", known)
		if err != nil {
			t.Fatal(err)
		}
		sub.Take()
		sub.Close()
	}
	close(stop)
}
