package insights

import "time"

// Span is one of the fixed spans a reader can pick from a menu.
//
// Some of the identifiers are shared with `StatsRange` in the Android
// app, but the two menus are not the same menu and never have been.
// `this_month`, `this_year` and `all` mean the same thing on both
// sides. `30d`, `90d` and `365d` are only offered here. And `7d` is the
// trap: it exists in both and does not agree — a rolling seven days
// here, the calendar week so far there. A span carried between the two
// products can be named, not assumed.
//
// These are written into a URL and a cookie, so they must not change
// once released.
//
// This sits on top of [Window] rather than replacing it: the API's
// `range=Nd` accepts any number of days, and only a menu needs a fixed
// list.
type Span string

const (
	Span7Days     Span = "7d"
	Span30Days    Span = "30d"
	SpanThisMonth Span = "this_month"
	Span90Days    Span = "90d"
	Span365Days   Span = "365d"
	SpanThisYear  Span = "this_year"
	SpanAllTime   Span = "all"
)

// DefaultSpan is what a reader who has never chosen gets, and what an
// unreadable choice falls back to. Thirty days is long enough to show a
// habit and short enough that every day in it is still a day the reader
// remembers.
const DefaultSpan = Span30Days

// MaxBarDays is as many days as read as a row of one bar a day. Past
// this a bar per day is a picket fence nobody can read a date off, and
// the bars are grouped into wider buckets instead.
const MaxBarDays = 31

// Spans is the menu, in the order it is offered.
var Spans = []Span{
	Span7Days, Span30Days, SpanThisMonth, Span90Days, Span365Days, SpanThisYear, SpanAllTime,
}

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
	case SpanThisMonth:
		return "This month"
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
// "This month" and "this year" are resolved against now rather than a
// day count, because the first of the month and the first of January
// are a different distance away every time they are asked, and a fixed
// 365 would call last December part of this year for most of the
// spring.
func (s Span) Window(now time.Time, loc *time.Location) Window {
	today := now.In(loc)
	switch s {
	case Span7Days:
		return DayWindow(today.AddDate(0, 0, -6), today, loc)
	case Span90Days:
		return DayWindow(today.AddDate(0, 0, -89), today, loc)
	case Span365Days:
		return DayWindow(today.AddDate(0, 0, -364), today, loc)
	case SpanThisMonth:
		return DayWindow(time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc), today, loc)
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

// Bucket is the calendar unit one bar of the activity chart covers.
type Bucket string

const (
	BucketDay   Bucket = "day"
	BucketWeek  Bucket = "week"
	BucketMonth Bucket = "month"
	BucketYear  Bucket = "year"
)

// Bucket is how wide a bar is drawn for this span.
//
// A bar is only worth a day while the days still fit: thirty-one is
// where a row of them stops being a chart and starts being a fence.
// Past that the days are grouped, so the chart keeps roughly the same
// number of bars whatever is being asked about, and each one stays wide
// enough to carry a label.
//
// The three rolling spans have no counterpart in the Android app, whose
// menu is calendar-aligned throughout. They are placed here by how many
// bars they would otherwise produce, which is the same reasoning that
// put the app's spans where they are.
func (s Span) Bucket() Bucket {
	switch s {
	case Span7Days, Span30Days, SpanThisMonth:
		return BucketDay
	case Span90Days:
		return BucketWeek
	case Span365Days, SpanThisYear:
		return BucketMonth
	case SpanAllTime:
		return BucketYear
	default:
		return BucketDay
	}
}

// Heading names the activity chart for the bucket it is drawing. The
// wording is the Android app's, so a reader who has both in front of
// them is looking at two of the same thing.
func (b Bucket) Heading() string {
	switch b {
	case BucketWeek:
		return "Week by week"
	case BucketMonth:
		return "Month by month"
	case BucketYear:
		return "Year by year"
	default:
		return "Day by day"
	}
}

// SuitsCalendar reports whether a span is long enough that a calendar
// grid of it says something the bars do not. Below a month the grid is
// a handful of columns, and the shape a heatmap exists to show — the
// fortnight away, the month nothing happened — is not there to show.
func (s Span) SuitsCalendar(now time.Time, loc *time.Location) bool {
	w := s.Window(now, loc)
	return w.Unbounded() || w.Days() > MaxBarDays
}
