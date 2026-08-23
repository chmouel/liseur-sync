package insights

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

var paris = mustLoad("Europe/Paris")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// A span is a number of whole days in the reader's own timezone, not a
// number of hours ending at the moment of the request. Asked at nine in
// the evening it must name the same dates it would have named at dawn.
func TestRangeIsWholeLocalDays(t *testing.T) {
	evening := time.Date(2026, 8, 9, 21, 30, 0, 0, paris)
	win := ParseWindow("", "", "7d", "", paris, evening)

	if got, want := win.FromDay(), "2026-08-03"; got != want {
		t.Errorf("first day: got %q, want %q", got, want)
	}
	if got, want := win.ToDay(), "2026-08-09"; got != want {
		t.Errorf("last day: got %q, want %q", got, want)
	}
	if got := win.Days(); got != 7 {
		t.Errorf("seven days is seven days, got %d", got)
	}

	dawn := time.Date(2026, 8, 9, 5, 0, 0, 0, paris)
	if atDawn := ParseWindow("", "", "7d", "", paris, dawn); atDawn.FromDay() != win.FromDay() {
		t.Errorf("the same day names a different span at dawn: %q vs %q",
			atDawn.FromDay(), win.FromDay())
	}
}

func TestExplicitDaysWinOverARange(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	win := ParseWindow("2026-01-01", "2026-03-31", "7d", DefaultSummaryRange, paris, now)
	if win.FromDay() != "2026-01-01" || win.ToDay() != "2026-03-31" {
		t.Fatalf("from/to ignored: %q..%q", win.FromDay(), win.ToDay())
	}
	if got := win.Days(); got != 90 {
		t.Errorf("January to March 2026 is 90 days, got %d", got)
	}
}

// A malformed span is not a licence to answer about everything: a typo
// asking for a fortnight must land on the caller's default, which is
// the cheapest correct answer, rather than on the whole history.
func TestUnreadableRangeFallsBackToTheDefault(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	for _, raw := range []string{"wat", "30", "0d", "-3d", "99999d"} {
		win := ParseWindow("", "", raw, DefaultSummaryRange, paris, now)
		if got := win.Days(); got != 30 {
			t.Errorf("range=%q: want the 30d default, got %d days", raw, got)
		}
	}
}

// A caller with no default of its own still means "everything", which
// is what the works endpoints answered before spans existed.
func TestUnreadableRangeWithNoDefaultIsUnbounded(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	for _, raw := range []string{"", UnboundedRange, "wat"} {
		if win := ParseWindow("", "", raw, "", paris, now); !win.Unbounded() {
			t.Errorf("range=%q with no default: want unbounded, got %d days", raw, win.Days())
		}
	}
}

func TestAllTimeIsUnboundedEvenWithADefault(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	if win := ParseWindow("", "", UnboundedRange, DefaultSummaryRange, paris, now); !win.Unbounded() {
		t.Fatalf("range=all: want unbounded, got %d days", win.Days())
	}
}

// A session is placed by its end, so a sitting that began before the
// span and finished inside it belongs to the span. This is the rule the
// web UI used to get wrong in the other direction.
func TestASessionIsPlacedByItsEnd(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	win := ParseWindow("", "", "7d", "", paris, now)

	across := store.Session{
		StartedAt: time.Date(2026, 8, 2, 23, 40, 0, 0, paris),
		EndedAt:   time.Date(2026, 8, 3, 0, 20, 0, 0, paris),
	}
	if !win.HoldsSession(across) {
		t.Error("a sitting finishing on the first day of the span is inside it")
	}

	before := store.Session{
		StartedAt: time.Date(2026, 8, 2, 20, 0, 0, 0, paris),
		EndedAt:   time.Date(2026, 8, 2, 23, 0, 0, 0, paris),
	}
	if win.HoldsSession(before) {
		t.Error("a sitting finishing the day before the span is outside it")
	}
}

// The far end of an unbounded window is the end of today, exactly as it
// is for a bounded window ending today. A device with a fast clock can
// file a session a moment into the future, and the two disagreeing
// about it would put a lifetime total below a ranged one.
func TestUnboundedReachesTheSameFarEndAsToday(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	future := store.Session{
		StartedAt: now.Add(30 * time.Minute),
		EndedAt:   now.Add(90 * time.Minute),
	}

	ranged := ParseWindow("", "", "30d", "", paris, now)
	if !ranged.HoldsSession(future) {
		t.Fatal("precondition: a bounded window ending today admits this session")
	}

	_, lifetimeEnd := Window{}.SessionBounds(now, paris)
	_, rangedEnd := ranged.SessionBounds(now, paris)
	if !lifetimeEnd.Equal(rangedEnd) {
		t.Errorf("far ends differ: lifetime %v, ranged %v", lifetimeEnd, rangedEnd)
	}
}

func TestDaysAcrossADaylightSavingChange(t *testing.T) {
	// The clocks go forward in Paris on 29 March 2026, so this span is
	// not a whole number of twenty-four hour periods.
	win := DayWindow(
		time.Date(2026, 3, 28, 0, 0, 0, 0, paris),
		time.Date(2026, 3, 30, 0, 0, 0, 0, paris),
		paris,
	)
	if got := win.Days(); got != 3 {
		t.Errorf("three dates is three days, got %d", got)
	}
}

func TestEachDayIncludesTheEmptyOnes(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	win := ParseWindow("", "", "7d", "", paris, now)
	var days []string
	win.EachDay("", now, paris, func(day string) { days = append(days, day) })

	if len(days) != 7 {
		t.Fatalf("want 7 days, got %d: %v", len(days), days)
	}
	if days[0] != "2026-08-03" || days[6] != "2026-08-09" {
		t.Errorf("wrong ends: %q..%q", days[0], days[6])
	}
}

// An unbounded span has no first day of its own. Drawing every day
// since the epoch would be mostly a picture of before the reader owned
// the software, so the caller says where its own history starts.
func TestEachDayOnAnUnboundedSpanStartsAtTheEarliestReading(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	var days []string
	Window{}.EachDay("2026-08-07", now, paris, func(day string) { days = append(days, day) })

	want := []string{"2026-08-07", "2026-08-08", "2026-08-09"}
	if len(days) != len(want) {
		t.Fatalf("want %v, got %v", want, days)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Fatalf("want %v, got %v", want, days)
		}
	}

	var none []string
	Window{}.EachDay("", now, paris, func(day string) { none = append(none, day) })
	if len(none) != 0 {
		t.Errorf("no reading on record draws no days, got %d", len(none))
	}
}

// This year is resolved against today, not as a fixed count of days:
// the first of January is a different distance away every time it is
// asked, and 365 would call last December part of this year for most of
// the spring.
func TestThisYearOpensOnTheFirstOfJanuary(t *testing.T) {
	spring := time.Date(2026, 3, 4, 12, 0, 0, 0, paris)
	win := SpanThisYear.Window(spring, paris)
	if got, want := win.FromDay(), "2026-01-01"; got != want {
		t.Errorf("first day: got %q, want %q", got, want)
	}
	if got, want := win.ToDay(), "2026-03-04"; got != want {
		t.Errorf("last day: got %q, want %q", got, want)
	}

	newYear := time.Date(2026, 1, 1, 9, 0, 0, 0, paris)
	if got := SpanThisYear.Window(newYear, paris).Days(); got != 1 {
		t.Errorf("on new year's day this year is one day, got %d", got)
	}
}

func TestSpanWindowsAreTheDaysTheyName(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	for span, want := range map[Span]int{
		Span7Days:   7,
		Span30Days:  30,
		Span90Days:  90,
		Span365Days: 365,
	} {
		if got := span.Window(now, paris).Days(); got != want {
			t.Errorf("%s: want %d days, got %d", span, want, got)
		}
	}
	if !SpanAllTime.Window(now, paris).Unbounded() {
		t.Error("all time is unbounded")
	}
}

// Bars while a day is still a readable unit, a grid once it is not.
func TestBarsGiveWayToTheGridAtAMonth(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, paris)
	for _, span := range []Span{Span7Days, Span30Days} {
		if !span.SuitsDailyBars(now, paris) {
			t.Errorf("%s (%d days) fits in bars", span, span.Window(now, paris).Days())
		}
	}
	for _, span := range []Span{Span90Days, Span365Days, SpanAllTime} {
		if span.SuitsDailyBars(now, paris) {
			t.Errorf("%s is too long for bars", span)
		}
	}
	// The boundary itself, which is what MaxBarDays names.
	edge := DayWindow(now.AddDate(0, 0, -(MaxBarDays-1)), now, paris)
	if edge.Days() != MaxBarDays {
		t.Fatalf("precondition: %d days, want %d", edge.Days(), MaxBarDays)
	}
}

// A cookie and a query parameter are both user input: an unknown value
// is a value somebody typed, not a value to render.
func TestParseSpanRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, raw := range []string{"", "wat", "8d", "this_decade", "ALL"} {
		if got := ParseSpan(raw); got != DefaultSpan {
			t.Errorf("ParseSpan(%q) = %q, want the default %q", raw, got, DefaultSpan)
		}
	}
	for _, span := range Spans {
		if got := ParseSpan(string(span)); got != span {
			t.Errorf("ParseSpan(%q) = %q", span, got)
		}
		if span.Label() == "" {
			t.Errorf("%q has no label", span)
		}
	}
}

func TestActiveSecondsSubtractsIdleAndNeverGoesNegative(t *testing.T) {
	start := time.Date(2026, 8, 9, 20, 0, 0, 0, paris)
	ses := store.Session{StartedAt: start, EndedAt: start.Add(20 * time.Minute), IdleMs: 12 * 60 * 1000}
	if got := ActiveSeconds(ses); got != 8*60 {
		t.Errorf("twenty minutes less twelve idle is eight, got %v", got/60)
	}

	// A backward clock, or an idle figure larger than the sitting.
	broken := store.Session{StartedAt: start, EndedAt: start.Add(time.Minute), IdleMs: 60 * 60 * 1000}
	if got := ActiveSeconds(broken); got != 0 {
		t.Errorf("want nought, got %v", got)
	}
}

// A streak may end today or yesterday: a reader who has not opened a
// book before lunch has not broken anything.
func TestStreakMayEndYesterday(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, paris)
	days := map[string]bool{
		"2026-08-08": true,
		"2026-08-07": true,
		"2026-08-06": true,
	}
	if got := StreakDays(days, paris, now); got != 3 {
		t.Errorf("want 3, got %d", got)
	}

	days["2026-08-09"] = true
	if got := StreakDays(days, paris, now); got != 4 {
		t.Errorf("today extends it to 4, got %d", got)
	}

	stale := map[string]bool{"2026-08-06": true, "2026-08-05": true}
	if got := StreakDays(stale, paris, now); got != 0 {
		t.Errorf("a run that ended before yesterday is over, got %d", got)
	}
}
