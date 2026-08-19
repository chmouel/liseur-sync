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
	"github.com/chmouel/liseur-sync/internal/store"
)

// --- dashboard ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	sessions, err := s.St.SessionsInRange(r.Context(), u.ID, from, now)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	works, _ := s.St.ListWorks(r.Context(), u.ID)
	titles := map[string]string{}
	for _, ws := range works {
		titles[ws.Work.ID] = orPlaceholder(ws.Work.Title)
	}

	var sum SummaryData
	sum.RangeDays = 30
	dayMin := map[string]float64{}
	loc := userLoc(u)
	for _, ses := range sessions {
		active := sessionActiveSeconds(ses)
		sum.ActiveMinutes += active / 60
		sum.Sessions++
		dayMin[ses.StartedAt.In(loc).Format("2006-01-02")] += active / 60
	}
	if rollups, err := s.St.RollupsInRange(r.Context(), u.ID,
		from.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02")); err == nil {
		for _, ru := range rollups {
			sum.ActiveMinutes += ru.ActiveSeconds / 60
			sum.Sessions += int(ru.SessionCount)
			dayMin[ru.Day] += ru.ActiveSeconds / 60
		}
	}
	sum.StreakDays = streakFrom(dayMin, u, now)

	// Year heatmap.
	yearStart := time.Date(now.In(loc).Year(), 1, 1, 0, 0, 0, 0, loc)
	yearSessions, err := s.St.SessionsInRange(r.Context(), u.ID, yearStart, now)
	if err == nil {
		yearMin := map[string]float64{}
		for _, ses := range yearSessions {
			yearMin[ses.StartedAt.In(loc).Format("2006-01-02")] +=
				sessionActiveSeconds(ses) / 60
		}
		if rollups, err := s.St.RollupsInRange(r.Context(), u.ID,
			yearStart.Format("2006-01-02"), now.In(loc).Format("2006-01-02")); err == nil {
			for _, ru := range rollups {
				yearMin[ru.Day] += ru.ActiveSeconds / 60
			}
		}
		var heat []DayCell
		for d := yearStart; !d.After(now); d = d.AddDate(0, 0, 1) {
			heat = append(heat, DayCell{Date: d.Format("2006-01-02"), Minutes: yearMin[d.Format("2006-01-02")]})
		}

		// Recent sessions (10 newest in the 30d window).
		var recent []SessionRow
		for i := len(sessions) - 1; i >= 0 && len(recent) < 10; i-- {
			ses := sessions[i]
			recent = append(recent, SessionRow{
				When:      ses.StartedAt.In(loc).Format("Jan 2 15:04"),
				WorkID:    ses.WorkID,
				WorkTitle: titles[ses.WorkID],
				DeviceID:  ses.DeviceID,
				Minutes:   int(ses.EndedAt.Sub(ses.StartedAt).Minutes()),
				StartProg: ses.StartProg,
				EndProg:   ses.EndProg,
			})
		}
		links := s.workBookIDs(r.Context(), u.ID, works)
		dashboard(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a),
			sum, heat, recent,
			s.markReadable(r, continueReading(works, links.active, loc))).
			Render(r.Context(), w)
		return
	}
	links := s.workBookIDs(r.Context(), u.ID, works)
	dashboard(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a),
		sum, nil, nil,
		s.markReadable(r, continueReading(works, links.active, loc))).
		Render(r.Context(), w)
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

func sessionActiveSeconds(ses store.Session) float64 {
	active := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	if active < 0 {
		return 0
	}
	return active
}

func streakFrom(dayMin map[string]float64, u *store.User, now time.Time) int {
	loc := userLoc(u)
	day := now.In(loc)
	if dayMin[day.Format("2006-01-02")] == 0 {
		day = day.AddDate(0, 0, -1)
	}
	streak := 0
	for dayMin[day.Format("2006-01-02")] > 0 {
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}

// --- works ---

// markReadable says which of these works' books the browser reader can
// open. The shelf it answers for is bounded (continueReadingLimit), so
// it is a handful of book lookups rather than the wall of them a whole
// catalog page would be.
func (s *Server) markReadable(r *http.Request, rows []WorkRow) []WorkRow {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BookID != "" {
			ids = append(ids, row.BookID)
		}
	}
	books := s.booksByID(r.Context(), ids)
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
		d.Minutes += sessionActiveSeconds(ses) / 60
		if delta := ses.EndProg - ses.StartProg; delta > 0 {
			progDelta += delta
			if ses.EditionSHA != nil {
				if ed, err := s.St.EditionBySHA(r.Context(), u.ID, *ses.EditionSHA); err == nil && ed.PageCount != nil {
					d.Pages += delta * float64(*ed.PageCount)
				}
			}
		}
		if rollups, err := s.St.RollupsForWork(r.Context(), u.ID, workID); err == nil {
			for _, ru := range rollups {
				d.Sessions += int(ru.SessionCount)
				d.Minutes += ru.ActiveSeconds / 60
				d.Pages += ru.Pages
				progDelta += ru.ProgDelta
			}
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
		if book, err := s.St.CatalogBookByID(r.Context(), d.BookID); err == nil {
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
			Minutes:   int(sessionActiveSeconds(ses) / 60),
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
	workPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), d, opRows, sessionRows).
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
		ID: id, UserID: u.ID, CodeSHA256: auth.HashSecret(code),
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
	s.renderSettings(w, r, a, u, section, view, userID, Flash{}, false, "", false)
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
		}
		s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	code, err := s.generateSecret()
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
