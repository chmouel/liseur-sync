package insights

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

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
		return 0, fmt.Errorf("missing edition for session %s", ses.SessionID)
	}
	if edition.PageCount == nil {
		return 0, nil
	}
	return delta * float64(*edition.PageCount), nil
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
	for _, w := range snap.Works {
		out.ByWork[w.ID] = Work{WorkID: w.ID, Title: w.Title, Author: w.Author}
	}
	add := func(workID, day string, seconds, pages float64, count int, last time.Time, measuredSeconds, delta float64, legacy bool) {
		if seconds > 0 && day <= today {
			out.ActiveDays[day] = true
			if out.FirstActivityDay == nil || day < *out.FirstActivityDay {
				date := day
				out.FirstActivityDay = &date
			}
		}
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
	for _, ses := range snap.Sessions {
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
		add(ses.WorkID, ses.EndedAt.In(loc).Format(DayFormat), active, pages, 1,
			ses.EndedAt, measured, delta, false)
	}
	for _, ru := range snap.Rollups {
		day, err := time.ParseInLocation(DayFormat, ru.Day, loc)
		if err != nil {
			return out, err
		}
		if ru.AttributionVersion != 2 || ru.Timezone != snap.Timezone {
			out.Complete = false
			out.IncompleteReason = "legacy_or_different_timezone_rollups"
		}
		add(ru.WorkID, ru.Day, ru.ActiveSeconds, ru.Pages, int(ru.SessionCount), day,
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
