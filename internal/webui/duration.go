package webui

import (
	"math"
	"strconv"
	"time"
)

// How a stretch of reading is written down.
//
// The rules are the Android app's (`ReadingDuration.kt`), unit for unit
// and word for word, so a reader looking at the phone and the browser
// side by side sees one number written one way rather than "2 h 5 min"
// in one place and "125 minutes" in the other.
//
// Every part truncates. A sitting of fifty-nine and a half minutes is
// "59 min", never "1 h": rounding a duration up makes the page claim
// reading that did not happen, and the totals on the same screen would
// stop adding up.

const (
	minutesPerHour = 60
	hoursPerDay    = 24
)

// durationParts is a duration broken into the units it will be written
// in, and which of the four shapes it takes.
type durationParts struct {
	days, hours, minutes int
	// none is a duration of nothing at all, which is a different fact
	// from a duration too short to name.
	none bool
	// under is positive but under a minute: real reading, no minutes to
	// show for it.
	under bool
}

// asDuration turns a count of some unit into a duration without losing
// what is too small to see or wrapping on what is too big to hold.
//
// Both ends are reachable from a client. Reading measured in
// milliseconds arrives as a fraction of a second, and truncating the
// count before scaling it would write a real half-second sitting as
// "None". At the other end the API accepts an active time up to the
// largest integer JSON carries exactly, which is millions of years:
// time.Duration tops out at about 292, and past that the multiplication
// wraps negative and a genuine total is written as "None" as well.
func asDuration(count float64, unit time.Duration) time.Duration {
	ns := count * float64(unit)
	switch {
	// Also catches NaN, which no comparison would.
	case !(ns > 0):
		return 0
	case ns >= float64(math.MaxInt64):
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ns)
}

func splitDuration(d time.Duration) durationParts {
	switch {
	case d <= 0:
		return durationParts{none: true}
	case d < time.Minute:
		return durationParts{under: true}
	}
	total := int(d.Minutes())
	return durationParts{
		days:    total / (minutesPerHour * hoursPerDay),
		hours:   total / minutesPerHour % hoursPerDay,
		minutes: total % minutesPerHour,
	}
}

// readingDuration writes a duration out in full.
//
//	0            -> "None"
//	30s          -> "Less than a minute"
//	45m          -> "45 min"
//	2h           -> "2 h"
//	1h5m         -> "1 h 5 min"
//	3d           -> "3 d"
//	2d3h         -> "2 d 3 h"
//
// Below a day the minutes matter and the seconds do not; above one the
// hours matter and the minutes do not. Naming every unit all the way
// down would put "2 d 3 h 14 min" on a card, which is a measurement
// rather than an answer.
func readingDuration(d time.Duration) string {
	p := splitDuration(d)
	switch {
	case p.none:
		return "None"
	case p.under:
		return "Less than a minute"
	case p.days > 0 && p.hours == 0:
		return strconv.Itoa(p.days) + " d"
	case p.days > 0:
		return strconv.Itoa(p.days) + " d " + strconv.Itoa(p.hours) + " h"
	case p.hours > 0 && p.minutes == 0:
		return strconv.Itoa(p.hours) + " h"
	case p.hours > 0:
		return strconv.Itoa(p.hours) + " h " + strconv.Itoa(p.minutes) + " min"
	default:
		return strconv.Itoa(p.minutes) + " min"
	}
}

// readingMinutes is readingDuration for a count of minutes, which is
// what the statistics are aggregated in.
func readingMinutes(minutes float64) string {
	return readingDuration(asDuration(minutes, time.Minute))
}

// compactDuration is the same duration with the spaces taken out, for a
// bar label or a tooltip where there is room for a few characters and
// no more.
//
//	0            -> ""
//	30s          -> "1m"
//	45m          -> "45m"
//	2h           -> "2h"
//	2h5m         -> "2h05"
//	3d           -> "3d"
//	3d4h         -> "3d4h"
//
// The minutes are padded to two digits because "2h5" reads as a decimal
// and "2h05" cannot. Anything positive but under a minute is written as
// "1m": the label sits over a bar that is visibly there, and "0m" would
// contradict it.
func compactDuration(d time.Duration) string {
	p := splitDuration(d)
	switch {
	case p.none:
		return ""
	case p.under:
		return "1m"
	case p.days > 0 && p.hours == 0:
		return strconv.Itoa(p.days) + "d"
	case p.days > 0:
		return strconv.Itoa(p.days) + "d" + strconv.Itoa(p.hours) + "h"
	case p.hours > 0 && p.minutes == 0:
		return strconv.Itoa(p.hours) + "h"
	case p.hours > 0:
		return strconv.Itoa(p.hours) + "h" + pad2(p.minutes)
	default:
		return strconv.Itoa(p.minutes) + "m"
	}
}

// compactMinutes is compactDuration for a count of minutes.
func compactMinutes(minutes float64) string {
	return compactDuration(asDuration(minutes, time.Minute))
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// sittings counts sittings in the app's words. "Sitting" rather than
// "session" throughout: a session is what a server has, a sitting is
// what a person did.
func sittings(n int) string {
	if n == 1 {
		return "Read in one sitting"
	}
	return "Read in " + strconv.Itoa(n) + " sittings"
}
