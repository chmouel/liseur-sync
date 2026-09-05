// Package insights holds the span vocabulary the reading statistics are
// answered in.
//
// It exists because there are three surfaces — the native API, the web
// UI and the Android app — that must agree on what "the last thirty
// days" means, and two of them run in this binary. When they each had
// their own copy they drifted: the API placed a sitting on the day it
// ended and the web dashboard on the day it began, so a session read
// across midnight appeared on different days depending on which screen
// the reader was looking at.
//
// Nothing here knows about HTTP. A caller hands over the strings it
// received and gets back a [Window] it can query the store with.
package insights

import (
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// DayFormat is how a tz-local day is written down everywhere in this
// package and in the store: rollups are keyed by it and windows are
// compared as strings against it.
const DayFormat = "2006-01-02"

// MaxRangeDays is as long a span as `range=Nd` may name: ten years,
// which is longer than this software has existed. It is not a limit on
// how far back an insight can reach — an unbounded window is unbounded
// and always was, and rollups are kept indefinitely.
const MaxRangeDays = 3660

// StreakLookbackDays is how far back a streak is searched for. A run of
// consecutive days is broken by the first day without reading on it, so
// looking further back than any reader has been syncing cannot lengthen
// one, and the query is bounded rather than open.
const StreakLookbackDays = MaxRangeDays

// DefaultSummaryRange is what the summary counts when a caller names no
// span at all, kept from before ranges were selectable.
const DefaultSummaryRange = "30d"

// UnboundedRange is how a caller spells "everything on record" rather
// than encoding it as a magic number of days nobody could read back.
const UnboundedRange = "all"

// EpochDay is older than any session or rollup a store can hold, and is
// what an unbounded window uses where a query insists on a lower bound.
const EpochDay = "1970-01-01"

// Window is the span an insight covers, always a whole number of days
// in the user's timezone.
//
// Whole days rather than a rolling count of hours, because that is what
// the question means: a reader asking for their last seven days at nine
// in the evening means the same seven dates they would have meant at
// dawn, not the 168 hours ending now — which would reach back into an
// eighth day and count part of it.
//
// A zero Window is unbounded: "everything on record", which is what a
// caller that names no range has always meant and must keep meaning.
type Window struct {
	// from is the first instant counted, to the first instant after.
	from time.Time
	to   time.Time
	// fromDay and toDay are the same bounds as tz-local dates, both
	// inclusive, and are empty exactly when the window is unbounded.
	fromDay string
	toDay   string
}

// Unbounded reports whether this window has no beginning.
func (w Window) Unbounded() bool { return w.fromDay == "" }

// FromDay is the first tz-local day counted, empty when unbounded.
func (w Window) FromDay() string { return w.fromDay }

// ToDay is the last tz-local day counted, empty when unbounded.
func (w Window) ToDay() string { return w.toDay }

// HoldsSession reports whether a session ended inside the window. The
// end is what places a session in a range, so that a stretch of reading
// counts once, on the day it was finished, rather than being smeared
// across a boundary it happened to straddle.
func (w Window) HoldsSession(ses store.Session) bool {
	if w.Unbounded() {
		return true
	}
	return !ses.EndedAt.Before(w.from) && ses.EndedAt.Before(w.to)
}

// HoldsDay reports whether a rollup's tz-local day falls in the window.
// Compared as strings because a rollup's day was fixed in the timezone
// in force when it was rolled up, and re-parsing it in today's timezone
// would move reading between days retroactively.
func (w Window) HoldsDay(day string) bool {
	if w.Unbounded() {
		return true
	}
	return day >= w.fromDay && day <= w.toDay
}

// Days counts the calendar days the window covers, 0 when unbounded.
//
// Counted from the day strings in UTC rather than by dividing the
// instants, because a span containing a daylight-saving change is not a
// whole number of twenty-four hour periods and would come out short.
func (w Window) Days() int {
	if w.Unbounded() {
		return 0
	}
	from, errFrom := time.Parse(DayFormat, w.fromDay)
	to, errTo := time.Parse(DayFormat, w.toDay)
	if errFrom != nil || errTo != nil {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}

// SessionBounds are the instants to query raw sessions between. An
// unbounded window reaches back to the epoch, which predates every
// session any store can hold, and forward to the end of today in the
// user's timezone — the same far end a bounded window ending today has,
// so that a lifetime total can never come out below a ranged one when a
// device with a fast clock files a session a moment into the future.
func (w Window) SessionBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	if w.Unbounded() {
		today := now.In(loc)
		endOfToday := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
		return time.Unix(0, 0), endOfToday
	}
	return w.from, w.to
}

// DayBounds are the inclusive tz-local dates to query rollups between.
func (w Window) DayBounds(now time.Time, loc *time.Location) (string, string) {
	if w.Unbounded() {
		return EpochDay, now.In(loc).Format(DayFormat)
	}
	return w.fromDay, w.toDay
}

// EachDay calls fn with every tz-local day in the window, in order,
// including the ones with nothing on them.
//
// Empty days are kept deliberately: a fortnight with two gaps in it is
// the information, and a series that silently skipped them would read
// as an unbroken run. An unbounded window has no first day of its own,
// so the caller supplies one — the earliest day it found reading on,
// because a chart of every day since the epoch is mostly a picture of
// before the reader owned the software.
func (w Window) EachDay(earliest string, now time.Time, loc *time.Location, fn func(day string)) {
	first, last := w.DayBounds(now, loc)
	if w.Unbounded() {
		if earliest == "" {
			return
		}
		first = earliest
	}
	from, err := time.Parse(DayFormat, first)
	if err != nil {
		return
	}
	to, err := time.Parse(DayFormat, last)
	if err != nil || to.Before(from) {
		return
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		fn(d.Format(DayFormat))
	}
}

// DayWindow builds the window covering firstDay through lastDay
// inclusive, in loc.
func DayWindow(firstDay, lastDay time.Time, loc *time.Location) Window {
	from := time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, loc)
	last := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 0, 0, 0, 0, loc)
	return Window{
		from:    from,
		to:      last.AddDate(0, 0, 1),
		fromDay: from.Format(DayFormat),
		toDay:   last.Format(DayFormat),
	}
}

// ParseWindow resolves the span a request asks for.
//
// rawFrom and rawTo name whole days in the user's own timezone, both
// included, and win when they are both present and readable. rawRange
// is the older spelling: `Nd` means the last N calendar days ending
// today — calendar days, so that a request made at nine in the evening
// covers the same dates as one made at dawn, which is what a reader
// means and what their own device counts locally. [UnboundedRange]
// means everything on record, as does an absent parameter where the
// caller passes no def.
//
// A range that cannot be read falls back to def rather than to
// everything: a typo asking for a fortnight should not quietly become
// the most expensive query there is, and an echo would report a span
// the caller never asked for either way.
func ParseWindow(rawFrom, rawTo, rawRange, def string, loc *time.Location, now time.Time) Window {
	if rawFrom != "" && rawTo != "" {
		from, errFrom := time.ParseInLocation(DayFormat, rawFrom, loc)
		to, errTo := time.ParseInLocation(DayFormat, rawTo, loc)
		if errFrom == nil && errTo == nil && !to.Before(from) {
			return DayWindow(from, to, loc)
		}
	}
	raw := rawRange
	if raw == "" {
		raw = def
	}
	if raw == "" || raw == UnboundedRange {
		return Window{}
	}
	days := parseRangeDays(raw, parseRangeDays(def, 0))
	if days <= 0 {
		return Window{}
	}
	today := now.In(loc)
	return DayWindow(today.AddDate(0, 0, -(days-1)), today, loc)
}

// parseRangeDays reads a `Nd` parameter, returning def for anything
// else — including [UnboundedRange], which names no number of days and
// is resolved to an unbounded window by [ParseWindow] instead.
func parseRangeDays(s string, def int) int {
	if len(s) >= 2 && s[len(s)-1] == 'd' {
		var v int
		if _, err := parseInt(s[:len(s)-1], &v); err == nil && v > 0 && v <= MaxRangeDays {
			return v
		}
	}
	return def
}

func parseInt(s string, out *int) (int, error) {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		v = v*10 + int(c-'0')
	}
	*out = v
	return v, nil
}

var errBadInt = errString("bad int")

type errString string

func (e errString) Error() string { return string(e) }

// ActiveSeconds returns a session's explicit active time when the
// device supplied it, otherwise the wall-clock length minus idle time,
// clamped at nought. Explicit active time is already foreground reading
// time, so idle is not subtracted from it again and it is not capped to
// the wall span.
func ActiveSeconds(ses store.Session) float64 {
	if ses.ActiveMs != nil {
		if *ses.ActiveMs < 0 {
			return 0
		}
		return float64(*ses.ActiveMs) / 1000
	}
	d := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	if d < 0 {
		return 0
	}
	return d
}

// StreakDays counts consecutive days with activity, ending today or
// yesterday.
//
// Yesterday counts because a streak is about a habit and the day is not
// over: a reader who has not opened a book before lunch has not broken
// anything, and a screen that said so every morning would be lying by
// eleven o'clock.
func StreakDays(daySet map[string]bool, loc *time.Location, now time.Time) int {
	streak := 0
	day := now.In(loc)
	if !daySet[day.Format(DayFormat)] {
		day = day.AddDate(0, 0, -1)
		if !daySet[day.Format(DayFormat)] {
			return 0
		}
	}
	for daySet[day.Format(DayFormat)] {
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}
