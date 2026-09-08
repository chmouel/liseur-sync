package webui

import (
	"testing"
	"time"
)

// The table is the Android app's (`ReadingDuration.kt`), which is the
// point: the two products write a duration the same way or a reader
// looking at both is reading two different numbers.
func TestReadingDurationMatchesTheApp(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		full string
		flat string
	}{
		{0, "None", ""},
		{-time.Hour, "None", ""},
		{30 * time.Second, "Less than a minute", "1m"},
		{59 * time.Second, "Less than a minute", "1m"},
		{time.Minute, "1 min", "1m"},
		{45 * time.Minute, "45 min", "45m"},
		{2 * time.Hour, "2 h", "2h"},
		{time.Hour + 5*time.Minute, "1 h 5 min", "1h05"},
		{2*time.Hour + 30*time.Minute, "2 h 30 min", "2h30"},
		{72 * time.Hour, "3 d", "3d"},
		{51 * time.Hour, "2 d 3 h", "2d3h"},
	} {
		if got := readingDuration(tc.in); got != tc.full {
			t.Errorf("readingDuration(%s) = %q, want %q", tc.in, got, tc.full)
		}
		if got := compactDuration(tc.in); got != tc.flat {
			t.Errorf("compactDuration(%s) = %q, want %q", tc.in, got, tc.flat)
		}
	}
}

// Truncation, not rounding. A page that rounds a duration up claims
// reading that did not happen, and the totals beside it stop adding up.
func TestReadingDurationNeverRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{59*time.Minute + 59*time.Second, "59 min"},
		{time.Hour + 59*time.Second, "1 h"},
		{24*time.Hour - time.Second, "23 h 59 min"},
		{48*time.Hour - time.Second, "1 d 23 h"},
	} {
		if got := readingDuration(tc.in); got != tc.want {
			t.Errorf("readingDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The minutes in a compact label are padded, because "2h5" reads as a
// decimal and "2h05" cannot.
func TestCompactDurationPadsItsMinutes(t *testing.T) {
	if got := compactDuration(2*time.Hour + 5*time.Minute); got != "2h05" {
		t.Errorf("got %q, want %q", got, "2h05")
	}
	if got := compactDuration(2*time.Hour + 15*time.Minute); got != "2h15" {
		t.Errorf("got %q, want %q", got, "2h15")
	}
}

// Minutes are what the statistics are aggregated in, so the helper that
// takes them has to agree with the one that takes a duration.
func TestReadingMinutesAgreesWithReadingDuration(t *testing.T) {
	for _, minutes := range []float64{0, 0.4, 1, 45, 120, 125, 1440, 3060} {
		want := readingDuration(time.Duration(minutes * float64(time.Minute)))
		if got := readingMinutes(minutes); got != want {
			t.Errorf("readingMinutes(%v) = %q, want %q", minutes, got, want)
		}
	}
}

// "1 sittings" reads as a bug even when the number is right.
func TestSittingsCountsInWords(t *testing.T) {
	for n, want := range map[int]string{
		0: "Read in 0 sittings",
		1: "Read in one sitting",
		2: "Read in 2 sittings",
	} {
		if got := sittings(n); got != want {
			t.Errorf("sittings(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestReadingSpanCountsDatesNotHours pins the day count to the calendar.
// Local midnights are twenty-three hours apart across a spring-forward
// and twenty-five across an autumn fall-back, so elapsed hours are not
// a day count.
func TestReadingSpanCountsDatesNotHours(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no timezone database")
	}
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 12, 0, 0, 0, paris)
	}
	for _, tc := range []struct {
		name     string
		from, to time.Time
		wants    string
	}{
		{"one afternoon", day(2026, time.March, 28), day(2026, time.March, 28), "1 day"},
		{"over a spring forward", day(2026, time.March, 28), day(2026, time.March, 30), "3 days"},
		{"over an autumn fall back", day(2026, time.October, 24), day(2026, time.October, 26), "3 days"},
		{"a plain week", day(2026, time.June, 1), day(2026, time.June, 7), "7 days"},
	} {
		if got := readingSpanDays(tc.from, tc.to); got != tc.wants {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.wants)
		}
	}
}

// TestReadingKeepsMeasuredTimeAtBothExtremes covers the two ways a
// scaled count used to come out as "None" for reading that happened.
//
// Native clients report an active time in milliseconds, so a short
// sitting arrives as a fraction of a second and truncating before
// scaling would lose all of it. The other end is the API's ceiling: it
// accepts a millisecond count up to the largest integer JSON carries
// exactly, which is far past what a time.Duration holds, and the
// multiplication used to wrap negative. Past the ceiling the answer
// stops at the ceiling, which no real reading reaches; what matters is
// that it is not written as nothing.
func TestReadingKeepsMeasuredTimeAtBothExtremes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		got   string
		wants string
	}{
		{"half a second", readingDuration(asDuration(0.5, time.Second)), "Less than a minute"},
		{"a millisecond", readingDuration(asDuration(0.001, time.Second)), "Less than a minute"},
		{"nothing at all", readingDuration(asDuration(0, time.Second)), "None"},
		{"the API's largest active time", readingDuration(
			asDuration(9007199254740991.0/1000, time.Second)), "106751 d 23 h"},
		{"minutes past what a duration holds", readingMinutes(1e18), "106751 d 23 h"},
	} {
		if tc.got != tc.wants {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.wants)
		}
	}
}
