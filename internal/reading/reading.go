// Package reading resolves whether a book is finished, being read or
// not started, from the reader's explicit choice and the newest
// position behind it (ADR-0066). Liseur implements the same rule, so a
// book marked read on a phone is read in the browser too.
package reading

// FinishedProgression is where a position alone counts as finished. It
// is Liseur's number: no reader lands on the last byte, and a book read
// to 0.98 is a book that is over.
const FinishedProgression = 0.97

// Status is a resolved reading state, the one every surface renders.
type Status string

const (
	StatusFinished    Status = "finished"
	StatusReading     Status = "reading"
	StatusUnread      Status = "unread"
	StatusReadyToRead Status = "ready_to_read"
)

// UnreadShelf reports whether a status belongs on the shelf of books the
// reader is not in the middle of: the explicit choice and the derived
// no-position result share the shelf, and only the wire value differs.
func (s Status) UnreadShelf() bool {
	return s == StatusUnread || s == StatusReadyToRead
}

// Override is what the reader said about a book, carried by the newest
// status act. The empty value is "they never said anything".
type Override string

const (
	OverrideNone      Override = ""
	OverrideFinished  Override = "finished"
	OverrideUnread    Override = "unread"
	OverrideAutomatic Override = "automatic"
)

// Resolve applies the reader's choice first: an explicit act outranks
// the position, which cannot carry "I stopped at 0.4" or "put it back
// on the pile". A withdrawn choice (automatic) derives again.
//
// The explicit unread choice resolves to [StatusUnread], which shares a
// shelf with the derived [StatusReadyToRead] but is not the same fact:
// one is what the reader said, the other is what an absent position
// says, and a client that has the choice must be able to tell them
// apart.
func Resolve(choice Override, position *float64, partialCoverage bool) Status {
	switch choice {
	case OverrideFinished:
		return StatusFinished
	case OverrideUnread:
		return StatusUnread
	}
	return Derive(position, partialCoverage)
}

// Derive is what the position alone says. partialCoverage marks a
// position mapped from a recording that does not cover the whole book,
// which can never finish it.
func Derive(position *float64, partialCoverage bool) Status {
	switch {
	case position == nil || *position <= 0:
		return StatusReadyToRead
	case partialCoverage:
		return StatusReading
	case *position >= FinishedProgression:
		return StatusFinished
	default:
		return StatusReading
	}
}
