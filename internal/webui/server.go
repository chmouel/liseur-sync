// Package webui is the embedded admin/insights UI: templ templates,
// vendored htmx (no CDN), no SPA build step. Sessions are HttpOnly
// SameSite=Strict cookies backed by hashed auth_sessions rows; every
// mutation checks a per-session CSRF token.
package webui

import (
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

//go:embed static
var staticFS embed.FS

// staticContent returns the embedded files rooted at static/, so the
// stripped request path matches ("/ui/static/x" -> "x" in static/).
func staticContent() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// Server is the web UI handler set.
type Server struct {
	St   store.Store
	Auth *auth.Service
}

const cookieName = "liseur_session"

// csrfFor derives a per-session CSRF token the server can recompute
// without storing plaintext: sha256(stored session hash). Stateless
// double-submit variant bound to the session row.
func csrfFor(a store.AuthSession) string {
	return auth.HashSecret(a.SHA256)
}

func (s *Server) session(r *http.Request) (store.AuthSession, *store.User, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return store.AuthSession{}, nil, false
	}
	a, err := s.St.AuthSessionByHash(r.Context(), auth.HashSecret(c.Value))
	if err != nil || a.Kind != "web" || a.RevokedAt != nil || time.Now().After(a.ExpiresAt) {
		return store.AuthSession{}, nil, false
	}
	u, err := s.St.UserByID(r.Context(), a.UserID)
	if err != nil {
		return store.AuthSession{}, nil, false
	}
	return a, &u, true
}

func (s *Server) checkCSRF(r *http.Request, a store.AuthSession) bool {
	return subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(csrfFor(a))) == 1
}

// redirectRel emits a redirect with a relative Location header. Unlike
// http.Redirect (which would resolve the target against the stripped
// request path), the browser resolves it against its own URL — this is
// what keeps redirects under the proxy prefix.
func redirectRel(w http.ResponseWriter, loc string, code int) {
	w.Header().Set("Location", loc)
	w.WriteHeader(code)
}

// Mount registers the UI routes. Route patterns stay absolute /ui/...;
// only rendered URLs and redirect Locations are relative, so the UI
// can be served under a stripped subpath (e.g. Caddy `handle_path
// /synch*`, ideally paired with `redir /synch /synch/`).
func (s *Server) Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler) {
	mux.Handle("GET /{$}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRel(w, "ui/", http.StatusMovedPermanently)
	}))
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/",
		http.FileServerFS(staticContent())))

	// /ui normalizes to /ui/ so the dashboard shares the /ui/ base
	// directory with the other top-level pages and relative links
	// resolve identically everywhere.
	mux.Handle("GET /ui", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRel(w, "ui/", http.StatusMovedPermanently)
	}))
	mux.Handle("GET /ui/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/" {
			http.NotFound(w, r)
			return
		}
		s.requireAuth(s.handleDashboard)(w, r)
	}))
	mux.Handle("GET /ui/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := s.session(r); ok {
			redirectRel(w, "./", http.StatusSeeOther)
			return
		}
		loginPage(relPrefix(r.URL.Path), "").Render(r.Context(), w)
	}))
	mux.Handle("GET /ui/works", http.HandlerFunc(s.requireAuth(s.handleWorks)))
	mux.Handle("GET /ui/works/{id}", http.HandlerFunc(s.requireAuth(s.handleWork)))
	mux.Handle("GET /ui/devices", http.HandlerFunc(s.requireAuth(s.handleDevices)))
	mux.Handle("GET /ui/settings", http.HandlerFunc(s.requireAuth(s.handleSettings)))
	mux.Handle("GET /ui/admin", http.HandlerFunc(s.requireAdmin(s.handleAdmin)))

	mux.Handle("POST /ui/login", secure(http.HandlerFunc(s.handleLogin)))
	mux.Handle("POST /ui/logout", http.HandlerFunc(s.handleLogout))
	mux.Handle("POST /ui/tokens", http.HandlerFunc(s.requireAuth(s.handleCreateToken)))
	mux.Handle("POST /ui/tokens/{id}/revoke", http.HandlerFunc(s.requireAuth(s.handleRevokeToken)))
	mux.Handle("POST /ui/pairing", http.HandlerFunc(s.requireAuth(s.handlePairing)))
	mux.Handle("POST /ui/koplugin", http.HandlerFunc(s.requireAuth(s.handleCreateKoplugin)))
	mux.Handle("POST /ui/koplugin/{id}/revoke", http.HandlerFunc(s.requireAuth(s.handleRevokeKoplugin)))
	mux.Handle("POST /ui/kosync/{slot}/revoke", http.HandlerFunc(s.requireAuth(s.handleRevokeKosync)))
	mux.Handle("POST /ui/settings", http.HandlerFunc(s.requireAuth(s.handleSaveSettings)))
	mux.Handle("POST /ui/admin/invites", http.HandlerFunc(s.requireAdmin(s.handleCreateInvite)))
	mux.Handle("POST /ui/admin/invites/{id}/revoke", http.HandlerFunc(s.requireAdmin(s.handleRevokeInvite)))
}

// requireAuth redirects to the canonical login page when
// unauthenticated, via a relative Location so the browser stays under
// the proxy prefix.
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, store.AuthSession, *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, u, ok := s.session(r)
		if !ok {
			redirectRel(w, relPrefix(r.URL.Path)+"login", http.StatusSeeOther)
			return
		}
		next(w, r, a, u)
	}
}

// requireAdmin additionally demands an admin-scope token exists for the
// user (the web session is user-level; admin pages require the user to
// hold at least one active admin token — crude but explicit for v1).
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, store.AuthSession, *store.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
		toks, err := s.St.ListTokens(r.Context(), u.ID)
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		isAdmin := false
		for _, t := range toks {
			if t.Scope == store.ScopeAdmin && t.RevokedAt == nil {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r, a, u)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	u, err := s.St.UserByName(r.Context(), r.FormValue("username"))
	if err != nil {
		loginPage(prefix, "invalid credentials").Render(r.Context(), w)
		return
	}
	ok, err := auth.CheckPassword(r.FormValue("password"), u.Argon2Hash)
	if err != nil || !ok {
		loginPage(prefix, "invalid credentials").Render(r.Context(), w)
		return
	}
	secret, _ := auth.NewSecret()
	csrf, _ := auth.NewSecret()
	id, _ := auth.NewSecret()
	now := time.Now()
	if err := s.St.CreateAuthSession(r.Context(), store.AuthSession{
		ID: id, UserID: u.ID, SHA256: auth.HashSecret(secret), Kind: "web",
		CSRFHash: auth.HashSecret(csrf), CreatedAt: now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}); err != nil {
		loginPage(prefix, "internal error").Render(r.Context(), w)
		return
	}
	// No Path attribute: the RFC 6265 default-path (the directory of
	// the request URL) scopes the cookie to /ui/ — or to the proxy
	// subpath (e.g. /synch/ui/) when served under one.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: secret,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
		Expires: now.Add(7 * 24 * time.Hour),
	})
	redirectRel(w, "./", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.session(r)
	if ok && s.checkCSRF(r, a) {
		_ = s.St.RevokeAuthSession(r.Context(), a.UserID, a.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", MaxAge: -1})
	redirectRel(w, "./", http.StatusSeeOther)
}
