package insights

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ErrMissingEdition means a session names an edition SHA absent from the snapshot.
var ErrMissingEdition = errors.New("missing edition")

type Summary struct {
	TotalActiveMinutes float64 `json:"total_active_minutes"`
	TotalPages         float64 `json:"total_pages"`
	Sessions           int     `json:"sessions"`
	StreakDays         int     `json:"streak_days"`
	SpeedProgPerHour   float64 `json:"speed_prog_per_hour"`
}

type Work struct {
	WorkID             string     `json:"work_id"`
	Title              string     `json:"title,omitempty"`
	Author             string     `json:"author,omitempty"`
	Sessions           int        `json:"sessions"`
	TotalActiveMinutes float64    `json:"total_active_minutes"`
	TotalPages         float64    `json:"total_pages"`
	CurrentProgression float64    `json:"current_progression"`
	ETASeconds         *float64   `json:"eta_seconds"`
	LastReadAt         *time.Time `json:"last_read_at"`
	// FirstReadDay is the account-local day the work was first read,
	// over the whole record whatever window was asked for: a book begun
	// last year was still begun last year when only this week is shown.
	// A device whose own history starts later has no other way to know.
	FirstReadDay *string `json:"first_read_day,omitempty"`
}

type Day struct {
	Date     string  `json:"date"`
	Minutes  float64 `json:"minutes"`
	Pages    float64 `json:"pages"`
	Sessions int     `json:"sessions"`
}

type Result struct {
	Summary          Summary
	Works            []Work
	ByWork           map[string]Work
	Days             []Day
	ActiveDays       map[string]bool
	FirstActivityDay *string
	Complete         bool
	IncompleteReason string
}

// Pages reads only metadata captured with the sessions. A failed metadata
// query must not become a zero-page rollup that replaces the original record.
func Pages(ses store.Session, editions map[string]store.Edition) (float64, error) {
	if ses.ReportedPages != nil {
		if *ses.ReportedPages < 0 || math.IsNaN(*ses.ReportedPages) || math.IsInf(*ses.ReportedPages, 0) {
			return 0, fmt.Errorf("invalid reported pages for session %s", ses.SessionID)
		}
		return *ses.ReportedPages, nil
	}
	delta := math.Max(0, ses.EndProg-ses.StartProg)
	if delta == 0 || ses.EditionSHA == nil {
		return 0, nil
	}
	edition, ok := editions[*ses.EditionSHA]
	if !ok {
		return 0, fmt.Errorf("%w for session %s sha %s", ErrMissingEdition, ses.SessionID, *ses.EditionSHA)
	}
	if edition.PageCount == nil {
		return 0, nil
	}
	return delta * float64(*edition.PageCount), nil
}

// SittingGap is the pause that ends a sitting. A reader records a new
// session whenever it is paused, so locking the screen or glancing at
// another app would otherwise turn one evening into dozens of sittings.
const SittingGap = 5 * time.Minute

// SittingStarts reports which sessions begin a sitting: those of a work
// that start at least SittingGap after every earlier one of that work on
// the same account-local day has ended, whichever device recorded them.
// A sitting never spans two days, so a day's count needs no other day and
// a window or an overlap made of whole days counts the same sittings
// either way. Compacted rollups keep one per session.
func SittingStarts(sessions []store.Session, loc *time.Location) []bool {
	type key struct{ workID, day string }
	groups := make(map[key][]int)
	for i, ses := range sessions {
		k := key{ses.WorkID, ses.EndedAt.In(loc).Format(DayFormat)}
		groups[k] = append(groups[k], i)
	}
	starts := make([]bool, len(sessions))
	for _, idx := range groups {
		sort.Slice(idx, func(a, b int) bool {
			x, y := sessions[idx[a]], sessions[idx[b]]
			if !x.StartedAt.Equal(y.StartedAt) {
				return x.StartedAt.Before(y.StartedAt)
			}
			if !x.EndedAt.Equal(y.EndedAt) {
				return x.EndedAt.Before(y.EndedAt)
			}
			return x.SessionID < y.SessionID
		})
		var end time.Time
		for n, i := range idx {
			ses := sessions[i]
			if n == 0 || ses.StartedAt.Sub(end) >= SittingGap {
				starts[i] = true
				end = ses.EndedAt
			} else if ses.EndedAt.After(end) {
				end = ses.EndedAt
			}
		}
	}
	return starts
}

// Build aggregates one coherent store snapshot without performing further reads.
// Legacy buckets remain visible, but cannot certify exact cross-device overlap.
func Build(snap store.StatsSnapshot, win Window, now time.Time) (Result, error) {
	out := Result{
		Works: []Work{}, Days: []Day{}, ByWork: make(map[string]Work),
		ActiveDays: make(map[string]bool), Complete: true,
	}
	loc, err := time.LoadLocation(snap.Timezone)
	if err != nil {
		return out, err
	}
	from, to := win.DayBounds(now, loc)
	today := now.In(loc).Format(DayFormat)
	byDay := make(map[string]*Day)
	type pace struct {
		seconds, progression float64
		unknown              bool
	}
	paces := make(map[string]pace)
	// Both halves of the record count, because an old sitting no longer
	// exists one by one: it lives on as the day its rollup names, and a
	// work read for a year would otherwise claim to have started
	// whenever compaction happened to stop. A raw sitting is placed on
	// the day it ended, which is the day its rollup will name once it is
	// compacted; placing it by its start would move the date the day the
	// sitting aged out.
	firstDays := make(map[string]string)
	recordFirstDay := func(workID, day string) {
		if first, ok := firstDays[workID]; !ok || day < first {
			firstDays[workID] = day
		}
	}
	for _, w := range snap.Works {
		out.ByWork[w.ID] = Work{WorkID: w.ID, Title: w.Title, Author: w.Author}
	}
	recordActiveDay := func(day string, seconds float64) {
		if seconds > 0 && day <= today {
			out.ActiveDays[day] = true
			if out.FirstActivityDay == nil || day < *out.FirstActivityDay {
				date := day
				out.FirstActivityDay = &date
			}
		}
	}
	add := func(workID, day string, seconds, pages float64, count int, last time.Time, measuredSeconds, delta float64, legacy bool) {
		recordActiveDay(day, seconds)
		if day < from || day > to {
			return
		}
		work := out.ByWork[workID]
		work.WorkID = workID
		work.TotalActiveMinutes += seconds / 60
		work.TotalPages += pages
		work.Sessions += count
		if work.LastReadAt == nil || last.After(*work.LastReadAt) {
			at := last
			work.LastReadAt = &at
		}
		out.ByWork[workID] = work
		p := paces[workID]
		p.seconds += measuredSeconds
		p.progression += delta
		p.unknown = p.unknown || legacy
		paces[workID] = p
		d := byDay[day]
		if d == nil {
			d = &Day{Date: day}
			byDay[day] = d
		}
		d.Minutes += seconds / 60
		d.Pages += pages
		d.Sessions += count
	}
	sittingStarts := SittingStarts(snap.Sessions, loc)
	for i, ses := range snap.Sessions {
		pages, err := Pages(ses, snap.Editions)
		if err != nil {
			return out, err
		}
		active := ActiveSeconds(ses)
		var measured, delta float64
		if ses.Origin != store.OriginInferred {
			measured = active
			delta = math.Max(0, ses.EndProg-ses.StartProg)
		}
		endedDay := ses.EndedAt.In(loc).Format(DayFormat)
		recordFirstDay(ses.WorkID, endedDay)
		sitting := 0
		if sittingStarts[i] {
			sitting = 1
		}
		add(ses.WorkID, endedDay, active, pages, sitting,
			ses.EndedAt, measured, delta, false)
	}
	for _, ru := range snap.Rollups {
		day, err := time.Parse(DayFormat, ru.Day)
		if err != nil {
			return out, err
		}
		recordFirstDay(ru.WorkID, ru.Day)
		recordActiveDay(ru.Day, ru.ActiveSeconds)
		if ru.AttributionVersion != 2 && win.HoldsDay(ru.Day) {
			out.Complete = false
			out.IncompleteReason = "legacy_or_different_timezone_rollups"
		}
		if ru.AttributionVersion == 2 && ru.Timezone != snap.Timezone {
			intersects, err := rollupDayIntersectsWindow(ru, win, now)
			if err != nil {
				return out, err
			}
			if intersects {
				out.Complete = false
				out.IncompleteReason = "legacy_or_different_timezone_rollups"
			} else {
				continue
			}
		}
		dayStart, _ := DayWindow(day, day, loc).SessionBounds(now, loc)
		add(ru.WorkID, ru.Day, ru.ActiveSeconds, ru.Pages, int(ru.SessionCount), dayStart,
			ru.MeasuredActiveSeconds, ru.MeasuredProgDelta, ru.AttributionVersion != 2)
	}
	var totalPace pace
	workIDs := make([]string, 0, len(out.ByWork))
	for id := range out.ByWork {
		workIDs = append(workIDs, id)
	}
	sort.Strings(workIDs)
	// Calendar chunks must reproduce the same floating-point totals at one
	// revision, regardless of Go's map iteration order.
	for _, id := range workIDs {
		work := out.ByWork[id]
		if day, ok := firstDays[id]; ok {
			work.FirstReadDay = &day
		}
		p := paces[id]
		if position, ok := snap.Positions[id]; ok {
			work.CurrentProgression = position.Progression
			if !p.unknown && p.seconds > 0 && p.progression > 0 && position.Progression < 1 {
				eta := (1 - position.Progression) * p.seconds / p.progression
				work.ETASeconds = &eta
			}
		}
		out.ByWork[id] = work
		if work.Sessions == 0 && work.TotalActiveMinutes == 0 && work.TotalPages == 0 {
			continue
		}
		out.Works = append(out.Works, work)
		out.Summary.TotalActiveMinutes += work.TotalActiveMinutes
		out.Summary.TotalPages += work.TotalPages
		out.Summary.Sessions += work.Sessions
		totalPace.seconds += p.seconds
		totalPace.progression += p.progression
		totalPace.unknown = totalPace.unknown || p.unknown
	}
	if !totalPace.unknown && totalPace.seconds > 0 {
		out.Summary.SpeedProgPerHour = totalPace.progression / totalPace.seconds * 3600
	}
	out.Summary.StreakDays = StreakDays(out.ActiveDays, loc, now)
	for _, day := range byDay {
		out.Days = append(out.Days, *day)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date < out.Days[j].Date })
	sort.Slice(out.Works, func(i, j int) bool {
		if out.Works[i].TotalActiveMinutes == out.Works[j].TotalActiveMinutes {
			return out.Works[i].WorkID < out.Works[j].WorkID
		}
		return out.Works[i].TotalActiveMinutes > out.Works[j].TotalActiveMinutes
	})
	return out, nil
}

func rollupDayIntersectsWindow(rollup store.SessionRollup, win Window, now time.Time) (bool, error) {
	loc, err := time.LoadLocation(rollup.Timezone)
	if err != nil {
		return false, err
	}
	day, err := time.Parse(DayFormat, rollup.Day)
	if err != nil {
		return false, err
	}
	rollupWindow := DayWindow(day, day, loc)
	from, to := rollupWindow.SessionBounds(now, loc)
	if win.Unbounded() {
		return true, nil
	}
	return from.Before(win.to) && to.After(win.from), nil
}
