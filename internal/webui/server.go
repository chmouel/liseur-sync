// Package webui is the embedded admin/insights UI: templ templates,
// vendored htmx (no CDN), no SPA build step. Sessions are HttpOnly
// SameSite=Strict cookies backed by hashed auth_sessions rows; every
// mutation checks a per-session CSRF token.
package webui

import (
	"crypto/subtle"
	"embed"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
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
	// Cfg is the instance config. The session cookie's Secure
	// attribute derives from it rather than from r.TLS: in the
	// documented deployment TLS terminates at a reverse proxy and the
	// backend is spoken to over HTTP, so r.TLS is nil even though the
	// browser is on HTTPS.
	Cfg config.Config
	// Uploads and Downloads delegate the content server's byte-handling
	// to the API's implementation. They are interfaces rather than a
	// concrete server so that this package keeps depending on nothing
	// but the store — and, more importantly, so that there is exactly
	// one implementation of the staging and download rules.
	Uploads   Uploader
	Downloads Downloader
	// Covers renders book covers. Nil shows the placeholder everywhere,
	// which is a page that looks plain rather than a page that fails.
	Covers CoverServer
	// LoginLimiter throttles the login form. It is the same limiter
	// the API's /v1/login uses, so the two surfaces share one budget
	// per IP and the form cannot be used to sidestep the API's limit.
	LoginLimiter *auth.RateLimiter
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
// /sync*`, ideally paired with `redir /sync /sync/`).
func (s *Server) Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler) {
	// Every route below carries or sets the session cookie, so all of
	// them go through the transport check — not just the login POST.
	// Static assets are exempt: they hold no credential, and serving
	// the stylesheet keeps the "https required" page readable.
	sec := func(h http.HandlerFunc) http.Handler { return secure(h) }

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
	mux.Handle("GET /ui/", sec(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/" {
			http.NotFound(w, r)
			return
		}
		s.requireAuth(s.handleDashboard)(w, r)
	}))
	mux.Handle("GET /ui/login", sec(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := s.session(r); ok {
			redirectRel(w, "./", http.StatusSeeOther)
			return
		}
		loginPage(relPrefix(r.URL.Path), "").Render(r.Context(), w)
	}))
	mux.Handle("GET /ui/works", sec(s.requireAuth(s.handleWorks)))
	mux.Handle("GET /ui/works/{id}", sec(s.requireAuth(s.handleWork)))
	mux.Handle("GET /ui/books", sec(s.requireAuth(s.handleBooks)))
	mux.Handle("GET /ui/books/{id}", sec(s.requireAuth(s.handleBook)))
	mux.Handle("GET /ui/books/{id}/download", sec(s.requireAuth(s.handleBookDownload)))
	mux.Handle("GET /ui/books/{id}/cover", sec(s.requireAuth(s.handleBookCover)))
	mux.Handle("POST /ui/books/upload", sec(s.requireAuth(s.handleUploadBook)))
	mux.Handle("POST /ui/books/{id}/delete", sec(s.requireAuth(s.handleDeleteBook)))
	mux.Handle("POST /ui/books/{id}/restore", sec(s.requireAuth(s.handleRestoreBook)))
	// Registered ahead of the {kind} pattern only for the reader's sake;
	// the router prefers the literal segment either way.
	mux.Handle("GET /ui/libraries/{library}/search", sec(s.requireAuth(s.handleSearch)))
	mux.Handle("GET /ui/libraries/{library}/{kind}", sec(s.requireAuth(s.handleEntities)))
	mux.Handle("GET /ui/libraries/{library}/{kind}/{entity}", sec(s.requireAuth(s.handleEntityBooks)))
	mux.Handle("POST /ui/libraries/{library}/{kind}/merge", sec(s.requireAuth(s.handleMergeEntities)))
	mux.Handle("POST /ui/libraries/{library}/{kind}/{entity}/rename", sec(s.requireAuth(s.handleRenameEntity)))
	mux.Handle("POST /ui/books/{id}/metadata", sec(s.requireAuth(s.handleEditBookMetadata)))
	mux.Handle("POST /ui/books/{id}/accept", sec(s.requireAuth(s.handleAcceptBook)))
	mux.Handle("GET /ui/devices", sec(s.requireAuth(s.handleDevices)))
	mux.Handle("GET /ui/settings", sec(s.requireAuth(s.handleSettings)))
	mux.Handle("GET /ui/admin", sec(s.requireAdmin(s.handleAdmin)))

	mux.Handle("POST /ui/login", sec(s.rateLimited(s.handleLogin)))
	mux.Handle("POST /ui/logout", sec(s.handleLogout))
	mux.Handle("POST /ui/tokens", sec(s.requireAuth(s.handleCreateToken)))
	mux.Handle("POST /ui/tokens/{id}/scopes", sec(s.requireAuth(s.handleUpdateTokenScopes)))
	mux.Handle("POST /ui/tokens/{id}/revoke", sec(s.requireAuth(s.handleRevokeToken)))
	mux.Handle("POST /ui/pairing", sec(s.requireAuth(s.handlePairing)))
	mux.Handle("POST /ui/koplugin", sec(s.requireAuth(s.handleCreateKoplugin)))
	mux.Handle("POST /ui/koplugin/{id}/revoke", sec(s.requireAuth(s.handleRevokeKoplugin)))
	mux.Handle("POST /ui/kosync/{slot}/revoke", sec(s.requireAuth(s.handleRevokeKosync)))
	mux.Handle("POST /ui/settings", sec(s.requireAuth(s.handleSaveSettings)))
	mux.Handle("POST /ui/settings/password", sec(s.requireAuth(s.handleChangePassword)))
	mux.Handle("POST /ui/reader/token", sec(s.requireAuth(s.handleReaderToken)))
	mux.Handle("POST /ui/admin/invites", sec(s.requireAdmin(s.handleCreateInvite)))
	mux.Handle("POST /ui/admin/invites/{id}/revoke", sec(s.requireAdmin(s.handleRevokeInvite)))
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

// requireAdmin additionally demands admin rights, per the single
// definition in auth.Service.IsAdmin (an active admin-scope token,
// which only the admin CLI or an existing admin can mint).
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, store.AuthSession, *store.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
		isAdmin, err := s.Auth.IsAdmin(r.Context(), u.ID)
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if !isAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r, a, u)
	})
}

// rateLimited throttles a credential-verifying handler per remote IP.
// It renders the login page with a message rather than the API's JSON
// 429, since the only caller is a browser form.
func (s *Server) rateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.LoginLimiter != nil {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if !s.LoginLimiter.Allow(host) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				loginPage(relPrefix(r.URL.Path), "too many attempts, try again later").
					Render(r.Context(), w)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	u, err := s.St.UserByName(r.Context(), r.FormValue("username"))
	if err != nil {
		// Same work as a real check, so an unknown username cannot be
		// told apart from a wrong password by response time.
		auth.CheckDummyPassword(r.FormValue("password"))
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
	// subpath (e.g. /sync/ui/) when served under one.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: secret,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure:  !s.Cfg.InsecureHTTP,
		Expires: now.Add(7 * 24 * time.Hour),
	})
	redirectRel(w, "./", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.session(r)
	if ok && s.checkCSRF(r, a) {
		// Reader tokens are derived from the session, so they end with
		// it. This revokes them for every session the user has open,
		// not just this one: a tab still signed in re-mints on its next
		// call, whereas a token left alive after a sign-out elsewhere
		// would be exactly the access the user thought they revoked.
		_ = s.Auth.RevokeReaderTokens(r.Context(), a.UserID)
		_ = s.St.RevokeAuthSession(r.Context(), a.UserID, a.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", MaxAge: -1})
	redirectRel(w, "./", http.StatusSeeOther)
}
