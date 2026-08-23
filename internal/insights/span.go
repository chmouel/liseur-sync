package insights

import "time"

// Span is one of the fixed spans a reader can pick from a menu.
//
// The identifiers are the ones `StatsRange` uses in the Android app, so
// the two menus offer the same choices under the same names and a span
// can be carried between them without translation. They are also what
// gets written into a URL and a cookie, so they must not change once
// released.
//
// This sits on top of [Window] rather than replacing it: the API's
// `range=Nd` accepts any number of days, and only a menu needs a fixed
// list.
type Span string

const (
	Span7Days    Span = "7d"
	Span30Days   Span = "30d"
	Span90Days   Span = "90d"
	Span365Days  Span = "365d"
	SpanThisYear Span = "this_year"
	SpanAllTime  Span = "all"
)

// DefaultSpan is what a reader who has never chosen gets, and what an
// unreadable choice falls back to. Thirty days is long enough to show a
// habit and short enough that every day in it is still a day the reader
// remembers.
const DefaultSpan = Span30Days

// MaxBarDays is as many days as read as a row of bars. Past this a bar
// per day is a picket fence nobody can read a date off, and the
// calendar grid takes over.
const MaxBarDays = 31

// Spans is the menu, in the order it is offered.
var Spans = []Span{Span7Days, Span30Days, Span90Days, Span365Days, SpanThisYear, SpanAllTime}

// ParseSpan reads a span identifier, falling back to [DefaultSpan] for
// anything it does not recognise. A query parameter and a cookie are
// both user input: an unknown value is a value somebody typed, not a
// value to render.
func ParseSpan(raw string) Span {
	for _, s := range Spans {
		if string(s) == raw {
			return s
		}
	}
	return DefaultSpan
}

// Label is how the span is named in a menu.
func (s Span) Label() string {
	switch s {
	case Span7Days:
		return "Last 7 days"
	case Span90Days:
		return "Last 90 days"
	case Span365Days:
		return "Last 365 days"
	case SpanThisYear:
		return "This year"
	case SpanAllTime:
		return "All time"
	case Span30Days:
		return "Last 30 days"
	default:
		return "Last 30 days"
	}
}

// Window resolves the span against a moment and a timezone.
//
// "This year" is resolved against now rather than a day count, because
// the first of January is a different distance away every time it is
// asked and a fixed 365 would call last December part of this year for
// most of the spring.
func (s Span) Window(now time.Time, loc *time.Location) Window {
	today := now.In(loc)
	switch s {
	case Span7Days:
		return DayWindow(today.AddDate(0, 0, -6), today, loc)
	case Span90Days:
		return DayWindow(today.AddDate(0, 0, -89), today, loc)
	case Span365Days:
		return DayWindow(today.AddDate(0, 0, -364), today, loc)
	case SpanThisYear:
		return DayWindow(time.Date(today.Year(), 1, 1, 0, 0, 0, 0, loc), today, loc)
	case SpanAllTime:
		return Window{}
	case Span30Days:
		return DayWindow(today.AddDate(0, 0, -29), today, loc)
	default:
		return DayWindow(today.AddDate(0, 0, -29), today, loc)
	}
}

// SuitsDailyBars reports whether the span is short enough to read as a
// row of one bar per day.
func (s Span) SuitsDailyBars(now time.Time, loc *time.Location) bool {
	w := s.Window(now, loc)
	if w.Unbounded() {
		return false
	}
	return w.Days() <= MaxBarDays
}
