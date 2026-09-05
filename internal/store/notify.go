package store

import "sync/atomic"

// Topic names a family of reading state a client already knows how to
// read for itself (ADR-0034). It is the entire content of a live
// notification: no work, no locator, no sequence number, nothing a
// client could mistake for the change itself.
type Topic string

const (
	// TopicPositions covers everything on the op log: native pushes,
	// the web reader's reading status, and the kosync adapter.
	TopicPositions Topic = "positions"
	// TopicAnnotations covers the annotation feed, tombstones included.
	TopicAnnotations Topic = "annotations"
	// TopicInsights covers reading sessions, which have no feed of
	// their own: a client answers by asking for the statistics again.
	TopicInsights Topic = "insights"
)

// ChangeNotifier is told, after a commit, that a user's reading state
// moved. It must not block: a write path calls it while a request is
// still in flight.
type ChangeNotifier interface {
	Notify(userID string, topics ...Topic)
}

// ChangeNotifying is the optional interface a backend implements to
// accept one. The Store interface deliberately does not carry it:
// nothing reads state through it, and a wrapper that hid it would be a
// bug rather than a layer.
type ChangeNotifying interface {
	SetChangeNotifier(ChangeNotifier)
}

// Notifications is embedded by a backend to carry the hook. The zero
// value notifies nobody, which is what every test and the admin CLI
// want.
type Notifications struct {
	notifier atomic.Pointer[ChangeNotifier]
}

// SetChangeNotifier installs the hook. Passing nil removes it.
func (n *Notifications) SetChangeNotifier(cn ChangeNotifier) {
	if cn == nil {
		n.notifier.Store(nil)
		return
	}
	n.notifier.Store(&cn)
}

// Notify raises the topics for one user. Callers must only reach here
// after a successful commit, and only when the commit changed
// something a client could read: a duplicate that changed no row is
// exactly the write that must not start another round of pulls.
func (n *Notifications) Notify(userID string, topics ...Topic) {
	if userID == "" || len(topics) == 0 {
		return
	}
	cn := n.notifier.Load()
	if cn == nil {
		return
	}
	(*cn).Notify(userID, topics...)
}

// AnyOpApplied reports whether a batch of op results contains a write
// that landed. Duplicates and conflicts changed no row.
func AnyOpApplied(results []OpResult) bool {
	for _, r := range results {
		if r.Status == "applied" {
			return true
		}
	}
	return false
}

// AnyAnnotationApplied is the same question for annotation results,
// which share the vocabulary but not the type.
func AnyAnnotationApplied(results []AnnotationResult) bool {
	for _, r := range results {
		if r.Status == "applied" {
			return true
		}
	}
	return false
}
