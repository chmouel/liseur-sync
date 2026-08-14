package webui

import (
	"net/http"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Presentation preferences (ADR-0011).
//
// Theme and view mode live in a cookie rather than in local storage, for
// two reasons. The server renders the theme onto the root element, so a
// reload cannot paint the wrong palette before a script gets a say; and
// the toggle is an ordinary form POST carrying the session's CSRF token,
// so it works with JavaScript switched off like every other mutation
// here. Nothing about a colour is worth a database column.
const prefsCookie = "liseur_ui"

// Theme values. Dark is the default because that is what a reading
// application is for; system defers to the browser.
const (
	themeDark   = "dark"
	themeLight  = "light"
	themeSystem = "system"
)

// View values. The grid is for recognising a book by its cover, the list
// is for scanning five thousand of them.
const (
	viewGrid = "grid"
	viewList = "list"
)

// prefs is what the shell needs to know before it draws anything.
type prefs struct {
	Theme string
	View  string
}

// defaultPrefs is what a browser that has never said otherwise gets.
func defaultPrefs() prefs { return prefs{Theme: themeDark, View: viewGrid} }

// readPrefs reads the preference cookie, falling back to the defaults
// for anything missing or unrecognised. A cookie is user input: an
// unknown value is a value somebody typed, not a value to render.
func readPrefs(r *http.Request) prefs {
	p := defaultPrefs()
	c, err := r.Cookie(prefsCookie)
	if err != nil {
		return p
	}
	for _, part := range strings.Split(c.Value, ".") {
		switch part {
		case themeDark, themeLight, themeSystem:
			p.Theme = part
		case viewGrid, viewList:
			p.View = part
		}
	}
	return p
}

// writePrefs stores both values in one cookie. It is not a credential:
// it is not HttpOnly-sensitive in the way the session is, but it is set
// the same way so that a proxy stripping a subpath cannot make it apply
// to the wrong path.
func writePrefs(w http.ResponseWriter, r *http.Request, p prefs) {
	http.SetCookie(w, &http.Cookie{
		Name:     prefsCookie,
		Value:    p.Theme + "." + p.View,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: false,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// handlePreferences records a theme or a view mode and returns the user
// to where they were. Both fields are optional: the theme toggle in the
// top bar and the view toggle in a browse toolbar are the same route
// with one field each.
// It is authenticated and CSRF-checked like every other mutation, even
// though the worst a forgery could do here is change somebody's colours:
// a route that skips the check is a route somebody later copies.
func (s *Server) handlePreferences(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, _ *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "bad csrf", http.StatusForbidden)
		return
	}
	p := readPrefs(r)
	switch theme := r.FormValue("theme"); theme {
	case themeDark, themeLight, themeSystem:
		p.Theme = theme
	}
	switch view := r.FormValue("view"); view {
	case viewGrid, viewList:
		p.View = view
	}
	writePrefs(w, r, p)

	// Back where they came from, but only if that is a place on this
	// UI: an open redirect through a preference toggle would be a
	// silly way to lose the property that every /ui link stays here.
	back := r.FormValue("back")
	if !safeUIPath(back) {
		back = "."
	}
	redirectRel(w, back, http.StatusSeeOther)
}

// safeUIPath accepts only a relative path with no scheme and no
// authority. "//host/x" and "https://host" are both rejected, as is any
// attempt to climb out with a backslash, which some browsers normalize
// to a forward one.
func safeUIPath(p string) bool {
	if p == "" || strings.ContainsAny(p, ":\\") {
		return false
	}
	if strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/") {
		return false
	}
	return true
}

// nextTheme is the cycle the one-button toggle walks: dark → light →
// system → dark.
func nextTheme(current string) string {
	switch current {
	case themeDark:
		return themeLight
	case themeLight:
		return themeSystem
	default:
		return themeDark
	}
}

// themeGlyph is what that button shows. Text rather than an icon font or
// an SVG sprite, because a glyph costs nothing to serve and nothing to
// cache.
func themeGlyph(current string) string {
	switch current {
	case themeDark:
		return "☾"
	case themeLight:
		return "☀"
	default:
		return "◐"
	}
}
