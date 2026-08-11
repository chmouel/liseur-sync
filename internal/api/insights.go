package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Insights semantics (design §6, plan):
// - pages read = max(0, end-start progression delta) x edition page
//   count when known; negative deltas (rereads) count time but zero
//   pages — never fabricated, never negative.
//
//   "When known" is the common case for it not to be. Only the KOReader
//   statistics adapter reports a page count, because only a paginated
//   reader has one to report: a reflowable EPUB has no inherent number
//   of pages, and inventing one from whichever device happened to sync
//   first would make the figure depend on that device's font size. So
//   total_pages is legitimately 0 for a native client, and the web UI
//   hides the tile rather than showing a zero that looks like a fault.
// - day boundaries use the user's configured IANA timezone; sessions
//   crossing midnight split across tz-local days.
// - speed = progression delta / active duration (duration - idle).
// - ETA = remaining progression / rolling speed.

type dayStat struct {
	Date    string  `json:"date"`
	Minutes float64 `json:"minutes"`
	Pages   float64 `json:"pages"`
}

// userLocation loads the user's configured timezone.
func (s *Server) userLocation(r *http.Request, userID string) *time.Location {
	u, err := s.St.UserByID(r.Context(), userID)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// activeSeconds returns duration minus idle, clamped >= 0.
func activeSeconds(ses store.Session) float64 {
	d := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	if d < 0 {
		return 0
	}
	return d
}

// pagesRead converts a progression delta to pages using the session's
// edition page count; negative deltas yield zero.
func (s *Server) pagesRead(ctx context.Context, ses store.Session) float64 {
	delta := ses.EndProg - ses.StartProg
	if delta <= 0 || ses.EditionSHA == nil {
		return 0
	}
	ed, err := s.St.EditionBySHA(ctx, ses.UserID, *ses.EditionSHA)
	if err != nil || ed.PageCount == nil {
		return 0
	}
	return delta * float64(*ed.PageCount)
}

// HandleInsightsSummary implements GET /v1/insights/summary?range=30d.
func (s *Server) HandleInsightsSummary(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	days := parseRangeDays(r.URL.Query().Get("range"), 30)
	now := time.Now()
	from := now.AddDate(0, 0, -days)
	sessions, err := s.St.SessionsInRange(r.Context(), tok.UserID, from, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "summary failed")
		return
	}
	loc := s.userLocation(r, tok.UserID)

	var totalActive float64
	var totalPages float64
	sessionCount := len(sessions)
	daySet := map[string]bool{}
	// Rolling speed: progression delta per active second over range.
	var progDelta float64
	for _, ses := range sessions {
		a := activeSeconds(ses)
		totalActive += a
		totalPages += s.pagesRead(r.Context(), ses)
		if d := ses.EndProg - ses.StartProg; d > 0 {
			progDelta += d
		}
		for _, day := range splitDays(ses, loc) {
			daySet[day.date] = true
		}
	}
	// Aged sessions live as daily rollups; merge them in so ranges
	// wider than the retention window stay correct.
	rollups, err := s.St.RollupsInRange(r.Context(), tok.UserID,
		from.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "summary failed")
		return
	}
	for _, ru := range rollups {
		totalActive += ru.ActiveSeconds
		totalPages += ru.Pages
		progDelta += ru.ProgDelta
		sessionCount += int(ru.SessionCount)
		if ru.ActiveSeconds > 0 {
			daySet[ru.Day] = true
		}
	}
	streak := streakDays(daySet, loc, now)

	speed := 0.0
	if totalActive > 0 {
		speed = progDelta / totalActive // progression fraction per second
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range_days":           days,
		"total_active_minutes": totalActive / 60,
		"total_pages":          totalPages,
		"sessions":             sessionCount,
		"streak_days":          streak,
		"speed_prog_per_hour":  speed * 3600,
	})
}

// HandleInsightsWork implements GET /v1/insights/works/{id}.
func (s *Server) HandleInsightsWork(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	if _, err := s.St.WorkByID(r.Context(), tok.UserID, workID); err != nil {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	sessions, err := s.St.CurrentSessionsForWork(r.Context(), tok.UserID, workID, 10_000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	var totalActive, totalPages, progDelta float64
	sessionCount := len(sessions)
	for _, ses := range sessions {
		totalActive += activeSeconds(ses)
		totalPages += s.pagesRead(r.Context(), ses)
		if d := ses.EndProg - ses.StartProg; d > 0 {
			progDelta += d
		}
	}
	// Aged sessions live as daily rollups.
	rollups, err := s.St.RollupsForWork(r.Context(), tok.UserID, workID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	for _, ru := range rollups {
		totalActive += ru.ActiveSeconds
		totalPages += ru.Pages
		progDelta += ru.ProgDelta
		sessionCount += int(ru.SessionCount)
	}
	// Current position: newest op.
	var currentProg float64
	var etaSeconds *float64
	if ops, err := s.St.Positions(r.Context(), tok.UserID, workID, 1); err == nil && len(ops) > 0 {
		currentProg = ops[0].Progression
		if totalActive > 0 && progDelta > 0 {
			speed := progDelta / totalActive
			remaining := 1 - currentProg
			if remaining > 0 && speed > 0 {
				eta := remaining / speed
				etaSeconds = &eta
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"work_id":              workID,
		"sessions":             sessionCount,
		"total_active_minutes": totalActive / 60,
		"total_pages":          totalPages,
		"current_progression":  currentProg,
		"eta_seconds":          etaSeconds,
	})
}

// HandleInsightsCalendar implements GET /v1/insights/calendar?year=2026 —
// daily minutes for heatmaps, days in the user's timezone.
func (s *Server) HandleInsightsCalendar(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	year := time.Now().Year()
	if y := r.URL.Query().Get("year"); y != "" {
		var v int
		if _, err := parseInt(y, &v); err == nil && v > 1970 && v < 3000 {
			year = v
		}
	}
	loc := s.userLocation(r, tok.UserID)
	from := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	to := from.AddDate(1, 0, 0)
	sessions, err := s.St.SessionsInRange(r.Context(), tok.UserID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "calendar failed")
		return
	}
	byDay := map[string]*dayStat{}
	for _, ses := range sessions {
		for _, part := range s.splitDaysFull(r.Context(), ses, loc) {
			d := byDay[part.date]
			if d == nil {
				d = &dayStat{Date: part.date}
				byDay[part.date] = d
			}
			d.Minutes += part.activeSec / 60
			d.Pages += part.pages
		}
	}
	rollups, err := s.St.RollupsInRange(r.Context(), tok.UserID,
		from.Format("2006-01-02"), to.AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "calendar failed")
		return
	}
	for _, ru := range rollups {
		d := byDay[ru.Day]
		if d == nil {
			d = &dayStat{Date: ru.Day}
			byDay[ru.Day] = d
		}
		d.Minutes += ru.ActiveSeconds / 60
		d.Pages += ru.Pages
	}
	out := []dayStat{}
	for _, d := range byDay {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	writeJSON(w, http.StatusOK, map[string]any{"year": year, "days": out})
}

type dayPart struct {
	date      string
	activeSec float64
	pages     float64
}

// splitDays splits a session into tz-local day parts. Pages attribute
// pro-rata by active time share (approximation, honest about it).
func (s *Server) splitDaysFull(ctx context.Context, ses store.Session, loc *time.Location) []dayPart {
	parts := splitDays(ses, loc)
	total := 0.0
	for _, p := range parts {
		total += p.activeSec
	}
	pages := s.pagesRead(ctx, ses)
	for i := range parts {
		if total > 0 {
			parts[i].pages = pages * parts[i].activeSec / total
		}
	}
	return parts
}

// splitDays divides a session's active seconds across the tz-local days
// it spans (idle excluded pro-rata is overkill; active = whole segment
// minus idle attributed to the segment containing the session end).
func splitDays(ses store.Session, loc *time.Location) []dayPart {
	var parts []dayPart
	start := ses.StartedAt.In(loc)
	end := ses.EndedAt.In(loc)
	idleSec := float64(ses.IdleMs) / 1000
	totalDur := ses.EndedAt.Sub(ses.StartedAt).Seconds()
	if totalDur <= 0 {
		return nil
	}
	for dayStart := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc); dayStart.Before(end); dayStart = dayStart.AddDate(0, 0, 1) {
		dayEnd := dayStart.AddDate(0, 0, 1)
		segStart := maxTime(start, dayStart)
		segEnd := minTime(end, dayEnd)
		if !segEnd.After(segStart) {
			continue
		}
		parts = append(parts, dayPart{
			date:      dayStart.Format("2006-01-02"),
			activeSec: segEnd.Sub(segStart).Seconds(),
		})
	}
	// Subtract idle from the end backwards. A pause can exceed the
	// final day's segment when a session crosses midnight.
	for i := len(parts) - 1; i >= 0 && idleSec > 0; i-- {
		if idleSec >= parts[i].activeSec {
			idleSec -= parts[i].activeSec
			parts[i].activeSec = 0
			continue
		}
		parts[i].activeSec -= idleSec
		idleSec = 0
	}
	return parts
}

// streakDays counts consecutive days (ending today or yesterday) with
// activity.
func streakDays(daySet map[string]bool, loc *time.Location, now time.Time) int {
	streak := 0
	day := now.In(loc)
	// Allow the streak to start today or yesterday.
	if !daySet[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
		if !daySet[day.Format("2006-01-02")] {
			return 0
		}
	}
	for daySet[day.Format("2006-01-02")] {
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}

func parseRangeDays(s string, def int) int {
	if len(s) >= 2 && s[len(s)-1] == 'd' {
		var v int
		if _, err := parseInt(s[:len(s)-1], &v); err == nil && v > 0 && v <= 3660 {
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

var errBadInt = errString2("bad int")

type errString2 string

func (e errString2) Error() string { return string(e) }

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
