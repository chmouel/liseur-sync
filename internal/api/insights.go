package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
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

// workInsight is one work's aggregate.
//
// Title and Author are here so that a client can name a book it has
// never seen. A reader's dashboard lists what it can put a local file
// against, and a work read only on another device falls out of that
// list while still counting towards the total above it — a list that is
// smaller than its own headline and gives no reason. The name is the
// only thing needed to show the row; both are omitted when the server
// has nothing to say rather than sent empty.
type workInsight struct {
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
//
// It lives here rather than in the insights package because it is the
// shape of a JSON answer, not a fact about a span.
func describe(w insights.Window, answer map[string]any) {
	answer["range_days"] = w.Days()
	if w.Unbounded() {
		return
	}
	answer["from"] = w.FromDay()
	answer["to"] = w.ToDay()
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
	win insights.Window,
) (map[string]bool, error) {
	if win.Unbounded() {
		return known, nil
	}
	all := make(map[string]bool, len(known))
	for day := range known {
		all[day] = true
	}
	from := now.AddDate(0, 0, -insights.StreakLookbackDays)
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
	win := requestWindow(r, loc, now, insights.DefaultSummaryRange)
	from, to := win.SessionBounds(now, loc)
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
		if !win.HoldsSession(ses) {
			continue
		}
		sessionCount++
		a := insights.ActiveSeconds(ses)
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
	fromDay, toDay := win.DayBounds(now, loc)
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
	streak := insights.StreakDays(streakSet, loc, now)

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
	describe(win, answer)
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) workInsight(ctx context.Context, userID, workID string, loc *time.Location, win insights.Window) (workInsight, error) {
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
	win insights.Window,
	sessions []store.Session,
) (workInsight, error) {
	var totalActive, totalPages, progDelta float64
	sessionCount := 0
	var lastReadAt *time.Time
	for _, ses := range sessions {
		if !win.HoldsSession(ses) {
			continue
		}
		sessionCount++
		totalActive += insights.ActiveSeconds(ses)
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
		if !win.HoldsDay(ru.Day) {
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
	// The name, when the server has one. A missing work is not an error
	// here: the aggregate is built from sessions and rollups that name
	// the work themselves, so a record that has gone stays a row with
	// figures on it and no title, rather than failing the whole request.
	// Any other failure is a real store problem and must propagate,
	// rather than being swallowed into the same silent blank.
	title, author := "", ""
	work, err := s.St.WorkByID(ctx, userID, workID)
	switch {
	case err == nil:
		title, author = work.Title, work.Author
	case errors.Is(err, store.ErrNotFound):
		// Nothing to do: title and author stay empty.
	default:
		return workInsight{}, err
	}
	return workInsight{
		WorkID:             workID,
		Title:              title,
		Author:             author,
		Sessions:           sessionCount,
		TotalActiveMinutes: totalActive / 60,
		TotalPages:         totalPages,
		CurrentProgression: currentProg,
		ETASeconds:         etaSeconds,
		LastReadAt:         lastReadAt,
	}, nil
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
	win := requestWindow(r, loc, now, "")
	from, to := win.SessionBounds(now, loc)
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
	describe(win, answer)
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
	win := requestWindow(r, loc, time.Now(), "")
	insight, err := s.workInsight(r.Context(), tok.UserID, workID, loc, win)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "work insights failed")
		return
	}
	writeJSON(w, http.StatusOK, workInsightAnswer{
		workInsight: insight,
		RangeDays:   win.Days(),
		From:        win.FromDay(),
		To:          win.ToDay(),
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
	from, to := win.SessionBounds(time.Now(), loc)
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
	rollups, err := s.St.RollupsInRange(r.Context(), tok.UserID, win.FromDay(), win.ToDay())
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
	answer := map[string]any{"year": calendarYear(win, loc), "days": out}
	if bounded {
		describe(win, answer)
	}
	writeJSON(w, http.StatusOK, answer)
}

// calendarBounds resolves the requested span. It reports whether an
// explicit from/to pair was honoured, which is what the response echo is
// derived from: a client must not be able to mistake an ignored
// parameter for an obeyed one. A span longer than any reader can have
// is refused rather than served, so that one authenticated request
// cannot ask the store to walk an unbounded history.
func calendarBounds(r *http.Request, loc *time.Location) (insights.Window, bool, bool) {
	q := r.URL.Query()
	rawFrom, rawTo := q.Get("from"), q.Get("to")
	if rawFrom != "" && rawTo != "" {
		from, errFrom := time.ParseInLocation("2006-01-02", rawFrom, loc)
		to, errTo := time.ParseInLocation("2006-01-02", rawTo, loc)
		if errFrom == nil && errTo == nil && !to.Before(from) {
			win := insights.DayWindow(from, to, loc)
			if win.Days() > maxCalendarDays {
				return insights.Window{}, true, false
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
	return insights.DayWindow(first, first.AddDate(1, 0, -1), loc), false, true
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

// requestWindow resolves the span an insights request asks for, from
// the parameters this API spells them with. The resolving itself lives
// in the insights package, so that the web UI — which spells them
// differently and never sees an *http.Request — reaches the same
// answer.
func requestWindow(r *http.Request, loc *time.Location, now time.Time, def string) insights.Window {
	q := r.URL.Query()
	return insights.ParseWindow(q.Get("from"), q.Get("to"), q.Get("range"), def, loc, now)
}

// calendarYear is the year the calendar's span opens in, which is the
// only thing the `year` field in its answer ever meant.
func calendarYear(win insights.Window, loc *time.Location) int {
	day, err := time.ParseInLocation(insights.DayFormat, win.FromDay(), loc)
	if err != nil {
		return time.Now().In(loc).Year()
	}
	return day.Year()
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
