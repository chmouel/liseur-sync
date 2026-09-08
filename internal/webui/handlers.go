package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

// --- insights ---

// handleInsights draws the reading statistics over the span the reader
// picked.
//
// One span, one window, one read. The two separate reads this replaced —
// thirty days for the numbers and year-to-date for the heatmap — could
// describe different stretches of time on the same screen, and neither
// of them was the stretch the reader had asked about, because there was
// no way to ask.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	now := time.Now()
	snapshot, err := s.St.StatisticsSnapshot(r.Context(), u.ID, nil)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	loc := userLoc(&store.User{Timezone: snapshot.Timezone})
	span, chart := insightsView(w, r, now, loc)
	win := span.Window(now, loc)

	stats, err := insights.Build(snapshot, win, now)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	sessions := make([]store.Session, 0, len(snapshot.Sessions))
	for _, ses := range snapshot.Sessions {
		if win.HoldsSession(ses) {
			sessions = append(sessions, ses)
		}
	}
	titles := map[string]string{}
	for _, work := range snapshot.Works {
		titles[work.ID] = orPlaceholder(work.Title)
	}

	sum := SummaryData{
		Span: span, RangeDays: win.Days(),
		Bucket:        span.Bucket(),
		Chart:         chart,
		Calendar:      span.SuitsCalendar(now, loc),
		ActiveMinutes: stats.Summary.TotalActiveMinutes,
		Sessions:      stats.Summary.Sessions,
		Pages:         stats.Summary.TotalPages,
		StreakDays:    stats.Summary.StreakDays,
		SpeedPerHour:  stats.Summary.SpeedProgPerHour,
		BooksRead:     len(stats.Works),
		BooksFinished: booksFinished(stats.Works),
	}
	dayMin := map[string]float64{}
	for _, day := range stats.Days {
		dayMin[day.Date] = day.Minutes
	}

	labels := deviceLabels(r.Context(), s.St, u.ID)
	insightsPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a),
		sum,
		daySeries(win, dayMin, now, loc),
		recentSessions(sessions, titles, loc, labels),
		byBook(stats.Works)).
		Render(r.Context(), w)
}

// finishedProgression is where a progression stops being "reading". It
// is library.go's threshold, spelled here so the two cannot drift: a
// book the library calls finished must not be a book the statistics
// call half-read.
const finishedProgression = finished

// booksFinished counts the works read in this span that are now done.
//
// It is deliberately not "finished during this span": the server has no
// flag saying when a book crossed the line, only where each work stands
// now (an op at or past [finishedProgression], which is what the
// library's own "mark as read" writes). So this answers the question it
// can answer honestly — you read from these in this span, and these
// ones are finished — rather than inventing a date for the last page.
func booksFinished(works []insights.Work) int {
	n := 0
	for _, w := range works {
		if w.CurrentProgression >= finishedProgression {
			n++
		}
	}
	return n
}

// byBookLimit is how many books the breakdown names. The page is a
// glance at where the reading went, and there is no fuller statistics
// page to send anybody to; a list past ten is a report.
const byBookLimit = 10

// byBook is the works read in this span, most time first — which is the
// order insights.Build already leaves them in.
func byBook(works []insights.Work) []BookStatRow {
	rows := make([]BookStatRow, 0, min(len(works), byBookLimit))
	for _, w := range works {
		if len(rows) == byBookLimit {
			break
		}
		rows = append(rows, BookStatRow{
			WorkID:      w.WorkID,
			Title:       orPlaceholder(w.Title),
			Author:      w.Author,
			Duration:    readingMinutes(w.TotalActiveMinutes),
			Sittings:    sittings(w.Sessions),
			Progression: w.CurrentProgression,
			Finished:    w.CurrentProgression >= finishedProgression,
		})
	}
	return rows
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
		active := insights.ActiveSeconds(ses)
		rows = append(rows, SessionRow{
			When:          ses.EndedAt.In(loc).Format("Jan 2 15:04"),
			WorkID:        ses.WorkID,
			WorkTitle:     titles[ses.WorkID],
			DeviceID:      ses.DeviceID,
			DeviceName:    labels[ses.DeviceID],
			DeviceIDShort: compactDeviceID(ses.DeviceID),
			Minutes:       int(active / 60),
			Duration:      readingDuration(asDuration(active, time.Second)),
			StartProg:     ses.StartProg,
			EndProg:       ses.EndProg,
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

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	workID := r.PathValue("id")
	snapshot, err := s.St.StatisticsSnapshot(r.Context(), u.ID, nil)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	var wk store.Work
	for _, work := range snapshot.Works {
		if work.ID == workID {
			wk = work
			break
		}
	}
	if wk.ID == "" {
		http.NotFound(w, r)
		return
	}
	sessions := make([]store.Session, 0)
	for _, ses := range snapshot.Sessions {
		if ses.WorkID == workID {
			sessions = append(sessions, ses)
		}
	}
	stats, err := insights.Build(snapshot, insights.Window{}, time.Now())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	stat := stats.ByWork[workID]
	loc := userLoc(&store.User{Timezone: snapshot.Timezone})
	d := WorkDetail{
		Work: wk, Sessions: stat.Sessions,
		Duration: readingMinutes(stat.TotalActiveMinutes),
		Sittings: sittings(stat.Sessions),
		Pages:    stat.TotalPages, CurrentProg: stat.CurrentProgression,
	}
	if seconds := stat.ETASeconds; seconds != nil && *seconds <= float64((1<<63-1)/int64(time.Second)) {
		d.ETAHuman = readingDuration(asDuration(*seconds, time.Second))
	}
	if first, ok := firstReadingDay(snapshot, workID, loc); ok {
		d.Started = first.Format("2 Jan 2006")
		if stat.LastReadAt != nil {
			last := stat.LastReadAt.In(loc)
			d.LastRead = last.Format("2 Jan 2006")
			d.ReadingFor = readingSpanDays(first, last)
		}
	}
	ops, err := s.St.Positions(r.Context(), u.ID, workID, 50)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
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
	const sessionLogLimit = 10_000
	sessionRows := make([]SessionRow, 0, min(len(sessions), sessionLogLimit))
	for i := len(sessions) - 1; i >= 0 && len(sessionRows) < sessionLogLimit; i-- {
		ses := sessions[i]
		active := insights.ActiveSeconds(ses)
		sessionRows = append(sessionRows, SessionRow{
			When:      ses.StartedAt.In(loc).Format("Jan 2 15:04"),
			WorkID:    workID,
			WorkTitle: wk.Title,
			DeviceID:  ses.DeviceID,
			Minutes:   int(active / 60),
			Duration:  readingDuration(asDuration(active, time.Second)),
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

// firstReadingDay is the day a work was first read.
//
// It comes from the snapshot the page already holds rather than from
// [insights.Work], which does not carry it: that struct is serialised
// straight onto the API, and one page wanting a date is not a reason to
// change a wire contract. Both halves of the record are consulted,
// because an old sitting no longer exists one by one — it lives on as
// the day its rollup names, and a work read for a year would otherwise
// claim to have started whenever compaction happened to stop.
//
// A raw sitting is placed on the account-local day it ended, which is
// the day its rollup will name once it is compacted. Placing it by its
// start would move the date a reader sees the day the sitting aged out.
func firstReadingDay(snap store.StatsSnapshot, workID string, loc *time.Location) (time.Time, bool) {
	var first time.Time
	consider := func(day time.Time) {
		if first.IsZero() || day.Before(first) {
			first = day
		}
	}
	midnight := func(t time.Time) time.Time {
		d := t.In(loc)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	}
	for _, ses := range snap.Sessions {
		if ses.WorkID == workID {
			consider(midnight(ses.EndedAt))
		}
	}
	for _, ru := range snap.Rollups {
		if ru.WorkID != workID {
			continue
		}
		day, err := time.Parse(insights.DayFormat, ru.Day)
		if err != nil {
			continue
		}
		consider(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc))
	}
	return first, !first.IsZero()
}

// readingSpanDays is how long a book has been on the go, counted in
// calendar days.
//
// Both endpoints count, so a book started and finished in one afternoon
// has been read for a day rather than for none.
//
// The two dates are rebuilt in UTC before subtracting. Local midnights
// are not always twenty-four hours apart: across a spring-forward they
// are twenty-three, and dividing elapsed hours would lose a day.
func readingSpanDays(first, last time.Time) string {
	from := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
	to := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC)
	days := int(to.Sub(from)/(24*time.Hour)) + 1
	if days < 1 {
		days = 1
	}
	if days == 1 {
		return "1 day"
	}
	return strconv.Itoa(days) + " days"
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
