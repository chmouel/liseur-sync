package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

// --- dashboard ---

// handleDashboard draws the reading dashboard over the span the reader
// picked.
//
// One span, one window, one read. The two separate reads this replaced —
// thirty days for the numbers and year-to-date for the heatmap — could
// describe different stretches of time on the same screen, and neither
// of them was the stretch the reader had asked about, because there was
// no way to ask.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	now := time.Now()
	loc := userLoc(u)
	span := dashboardSpan(w, r)
	win := span.Window(now, loc)

	from, to := win.SessionBounds(now, loc)
	stored, err := s.St.SessionsInRange(r.Context(), u.ID, from, to)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	// SessionsInRange asks for an overlap, so it also answers with a
	// sitting that began inside the span and ran out the far end of
	// it. The window decides membership by the day a sitting ended,
	// and it has to decide it here too or the totals count reading
	// that has not happened yet.
	sessions := make([]store.Session, 0, len(stored))
	for _, ses := range stored {
		if win.HoldsSession(ses) {
			sessions = append(sessions, ses)
		}
	}
	works, _ := s.St.ListWorks(r.Context(), u.ID)
	titles := map[string]string{}
	for _, ws := range works {
		titles[ws.Work.ID] = orPlaceholder(ws.Work.Title)
	}

	sum := SummaryData{
		Span: span, RangeDays: win.Days(),
		Bars: span.SuitsDailyBars(now, loc),
	}
	dayMin := map[string]float64{}
	pages := newPageCounter(s.St)
	for _, ses := range sessions {
		active := insights.ActiveSeconds(ses)
		sum.ActiveMinutes += active / 60
		sum.Sessions++
		sum.Pages += pages.of(r.Context(), ses)
		// By the day it ended, which is where the app puts it and
		// where the window draws its own edges. A sitting read across
		// midnight belongs to one day, and the reader should find it
		// on the same one wherever they look.
		//
		// A rollup is the exception, and knowingly: the materializer
		// divides a crossing sitting between the days it touched, so
		// once retention compacts that evening its minutes move off
		// the end day. Making the two agree means changing how
		// rollups are allocated, which is a change to stored data and
		// to what the API answers, not to this page.
		dayMin[ses.EndedAt.In(loc).Format(insights.DayFormat)] += active / 60
	}
	fromDay, toDay := win.DayBounds(now, loc)
	if rollups, err := s.St.RollupsInRange(r.Context(), u.ID, fromDay, toDay); err == nil {
		for _, ru := range rollups {
			sum.ActiveMinutes += ru.ActiveSeconds / 60
			sum.Sessions += int(ru.SessionCount)
			sum.Pages += ru.Pages
			dayMin[ru.Day] += ru.ActiveSeconds / 60
		}
	}
	// The streak is answered from further back than the span on
	// purpose: asking about the last week must not report a
	// months-long run as seven days.
	sum.StreakDays = s.streakFor(r, u.ID, loc, now, win, dayMin)

	links := s.workBookIDs(r.Context(), u.ID, works)
	labels := deviceLabels(r.Context(), s.St, u.ID)
	dashboard(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a),
		sum,
		daySeries(win, dayMin, now, loc),
		recentSessions(sessions, titles, loc, labels),
		s.markReadable(r, u.ID, continueReading(works, links.active, loc))).
		Render(r.Context(), w)
}

// daySeries is every day in the window, the empty ones included.
//
// A fortnight with two gaps in it is the information; a chart that
// silently skipped them would read as an unbroken run.
func daySeries(win insights.Window, dayMin map[string]float64, now time.Time, loc *time.Location) []DayCell {
	// The days with nothing on them are what an unbounded span starts
	// after, not what it starts at: a sitting that was all pause, or a
	// rollup whose active time came to nothing, leaves a key behind
	// without leaving any reading behind.
	earliest := ""
	for day, minutes := range dayMin {
		if minutes <= 0 {
			continue
		}
		if earliest == "" || day < earliest {
			earliest = day
		}
	}
	var cells []DayCell
	win.EachDay(earliest, now, loc, func(day string) {
		cells = append(cells, DayCell{Date: day, Minutes: dayMin[day]})
	})
	return cells
}

// recentSessionLimit is how many sittings the table names one by one.
// It is a glance at what has just been read, not a log; the log is the
// per-work page.
const recentSessionLimit = 10

// recentSessions is the newest sittings in the window, newest first.
func recentSessions(sessions []store.Session, titles map[string]string, loc *time.Location, labels map[string]string) []SessionRow {
	rows := make([]SessionRow, 0, recentSessionLimit)
	for i := len(sessions) - 1; i >= 0 && len(rows) < recentSessionLimit; i-- {
		ses := sessions[i]
		rows = append(rows, SessionRow{
			When:          ses.EndedAt.In(loc).Format("Jan 2 15:04"),
			WorkID:        ses.WorkID,
			WorkTitle:     titles[ses.WorkID],
			DeviceID:      ses.DeviceID,
			DeviceName:    labels[ses.DeviceID],
			DeviceIDShort: compactDeviceID(ses.DeviceID),
			Minutes:       int(insights.ActiveSeconds(ses) / 60),
			StartProg:     ses.StartProg,
			EndProg:       ses.EndProg,
		})
	}
	return rows
}

// streakFor counts the reader's current run of days.
//
// It looks back over the whole lookback rather than over the span,
// because a streak is a fact about the reader and not about the
// question: narrowing it to the chosen span would report a
// months-long run as seven days to anybody looking at their week.
// The days already counted for the span are passed in so the common
// case — a span that reaches back further than the run — needs no
// second read.
func (s *Server) streakFor(
	r *http.Request, userID string, loc *time.Location, now time.Time,
	win insights.Window, known map[string]float64,
) int {
	days := make(map[string]bool, len(known))
	for day, minutes := range known {
		if minutes > 0 {
			days[day] = true
		}
	}
	// An unbounded span has already read everything there is, so the
	// lookback would be the same two queries over the same rows.
	if win.Unbounded() {
		return insights.StreakDays(days, loc, now)
	}
	from := now.AddDate(0, 0, -insights.StreakLookbackDays)
	if sessions, err := s.St.SessionsInRange(r.Context(), userID, from, now); err == nil {
		for _, ses := range sessions {
			if insights.ActiveSeconds(ses) > 0 {
				days[ses.EndedAt.In(loc).Format(insights.DayFormat)] = true
			}
		}
	}
	if rollups, err := s.St.RollupsInRange(r.Context(), userID,
		from.In(loc).Format(insights.DayFormat), now.In(loc).Format(insights.DayFormat)); err == nil {
		for _, ru := range rollups {
			if ru.ActiveSeconds > 0 {
				days[ru.Day] = true
			}
		}
	}
	return insights.StreakDays(days, loc, now)
}

// continueReadingLimit is a shelf, not a list: the point is to get back
// into the book you put down, and a wall of half-read books is a guilt
// trip rather than a shortcut.
const continueReadingLimit = 8

// continueReading is the works that are started and not finished, newest
// first. It reads nothing extra — the dashboard has already listed the
// works to name them in the session table.
func continueReading(works []store.WorkSummary, bookIDs map[string]string, loc *time.Location) []WorkRow {
	started := make([]store.WorkSummary, 0, len(works))
	for _, ws := range works {
		if ws.Progression == nil || *ws.Progression <= 0 || *ws.Progression >= 0.999 {
			continue
		}
		if ws.LastActive == nil {
			continue
		}
		started = append(started, ws)
	}
	sort.Slice(started, func(i, j int) bool {
		return started[i].LastActive.After(*started[j].LastActive)
	})
	if len(started) > continueReadingLimit {
		started = started[:continueReadingLimit]
	}
	rows := make([]WorkRow, 0, len(started))
	for _, ws := range started {
		rows = append(rows, WorkRow{
			ID: ws.Work.ID, BookID: bookIDs[ws.Work.ID], Title: ws.Work.Title, Author: ws.Work.Author,
			Progression: ws.Progression, Pending: ws.Pending,
			LastActive: ws.LastActive.In(loc).Format("Jan 2"),
		})
	}
	return rows
}

func userLoc(u *store.User) *time.Location {
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// --- works ---

// markReadable says which of these works' books the browser reader can
// open. The shelf it answers for is bounded (continueReadingLimit), so
// it is a handful of book lookups rather than the wall of them a whole
// catalog page would be.
func (s *Server) markReadable(r *http.Request, userID string, rows []WorkRow) []WorkRow {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BookID != "" {
			ids = append(ids, row.BookID)
		}
	}
	books := s.booksByID(r.Context(), userID, ids)
	for i := range rows {
		book, ok := books[rows[i].BookID]
		rows[i].CanRead = ok && bookReadable(book)
	}
	return rows
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	workID := r.PathValue("id")
	wk, err := s.St.WorkByID(r.Context(), u.ID, workID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sessions, _ := s.St.CurrentSessionsForWork(r.Context(), u.ID, workID, 10_000)
	var d WorkDetail
	d.Work = wk
	d.Sessions = len(sessions)
	var progDelta float64
	for _, ses := range sessions {
		d.Minutes += insights.ActiveSeconds(ses) / 60
		if delta := ses.EndProg - ses.StartProg; delta > 0 {
			progDelta += delta
			if ses.EditionSHA != nil {
				if ed, err := s.St.EditionBySHA(r.Context(), u.ID, *ses.EditionSHA); err == nil && ed.PageCount != nil {
					d.Pages += delta * float64(*ed.PageCount)
				}
			}
		}
	}
	// Once, not once per sitting. A work old enough for its early
	// sessions to have been compacted counted its rolled-up totals
	// again for every session it still holds in full, so the longer a
	// book had been read the more wrong this page was about it.
	if rollups, err := s.St.RollupsForWork(r.Context(), u.ID, workID); err == nil {
		for _, ru := range rollups {
			d.Sessions += int(ru.SessionCount)
			d.Minutes += ru.ActiveSeconds / 60
			d.Pages += ru.Pages
			progDelta += ru.ProgDelta
		}
	}
	ops, _ := s.St.Positions(r.Context(), u.ID, workID, 50)
	if len(ops) > 0 {
		d.CurrentProg = ops[0].Progression
		if progDelta > 0 && d.Minutes > 0 {
			speed := progDelta / (d.Minutes * 60)
			if remaining := 1 - d.CurrentProg; remaining > 0 && speed > 0 {
				eta := time.Duration(remaining/speed) * time.Second
				d.ETAHuman = humanDuration(eta)
			}
		}
	}
	loc := userLoc(u)
	// The work's own book, when it has one: it makes this page a way
	// back into the reading rather than only a report about it.
	if ids, err := s.St.WorkBookIDs(r.Context(), u.ID, workID); err == nil && len(ids) > 0 {
		d.BookID = ids[0]
	}
	if d.BookID != "" {
		if book, err := s.St.CatalogBookByID(r.Context(), u.ID, d.BookID); err == nil {
			d.CanRead = bookReadable(book)
		}
	}
	// Newest first, and only the sessions still held one by one — the
	// aged ones live on as the daily totals counted in the statistics
	// above, which is why this list can be shorter than that count.
	sessionRows := make([]SessionRow, 0, len(sessions))
	for i := len(sessions) - 1; i >= 0; i-- {
		ses := sessions[i]
		sessionRows = append(sessionRows, SessionRow{
			When:      ses.StartedAt.In(loc).Format("Jan 2 15:04"),
			WorkID:    workID,
			WorkTitle: wk.Title,
			DeviceID:  ses.DeviceID,
			Minutes:   int(insights.ActiveSeconds(ses) / 60),
			StartProg: ses.StartProg,
			EndProg:   ses.EndProg,
		})
	}
	var opRows []OpRow
	for _, o := range ops {
		row := OpRow{
			When:     o.ReceivedAt.In(loc).Format("Jan 2 15:04"),
			DeviceID: o.DeviceID, Origin: string(o.Origin),
			Progression: o.Progression,
		}
		if o.ForeignPos != nil {
			row.ForeignPos = *o.ForeignPos
		}
		opRows = append(opRows, row)
	}
	// The reader's own highlights and notes for this work (ADR-0028),
	// view-only: excerpts and bodies render as text, and the panel is
	// simply absent when there are none.
	var annRows []AnnotationRow
	if anns, err := s.St.WorkAnnotations(r.Context(), u.ID, workID); err == nil {
		for _, an := range anns {
			row := AnnotationRow{
				Kind:    string(an.Kind),
				Color:   an.Color,
				Excerpt: an.Excerpt,
				Body:    an.Body,
				When:    an.ClientTS.In(loc).Format("Jan 2 15:04"),
			}
			if an.Progression != nil {
				row.Where = fmt.Sprintf("%d%%", int(*an.Progression*100))
			}
			annRows = append(annRows, row)
		}
	}
	workPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), d, opRows, sessionRows, annRows).
		Render(r.Context(), w)
}

func humanDuration(d time.Duration) string {
	h := int(d.Hours())
	if h >= 1 {
		return fmt.Sprintf("~%dh", h)
	}
	return fmt.Sprintf("~%dm", int(d.Minutes()))
}

// --- devices ---

func (s *Server) renderDevices(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash) {
	if settingsRedirect(w, r, settingsDevices, "", "", flash) {
		return
	}
	s.renderSettings(w, r, a, u, settingsDevices, "", "", flash, false, "", false)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	scopes, err := formScopes(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Auth.CheckScopeGrant(r.Context(), u.ID, scopes); err != nil {
		if errors.Is(err, auth.ErrAdminGrantRequiresAdmin) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	secret, _, err := s.Auth.MintToken(r.Context(), u.ID, name, scopes, nil)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	s.renderDevices(w, r, a, u, Flash{Secret: secret, SecretLabel: "New token secret"})
}

func (s *Server) handleUpdateTokenScopes(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	scopes, err := formScopes(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Auth.CheckScopeGrant(r.Context(), u.ID, scopes); err != nil {
		if errors.Is(err, auth.ErrAdminGrantRequiresAdmin) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if err := s.St.UpdateTokenScopes(r.Context(), u.ID, r.PathValue("id"), scopes); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	s.renderDevices(w, r, a, u, Flash{Notice: "Token scopes updated."})
}

func formScopes(r *http.Request) (store.ScopeSet, error) {
	values := r.Form["scopes"]
	if len(values) == 0 {
		if legacy := r.FormValue("scope"); legacy != "" {
			values = []string{legacy}
		}
	}
	requested := make([]store.Scope, len(values))
	for i, value := range values {
		requested[i] = store.Scope(value)
	}
	return store.NormalizeScopes(requested)
}

func formatScopes(scopes store.ScopeSet) string {
	return scopes.String()
}

func hasScope(scopes store.ScopeSet, scope store.Scope) bool {
	return scopes.Contains(scope)
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = s.St.RevokeToken(r.Context(), u.ID, r.PathValue("id"))
	s.renderDevices(w, r, a, u, Flash{Notice: "Token revoked."})
}

// handleRevokeBrowsers ends browser reading everywhere, for the case
// this page exists to answer: a machine you no longer have, or no longer
// trust, that you were signed in on.
//
// It takes this browser down with the others rather than sparing it,
// because sparing it would mean deciding which credential is "this one"
// from a form post that carries no credential — and a button that
// mostly signs you out is worse than one that plainly does.
func (s *Server) handleRevokeBrowsers(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.Auth.RevokeReaderTokens(r.Context(), u.ID); err != nil {
		s.renderDevices(w, r, a, u, Flash{Error: "Could not sign browsers out."})
		return
	}
	s.renderDevices(w, r, a, u, Flash{
		Notice: "Browser reading signed out. Reopen a book to read here again.",
	})
}

func (s *Server) handlePairing(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	code, _ := auth.NewSecret()
	code = code[:32]
	id, _ := auth.NewSecret()
	_ = s.St.CreatePairingCode(r.Context(), store.PairingCode{
		ID: id, UserID: u.ID, CodeSHA256: auth.KosyncPairingHash(code),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	})
	s.renderDevices(w, r, a, u, Flash{Secret: code, SecretLabel: "kosync pairing code (15 min, single use)"})
}

func (s *Server) handleCreateKoplugin(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	capability, _ := auth.NewSecret()
	id, _ := auth.NewSecret()
	name := r.FormValue("name")
	_ = s.St.CreateKopluginDevice(r.Context(), store.KopluginDevice{
		ID: id, UserID: u.ID, TokenSHA256: auth.HashSecret(capability),
		Label: name, DeviceID: "koplugin:" + name, CreatedAt: time.Now(),
	})
	s.renderDevices(w, r, a, u, Flash{Secret: capability, SecretLabel: "koplugin capability URL token"})
}

func (s *Server) handleRevokeKoplugin(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = s.St.RevokeKopluginDevice(r.Context(), u.ID, r.PathValue("id"))
	s.renderDevices(w, r, a, u, Flash{Notice: "Capability revoked."})
}

func (s *Server) handleRevokeKosync(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = s.St.RevokeKosyncDevice(r.Context(), u.ID, r.PathValue("slot"))
	s.renderDevices(w, r, a, u, Flash{Notice: "kosync device revoked."})
}

// --- settings ---

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	section, view, userID := settingsSelection(r)
	s.renderSettings(w, r, a, u, section, view, userID, flashFromQuery(r), false, "", false)
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tz := r.FormValue("timezone")
	if _, err := time.LoadLocation(tz); err != nil {
		tz = "UTC"
	}
	kosyncOn := r.FormValue("kosync_enabled") == "on"
	kopluginOn := r.FormValue("koplugin_enabled") == "on"
	if err := s.St.UpdateUserSettings(r.Context(), u.ID, tz, kosyncOn, kopluginOn); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	u.Timezone = tz
	u.KosyncEnabled = kosyncOn
	u.KopluginEnabled = kopluginOn
	s.renderSettings(w, r, a, u, settingsProfile, "", "", Flash{}, true, "", false)
}

// handleChangePassword verifies the current password, then replaces the
// hash and revokes every other web session (the changing session stays
// live so the user isn't logged out mid-action).
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	render := func(msg string, isErr bool) {
		s.renderSettings(w, r, a, u, settingsProfile, "", "", Flash{}, false, msg, isErr)
	}
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cur, new1, new2 := r.FormValue("current"), r.FormValue("new"), r.FormValue("repeat")
	if new1 != new2 {
		render("New passwords do not match.", true)
		return
	}
	if len(new1) < 8 {
		render("Password must be at least 8 characters.", true)
		return
	}
	ok, err := auth.CheckPassword(cur, u.Argon2Hash)
	if err != nil || !ok {
		render("Current password is wrong.", true)
		return
	}
	hash, err := auth.HashPassword(new1)
	if err != nil {
		render("Internal error.", true)
		return
	}
	if err := s.St.SetUserPassword(r.Context(), u.ID, hash, a.ID); err != nil {
		render("Internal error.", true)
		return
	}
	render("Password changed.", false)
}

// commonZones keeps the picker usable; arbitrary IANA names are also
// accepted server-side.
var commonZones = []string{
	"UTC", "Europe/Paris", "Europe/London", "Europe/Berlin",
	"America/New_York", "America/Chicago", "America/Denver",
	"America/Los_Angeles", "America/Sao_Paulo", "Asia/Tokyo",
	"Asia/Shanghai", "Asia/Singapore", "Australia/Sydney",
	"Pacific/Auckland",
}

// --- admin ---

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, "create-invite", "", err)
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			s.renderAdminUsersInPlace(w, r, a, u, Flash{Error: err.Error()})
			return
		}
		s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	code, err := s.generateSecret()
	if err == nil && len(code) < 32 {
		err = errors.New("generated secret is too short")
	}
	if err != nil {
		logAdminAction(r, u, "create-invite", "", err)
		slog.ErrorContext(r.Context(), "create invite failed", "error", err)
		s.renderAdminUsers(w, r, a, u, Flash{Error: "Could not create invite."})
		return
	}
	code = code[:32]
	id, err := s.generateSecret()
	if err == nil {
		err = s.St.CreateInvite(r.Context(), store.Invite{
			ID: id, CodeSHA256: auth.HashSecret(code), CreatedBy: u.ID,
			ExpiresAt: time.Now().Add(inviteTTL),
		})
	}
	logAdminAction(r, u, "create-invite", id, err)
	if err != nil {
		slog.ErrorContext(r.Context(), "create invite failed", "error", err)
		s.renderAdminUsers(w, r, a, u, Flash{Error: "Could not create invite."})
		return
	}
	s.renderAdminUsers(w, r, a, u, Flash{
		Secret:      code,
		SecretLabel: "Invite code (7 days, single use)",
	})
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = s.St.RevokeInvite(r.Context(), u.ID, r.PathValue("id"))
	s.renderAdminUsers(w, r, a, u, Flash{Notice: "Invite revoked."})
}
