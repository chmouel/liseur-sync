package insights

import (
	"math"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const ComparisonTimeFormat = "15:04:05.000"

// ComparisonWindow is a bounded date span whose last day stops at one
// account-local wall-clock time rather than at midnight.
type ComparisonWindow struct {
	fromDay string
	toDay   string
	through string
	from    time.Time
	cutoff  time.Time
}

// NewComparisonWindow validates and resolves a comparison span. Repeated
// wall times use the earlier occurrence; skipped wall times move forward by
// the size of the gap, matching java.time.LocalDateTime.atZone.
func NewComparisonWindow(rawFrom, rawTo, rawThrough string, loc *time.Location) (ComparisonWindow, bool) {
	fromDay, errFrom := time.Parse(DayFormat, rawFrom)
	toDay, errTo := time.Parse(DayFormat, rawTo)
	hour, minute, second, millisecond, through, ok := parseComparisonTime(rawThrough)
	if errFrom != nil || errTo != nil || toDay.Before(fromDay) || !ok {
		return ComparisonWindow{}, false
	}
	from, ok := resolveWallTime(fromDay, 0, 0, 0, 0, loc)
	if !ok {
		return ComparisonWindow{}, false
	}
	cutoff, ok := resolveWallTime(toDay, hour, minute, second, millisecond*int(time.Millisecond), loc)
	if !ok {
		return ComparisonWindow{}, false
	}
	return ComparisonWindow{
		fromDay: rawFrom,
		toDay:   rawTo,
		through: through,
		from:    from,
		cutoff:  cutoff,
	}, true
}

func (w ComparisonWindow) FromDay() string { return w.fromDay }
func (w ComparisonWindow) ToDay() string   { return w.toDay }
func (w ComparisonWindow) Through() string { return w.through }

func (w ComparisonWindow) Days() int {
	from, errFrom := time.Parse(DayFormat, w.fromDay)
	to, errTo := time.Parse(DayFormat, w.toDay)
	if errFrom != nil || errTo != nil {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}

// HoldsSession reports whether a raw session contributes positive active
// time to this span.
func (w ComparisonWindow) HoldsSession(ses store.Session) bool {
	ms, ok := comparisonSessionMillis(ses, w)
	return ok && ms > 0
}

// SpanMinutes sums one comparison span from a coherent statistics snapshot.
//
// Raw sessions can be divided at the cutoff because their original
// timestamps remain available. A rollup on or after the partial last day
// cannot prove whether one of its sessions crossed the cutoff, so the whole
// comparison is refused rather than approximated.
func SpanMinutes(snap store.StatsSnapshot, win ComparisonWindow, loc *time.Location) (float64, bool) {
	var total int64
	add := func(ms int64) bool {
		if ms < 0 || total > math.MaxInt64-ms {
			return false
		}
		total += ms
		return true
	}
	for _, ses := range snap.Sessions {
		ms, ok := comparisonSessionMillis(ses, win)
		if !ok || !add(ms) {
			return 0, false
		}
	}
	for _, rollup := range snap.Rollups {
		if _, err := time.Parse(DayFormat, rollup.Day); err != nil {
			return 0, false
		}
		if rollup.ActiveSeconds <= 0 {
			continue
		}
		if rollup.AttributionVersion != 2 || rollup.Timezone == "" {
			if rollup.Day < win.fromDay {
				continue
			}
			return 0, false
		}
		if rollup.Timezone != snap.Timezone {
			rollupLoc, err := time.LoadLocation(rollup.Timezone)
			if err != nil {
				return 0, false
			}
			day, _ := time.Parse(DayFormat, rollup.Day)
			_, rollupEnd := DayWindow(day, day, rollupLoc).SessionBounds(win.cutoff, rollupLoc)
			if !rollupEnd.After(win.from) {
				continue
			}
			return 0, false
		}
		if rollup.Day < win.fromDay {
			continue
		}
		if rollup.Day >= win.toDay {
			return 0, false
		}
		if rollup.ComparisonActiveMs == nil || *rollup.ComparisonActiveMs < 0 {
			return 0, false
		}
		ms := *rollup.ComparisonActiveMs
		if !add(ms) {
			return 0, false
		}
	}
	return float64(total) / 60_000, true
}

func comparisonSessionMillis(ses store.Session, win ComparisonWindow) (int64, bool) {
	if ses.EndedAt.Before(ses.StartedAt) {
		return 0, true
	}
	active, ok := sessionActiveMilliseconds(ses)
	if !ok || active <= 0 || ses.EndedAt.Before(win.from) {
		return 0, ok
	}
	if !ses.EndedAt.After(win.cutoff) {
		return active, true
	}
	if !ses.StartedAt.Before(win.cutoff) {
		return 0, true
	}
	extent := ses.EndedAt.UnixMilli() - ses.StartedAt.UnixMilli()
	elapsed := win.cutoff.UnixMilli() - ses.StartedAt.UnixMilli()
	if extent <= 0 || elapsed <= 0 {
		return 0, true
	}
	counted := math.Round(float64(active) * float64(elapsed) / float64(extent))
	if counted <= 0 {
		return 0, true
	}
	if counted > float64(math.MaxInt64) {
		return 0, false
	}
	return int64(counted), true
}

func sessionActiveMilliseconds(ses store.Session) (int64, bool) {
	if ses.ActiveMs != nil {
		if *ses.ActiveMs < 0 {
			return 0, false
		}
		return *ses.ActiveMs, true
	}
	span := ses.EndedAt.UnixMilli() - ses.StartedAt.UnixMilli()
	if span <= ses.IdleMs {
		return 0, true
	}
	return span - ses.IdleMs, true
}

func parseComparisonTime(raw string) (hour, minute, second, millisecond int, normalized string, ok bool) {
	if len(raw) != 5 && len(raw) != 8 && (len(raw) < 10 || len(raw) > 12) {
		return 0, 0, 0, 0, "", false
	}
	if raw[2] != ':' || (len(raw) > 5 && raw[5] != ':') || (len(raw) > 8 && raw[8] != '.') {
		return 0, 0, 0, 0, "", false
	}
	digit := func(i int) (int, bool) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
		return int(raw[i] - '0'), true
	}
	pair := func(i int) (int, bool) {
		a, aok := digit(i)
		b, bok := digit(i + 1)
		return a*10 + b, aok && bok
	}
	var valid bool
	if hour, valid = pair(0); !valid {
		return 0, 0, 0, 0, "", false
	}
	if minute, valid = pair(3); !valid {
		return 0, 0, 0, 0, "", false
	}
	if hour > 23 || minute > 59 {
		return 0, 0, 0, 0, "", false
	}
	if len(raw) == 5 {
		second, millisecond = 0, 0
	} else if second, valid = pair(6); !valid || second > 59 {
		return 0, 0, 0, 0, "", false
	} else if len(raw) == 8 {
		millisecond = 0
	} else {
		fraction := 0
		for i := 9; i < len(raw); i++ {
			value, valid := digit(i)
			if !valid {
				return 0, 0, 0, 0, "", false
			}
			fraction = fraction*10 + value
		}
		switch len(raw) - 9 {
		case 1:
			millisecond = fraction * 100
		case 2:
			millisecond = fraction * 10
		case 3:
			millisecond = fraction
		default:
			return 0, 0, 0, 0, "", false
		}
	}
	at := time.Date(0, 1, 1, hour, minute, second, millisecond*int(time.Millisecond), time.UTC)
	return hour, minute, second, millisecond, at.Format(ComparisonTimeFormat), true
}

func resolveWallTime(day time.Time, hour, minute, second, nanosecond int, loc *time.Location) (time.Time, bool) {
	naive := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, nanosecond, time.UTC)
	offsets := make(map[int]bool)
	guess := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, nanosecond, loc)
	for probe := guess.Add(-48 * time.Hour); !probe.After(guess.Add(48 * time.Hour)); probe = probe.Add(15 * time.Minute) {
		_, offset := probe.In(loc).Zone()
		offsets[offset] = true
	}
	match := func(wall time.Time) (time.Time, bool) {
		var earliest time.Time
		for offset := range offsets {
			candidate := wall.Add(-time.Duration(offset) * time.Second)
			local := candidate.In(loc)
			if local.Year() != wall.Year() || local.Month() != wall.Month() || local.Day() != wall.Day() ||
				local.Hour() != wall.Hour() || local.Minute() != wall.Minute() ||
				local.Second() != wall.Second() || local.Nanosecond() != wall.Nanosecond() {
				continue
			}
			if earliest.IsZero() || candidate.Before(earliest) {
				earliest = candidate
			}
		}
		return earliest, !earliest.IsZero()
	}
	if instant, ok := match(naive); ok {
		return instant, true
	}
	values := make([]int, 0, len(offsets))
	for offset := range offsets {
		values = append(values, offset)
	}
	sort.Ints(values)
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			gap := values[j] - values[i]
			if gap <= 0 {
				continue
			}
			if instant, ok := match(naive.Add(time.Duration(gap) * time.Second)); ok {
				return instant, true
			}
		}
	}
	return time.Time{}, false
}
