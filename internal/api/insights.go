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

type workInsight struct {
	WorkID             string     `json:"work_id"`
	Sessions           int        `json:"sessions"`
	TotalActiveMinutes float64    `json:"total_active_minutes"`
	TotalPages         float64    `json:"total_pages"`
	CurrentProgression float64    `json:"current_progression"`
	ETASeconds         *float64   `json:"eta_seconds"`
	LastReadAt         *time.Time `json:"last_read_at"`
}

// window is the span an insight covers, always a whole number of days
// in the user's timezone.
//
// Whole days rather than a rolling count of hours, because that is what
// the question means: a reader asking for their last seven days at nine
// in the evening means the same seven dates they would have meant at
// dawn, not the 168 hours ending now — which would reach back into an
// eighth day and count part of it.
//
// A zero window is unbounded: "everything on record", which is what a
// caller that names no range has always meant and must keep meaning.
type window struct {
	// from is the first instant counted, to the first instant after.
	from time.Time
	to   time.Time
	// fromDay and toDay are the same bounds as tz-local dates, both
	// inclusive, and are empty exactly when the window is unbounded.
	fromDay string
	toDay   string
}

func (w window) unbounded() bool { return w.fromDay == "" }

// holdsSession reports whether a session ended inside the window. The
// end is what places a session in a range, so that a stretch of reading
// counts once, on the day it was finished, rather than being smeared
// across a boundary it happened to straddle.
func (w window) holdsSession(ses store.Session) bool {
	if w.unbounded() {
		return true
	}
	return !ses.EndedAt.Before(w.from) && ses.EndedAt.Before(w.to)
}

// holdsDay reports whether a rollup's tz-local day falls in the window.
// Compared as strings because a rollup's day was fixed in the timezone
// in force when it was rolled up, and re-parsing it in today's timezone
// would move reading between days retroactively.
func (w window) holdsDay(day string) bool {
	if w.unbounded() {
		return true
	}
	return day >= w.fromDay && day <= w.toDay
}

// days counts the calendar days the window covers, 0 when unbounded.
//
// Counted from the day strings in UTC rather than by dividing the
// instants, because a span containing a daylight-saving change is not a
// whole number of twenty-four hour periods and would come out short.
func (w window) days() int {
	if w.unbounded() {
		return 0
	}
	from, errFrom := time.Parse("2006-01-02", w.fromDay)
	to, errTo := time.Parse("2006-01-02", w.toDay)
	if errFrom != nil || errTo != nil {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}

// sessionBounds are the instants to query raw sessions between. An
// unbounded window reaches back to the epoch, which predates every
// session any store can hold, and forward to the end of today in the
// user's timezone — the same far end a bounded window ending today has,
// so that a lifetime total can never come out below a ranged one when a
// device with a fast clock files a session a moment into the future.
func (w window) sessionBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	if w.unbounded() {
		today := now.In(loc)
		endOfToday := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
		return time.Unix(0, 0), endOfToday
	}
	return w.from, w.to
}

// dayBounds are the inclusive tz-local dates to query rollups between.
func (w window) dayBounds(now time.Time, loc *time.Location) (string, string) {
	if w.unbounded() {
		return epochDay, now.In(loc).Format("2006-01-02")
	}
	return w.fromDay, w.toDay
}

// describe adds the window to a response so a client can tell what was
// actually counted from what it asked for. `range_days` is always
// written — nought for an unbounded window — because "everything on
// record" needs checking as much as any other span does: a server too
// old to know the word answers with a horizon of its own and nothing in
// the totals says which it was.
//
// This is what makes a new client safe against an old server. The
// aggregates themselves look identical either way, so without an echo a
// client cannot tell a range that was honoured from one that was
// silently ignored — and would label a lifetime total as a fortnight.
func (w window) describe(answer map[string]any) {
	answer["range_days"] = w.days()
	if w.unbounded() {
		return
	}
	answer["from"] = w.fromDay
	answer["to"] = w.toDay
}

// dayWindow builds the window covering firstDay through lastDay
// inclusive, in loc.
func dayWindow(firstDay, lastDay time.Time, loc *time.Location) window {
	from := time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, loc)
	last := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 0, 0, 0, 0, loc)
	return window{
		from:    from,
		to:      last.AddDate(0, 0, 1),
		fromDay: from.Format("2006-01-02"),
		toDay:   last.Format("2006-01-02"),
	}
}

// epochDay is older than any session or rollup a store can hold, and is
// what an unbounded window uses where a query insists on a lower bound.
const epochDay = "1970-01-01"

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

// activeDays returns the set of tz-local days with reading on them,
// across all of history rather than a requested window.
//
// The set the caller already built for its own range is reused as a
// starting point, and an unbounded range has nothing left to add, so
// the extra queries are skipped. Raw sessions only survive the
// retention horizon, so the long tail here is rollups, which are one
// row per work per day and cheap to walk.
func (s *Server) activeDays(
	ctx context.Context,
	userID string,
	loc *time.Location,
	now time.Time,
	known map[string]bool,
	win window,
) (map[string]bool, error) {
	if win.unbounded() {
		return known, nil
	}
	all := make(map[string]bool, len(known))
	for day := range known {
		all[day] = true
	}
	from := now.AddDate(0, 0, -streakLookbackDays)
	sessions, err := s.St.SessionsInRange(ctx, userID, from, now)
	if err != nil {
		return nil, err
	}
	for _, ses := range sessions {
		for _, day := range splitDays(ses, loc) {
			all[day.date] = true
		}
	}
	rollups, err := s.St.RollupsInRange(ctx, userID,
		from.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	for _, ru := range rollups {
		if ru.ActiveSeconds > 0 {
			all[ru.Day] = true
		}
	}
	return all, nil
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

// HandleInsightsSummary implements GET /v1/insights/summary. The span
// is either `from=2026-01-01&to=2026-03-31` or the older `range=30d`,
// and defaults to the last thirty days when neither is given.
func (s *Server) HandleInsightsSummary(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	now := time.Now()
	loc := s.userLocation(r, tok.UserID)
	win := insightsWindow(r, loc, now, defaultSummaryRange)
	from, to := win.sessionBounds(now, loc)
	sessions, err := s.St.SessionsInRange(r.Context(), tok.UserID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "summary failed")
		return
	}

	var totalActive float64
	var totalPages float64
	sessionCount := 0
	daySet := map[string]bool{}
	// Rolling speed: progression delta per active second over range.
	var progDelta float64
	for _, ses := range sessions {
		// A query by overlap returns a session that began before the
		// window and ran into it. Where it counts is settled the same
		// way here as for one book — by where it ended — so that the
		// headline and the rows beneath it cannot disagree about a
		// stretch of reading that straddles the first morning.
		if !win.holdsSession(ses) {
			continue
		}
		sessionCount++
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
	fromDay, toDay := win.dayBounds(now, loc)
	rollups, err := s.St.RollupsInRange(r.Context(), tok.UserID, fromDay, toDay)
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
	// A streak is a fact about the reader, not about the window they
	// asked to look through. Counted from every day on record, so that
	// asking for the last week cannot report a hundred-day run as seven.
	streakSet, err := s.activeDays(r.Context(), tok.UserID, loc, now, daySet, win)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "summary failed")
		return
	}
	streak := streakDays(streakSet, loc, now)

	speed := 0.0
	if totalActive > 0 {
		speed = progDelta / totalActive // progression fraction per second
	}
	answer := map[string]any{
		"total_active_minutes": totalActive / 60,
		"total_pages":          totalPages,
		"sessions":             sessionCount,
		"streak_days":          streak,
		"speed_prog_per_hour":  speed * 3600,
	}
	win.describe(answer)
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) workInsight(ctx context.Context, userID, workID string, loc *time.Location, win window) (workInsight, error) {
	sessions, err := s.St.CurrentSessionsForWork(ctx, userID, workID, sessionsPerWork)
	if err != nil {
		return workInsight{}, err
	}
	return s.workInsightFrom(ctx, userID, workID, loc, win, sessions)
}

// workInsightFrom builds one work's aggregate from sessions already in
// hand.
//
// The collection endpoint fetches the whole window once and groups it,
// rather than asking per work: that is one query instead of one per
// book, and it is not subject to the row limit a per-work fetch needs —
// which, applied newest-first to a narrow window deep in the past,
// could otherwise drop the very sessions being asked about.
func (s *Server) workInsightFrom(
	ctx context.Context,
	userID, workID string,
	loc *time.Location,
	win window,
	sessions []store.Session,
) (workInsight, error) {
	var totalActive, totalPages, progDelta float64
	sessionCount := 0
	var lastReadAt *time.Time
	for _, ses := range sessions {
		if !win.holdsSession(ses) {
			continue
		}
		sessionCount++
		totalActive += activeSeconds(ses)
		totalPages += s.pagesRead(ctx, ses)
		if d := ses.EndProg - ses.StartProg; d > 0 {
			progDelta += d
		}
		if lastReadAt == nil || ses.EndedAt.After(*lastReadAt) {
			ended := ses.EndedAt
			lastReadAt = &ended
		}
	}
	// Aged sessions live as daily rollups.
	rollups, err := s.St.RollupsForWork(ctx, userID, workID)
	if err != nil {
		return workInsight{}, err
	}
	for _, ru := range rollups {
		if !win.holdsDay(ru.Day) {
			continue
		}
		totalActive += ru.ActiveSeconds
		totalPages += ru.Pages
		progDelta += ru.ProgDelta
		sessionCount += int(ru.SessionCount)
		if day, err := time.ParseInLocation("2006-01-02", ru.Day, loc); err == nil &&
			(lastReadAt == nil || day.After(*lastReadAt)) {
			lastReadAt = &day
		}
	}
	// Current position: newest op. Never windowed — where the reader is
	// in a book is true now, whatever span the totals beside it cover,
	// and an estimate of what is left must be measured from there.
	var currentProg float64
	var etaSeconds *float64
	if ops, err := s.St.Positions(ctx, userID, workID, 1); err == nil && len(ops) > 0 {
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
	return workInsight{
		WorkID:             workID,
		Sessions:           sessionCount,
		TotalActiveMinutes: totalActive / 60,
		TotalPages:         totalPages,
		CurrentProgression: currentProg,
		ETASeconds:         etaSeconds,
		LastReadAt:         lastReadAt,
	}, nil
}

// insightsWindow resolves the span an insights request asks for.
//
// `from=2026-01-01&to=2026-03-31` names whole days in the user's own
// timezone, both included. `range=Nd` is the older spelling and means
// the last N calendar days ending today — calendar days, so that a
// request made at nine in the evening covers the same dates as one made
// at dawn, which is what a reader means and what their own device
// counts locally. `range=all` means everything on record, as does an
// absent parameter where the endpoint has no default.
//
// A range that cannot be read falls back to the endpoint's default
// rather than to everything: a typo asking for a fortnight should not
// quietly become the most expensive query the endpoint can run, and the
// echo would report a span the client never asked for either way.
func insightsWindow(r *http.Request, loc *time.Location, now time.Time, def string) window {
	q := r.URL.Query()
	rawFrom, rawTo := q.Get("from"), q.Get("to")
	if rawFrom != "" && rawTo != "" {
		from, errFrom := time.ParseInLocation("2006-01-02", rawFrom, loc)
		to, errTo := time.ParseInLocation("2006-01-02", rawTo, loc)
		if errFrom == nil && errTo == nil && !to.Before(from) {
			return dayWindow(from, to, loc)
		}
	}
	raw := q.Get("range")
	if raw == "" {
		raw = def
	}
	if raw == "" || raw == unboundedRange {
		return window{}
	}
	days := parseRangeDays(raw, parseRangeDays(def, 0))
	if days <= 0 {
		return window{}
	}
	today := now.In(loc)
	return dayWindow(today.AddDate(0, 0, -(days-1)), today, loc)
}

// HandleInsightsWorks implements GET /v1/insights/works. It returns one
// aggregate per work with reading history, so a client can render its
// per-book dashboard without issuing one request for every catalog item.
//
// An optional span narrows every aggregate to the same window the
// summary uses, so that a dashboard's headline and its per-book rows
// add up to each other instead of describing two different spans. It is
// echoed back for the same reason the calendar's is: a client must be
// able to tell a narrowed answer from a lifetime one, because the two
// are indistinguishable from the aggregates alone.
func (s *Server) HandleInsightsWorks(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workIDs, err := s.St.WorkIDsWithInsights(r.Context(), tok.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	loc := s.userLocation(r, tok.UserID)
	now := time.Now()
	win := insightsWindow(r, loc, now, "")
	from, to := win.sessionBounds(now, loc)
	sessions, err := s.St.SessionsInRange(r.Context(), tok.UserID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	byWork := map[string][]store.Session{}
	for _, ses := range sessions {
		byWork[ses.WorkID] = append(byWork[ses.WorkID], ses)
	}
	works := make([]workInsight, 0, len(workIDs))
	for _, workID := range workIDs {
		insight, err := s.workInsightFrom(
			r.Context(), tok.UserID, workID, loc, win, byWork[workID])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "work insights failed")
			return
		}
		if insight.Sessions > 0 || insight.TotalActiveMinutes > 0 {
			works = append(works, insight)
		}
	}
	sort.Slice(works, func(i, j int) bool {
		return works[i].TotalActiveMinutes > works[j].TotalActiveMinutes
	})
	answer := map[string]any{"works": works}
	win.describe(answer)
	writeJSON(w, http.StatusOK, answer)
}

// workInsightAnswer is one work's aggregate with the span it covers.
// The embedded type's fields are promoted, so this stays the flat object
// it has always been rather than growing a nested envelope.
type workInsightAnswer struct {
	workInsight
	RangeDays int    `json:"range_days"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
}

// HandleInsightsWork implements GET /v1/insights/works/{id}.
func (s *Server) HandleInsightsWork(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	if _, err := s.St.WorkByID(r.Context(), tok.UserID, workID); err != nil {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	loc := s.userLocation(r, tok.UserID)
	win := insightsWindow(r, loc, time.Now(), "")
	insight, err := s.workInsight(r.Context(), tok.UserID, workID, loc, win)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	writeJSON(w, http.StatusOK, workInsightAnswer{
		workInsight: insight,
		RangeDays:   win.days(),
		From:        win.fromDay,
		To:          win.toDay,
	})
}

// HandleInsightsCalendar implements GET /v1/insights/calendar — daily
// minutes for heatmaps, days in the user's timezone.
//
// Either `year=2026` or `from=2025-04-01&to=2026-03-31`. The bounded
// form exists so a rolling window costs one request rather than one per
// calendar year it happens to straddle, and both bounds are echoed back
// so a client can tell a server that understood them from one that
// ignored them and answered with this year.
func (s *Server) HandleInsightsCalendar(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	loc := s.userLocation(r, tok.UserID)
	win, bounded, ok := calendarBounds(r, loc)
	if !ok {
		writeError(w, http.StatusBadRequest, "calendar span too long")
		return
	}
	sessions, err := s.St.SessionsInRange(r.Context(), tok.UserID, win.from, win.to)
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
	rollups, err := s.St.RollupsInRange(r.Context(), tok.UserID, win.fromDay, win.toDay)
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
	answer := map[string]any{"year": win.from.In(loc).Year(), "days": out}
	if bounded {
		win.describe(answer)
	}
	writeJSON(w, http.StatusOK, answer)
}

// calendarBounds resolves the requested span. It reports whether an
// explicit from/to pair was honoured, which is what the response echo is
// derived from: a client must not be able to mistake an ignored
// parameter for an obeyed one. A span longer than any reader can have
// is refused rather than served, so that one authenticated request
// cannot ask the store to walk an unbounded history.
func calendarBounds(r *http.Request, loc *time.Location) (window, bool, bool) {
	q := r.URL.Query()
	rawFrom, rawTo := q.Get("from"), q.Get("to")
	if rawFrom != "" && rawTo != "" {
		from, errFrom := time.ParseInLocation("2006-01-02", rawFrom, loc)
		to, errTo := time.ParseInLocation("2006-01-02", rawTo, loc)
		if errFrom == nil && errTo == nil && !to.Before(from) {
			win := dayWindow(from, to, loc)
			if win.days() > maxCalendarDays {
				return window{}, true, false
			}
			return win, true, true
		}
	}
	year := time.Now().In(loc).Year()
	if y := q.Get("year"); y != "" {
		var v int
		if _, err := parseInt(y, &v); err == nil && v > 1970 && v < 3000 {
			year = v
		}
	}
	first := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	return dayWindow(first, first.AddDate(1, 0, -1), loc), false, true
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

// maxRangeDays is as long a span as `range=Nd` may name: ten years,
// which is longer than this software has existed. It is not a limit on
// how far back an insight can reach — `range=all` and an absent range
// are unbounded and always were, and rollups are kept indefinitely.
const maxRangeDays = 3660

// streakLookbackDays is how far back a streak is searched for. A run of
// consecutive days is broken by the first day without reading on it, so
// looking further back than any reader has been syncing cannot lengthen
// one, and the query is bounded rather than open.
const streakLookbackDays = maxRangeDays

// sessionsPerWork bounds a single work's raw session fetch. Raw
// sessions are reduced to daily rollups past the retention horizon, so
// this is far more sittings than one book can hold; the collection
// endpoint does not use it at all, fetching the window once and
// grouping it instead.
const sessionsPerWork = 10_000

// maxCalendarDays is the longest calendar a single request may ask for.
// Comfortably more than a client showing a year at a time needs, and
// short of asking the store to walk an entire history per request.
const maxCalendarDays = 4000

// defaultSummaryRange is what the summary counts when a caller names
// no span at all, kept from before ranges were selectable.
const defaultSummaryRange = "30d"

// unboundedRange is how a caller spells "everything on record" rather
// than encoding it as a magic number of days nobody could read back.
const unboundedRange = "all"

// parseRangeDays reads a `range=Nd` parameter, returning def for
// anything else — including `all`, which names no number of days and is
// resolved to an unbounded window by insightsWindow instead.
func parseRangeDays(s string, def int) int {
	if len(s) >= 2 && s[len(s)-1] == 'd' {
		var v int
		if _, err := parseInt(s[:len(s)-1], &v); err == nil && v > 0 && v <= maxRangeDays {
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
