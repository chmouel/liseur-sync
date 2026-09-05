package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

const maxCalendarDays = 4000

func describe(win insights.Window, answer map[string]any) {
	answer["range_days"] = win.Days()
	if !win.Unbounded() {
		answer["from"] = win.FromDay()
		answer["to"] = win.ToDay()
	}
}

func describeSnapshot(snap store.StatsSnapshot, answer map[string]any) {
	answer["timezone"] = snap.Timezone
	answer["stats_revision"] = strconv.FormatInt(snap.Revision, 10)
	answer["attribution_version"] = 2
}

func (s *Server) insightInput(w http.ResponseWriter, r *http.Request, ids []string) (store.StatsSnapshot, *time.Location, bool) {
	tok, _ := auth.TokenFrom(r)
	snap, err := s.St.StatisticsSnapshot(r.Context(), tok.UserID, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "statistics read failed")
		return snap, nil, false
	}
	loc, err := time.LoadLocation(snap.Timezone)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid account timezone")
		return snap, nil, false
	}
	return snap, loc, true
}

func buildInsights(w http.ResponseWriter, snap store.StatsSnapshot, win insights.Window, now time.Time) (insights.Result, bool) {
	result, err := insights.Build(snap, win, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "statistics aggregation failed")
		return result, false
	}
	return result, true
}

func (s *Server) HandleInsightsSummary(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	snap, loc, ok := s.insightInput(w, r, nil)
	if !ok {
		return
	}
	win := requestWindow(r, loc, now, insights.DefaultSummaryRange)
	result, ok := buildInsights(w, snap, win, now)
	if !ok {
		return
	}
	sum := result.Summary
	answer := map[string]any{
		"total_active_minutes": sum.TotalActiveMinutes, "total_pages": sum.TotalPages,
		"sessions": sum.Sessions, "streak_days": sum.StreakDays,
		"speed_prog_per_hour": sum.SpeedProgPerHour, "first_activity_day": result.FirstActivityDay,
	}
	describe(win, answer)
	describeSnapshot(snap, answer)
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) HandleInsightsWorks(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	snap, loc, ok := s.insightInput(w, r, nil)
	if !ok {
		return
	}
	win := requestWindow(r, loc, now, "")
	result, ok := buildInsights(w, snap, win, now)
	if !ok {
		return
	}
	answer := map[string]any{"works": result.Works}
	describe(win, answer)
	describeSnapshot(snap, answer)
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) HandleInsightsWork(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	snap, loc, ok := s.insightInput(w, r, nil)
	if !ok {
		return
	}
	win := requestWindow(r, loc, now, "")
	result, ok := buildInsights(w, snap, win, now)
	if !ok {
		return
	}
	work, found := result.ByWork[r.PathValue("id")]
	if !found {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		insights.Work
		RangeDays int    `json:"range_days"`
		From      string `json:"from,omitempty"`
		To        string `json:"to,omitempty"`
		Timezone  string `json:"timezone"`
	}{work, win.Days(), win.FromDay(), win.ToDay(), snap.Timezone})
}

func (s *Server) HandleInsightsCalendar(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	snap, loc, ok := s.insightInput(w, r, nil)
	if !ok {
		return
	}
	win, bounded, ok := calendarBounds(r, loc)
	if !ok {
		writeError(w, http.StatusBadRequest, "calendar span too long")
		return
	}
	result, ok := buildInsights(w, snap, win, now)
	if !ok {
		return
	}
	first, _ := time.Parse(insights.DayFormat, win.FromDay())
	answer := map[string]any{"year": first.Year(), "days": result.Days}
	if bounded {
		describe(win, answer)
	}
	describeSnapshot(snap, answer)
	writeJSON(w, http.StatusOK, answer)
}

func calendarBounds(r *http.Request, loc *time.Location) (insights.Window, bool, bool) {
	q := r.URL.Query()
	rawFrom, rawTo := q.Get("from"), q.Get("to")
	if rawFrom != "" && rawTo != "" {
		from, errFrom := time.ParseInLocation(insights.DayFormat, rawFrom, loc)
		to, errTo := time.ParseInLocation(insights.DayFormat, rawTo, loc)
		if errFrom == nil && errTo == nil && !to.Before(from) {
			win := insights.DayWindow(from, to, loc)
			return win, true, win.Days() <= maxCalendarDays
		}
	}
	year := time.Now().In(loc).Year()
	if v, err := strconv.Atoi(q.Get("year")); err == nil && v > 1970 && v < 3000 {
		year = v
	}
	first := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	return insights.DayWindow(first, first.AddDate(1, 0, -1), loc), false, true
}

func requestWindow(r *http.Request, loc *time.Location, now time.Time, def string) insights.Window {
	q := r.URL.Query()
	return insights.ParseWindow(q.Get("from"), q.Get("to"), q.Get("range"), def, loc, now)
}
