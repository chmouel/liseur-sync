// Package webui is the embedded admin/insights UI: templ templates,
// vendored htmx (no CDN), no SPA build step. Sessions are HttpOnly
// SameSite=Strict cookies backed by hashed auth_sessions rows; every
// mutation checks a per-session CSRF token.
package webui

import (
	"context"
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
	// Downloads delegates the content server's byte-handling to the
	// API's implementation. It is an interface rather than a concrete
	// server so that this package keeps depending on nothing but the
	// store — and, more importantly, so that there is exactly one
	// implementation of the download rules.
	Downloads Downloader
	// Covers renders book covers. Nil shows the placeholder everywhere,
	// which is a page that looks plain rather than a page that fails.
	Covers CoverServer
	// Uploads receives a book sent from the browser. Like Downloads it
	// is the API's implementation behind an interface, so there is one
	// set of rules about what may be written into a folder. Nil hides
	// the form rather than showing one that cannot work.
	Uploads Uploader
	// Deletes takes a book back out of a folder that accepts uploads
	// (ADR-0025). Nil hides the control.
	Deletes Deleter
	// Watching is told about a folder the moment somebody adds or
	// removes one, so "add a folder and the books show up" is true
	// without a restart. Nil means the server was started without a
	// watcher: a new folder is still catalogued, but not until the
	// periodic safety pass notices it, which is up to half an hour.
	Watching FolderWatcher
	// LoginLimiter throttles the login form. It is the same limiter
	// the API's /v1/login uses, so the two surfaces share one budget
	// per IP and the form cannot be used to sidestep the API's limit.
	LoginLimiter *auth.RateLimiter
	// AdminReauthUserLimiter and AdminReauthIPLimiter throttle the
	// admin panel's password re-verification on two independent
	// budgets, both of which must allow an attempt (ADR-0013). One is
	// keyed on the acting administrator, so an attacker who moves
	// between addresses still runs out; the other on the remote
	// address, so one account cannot spend the instance's whole budget.
	// Mount fills in defaults when they are nil.
	AdminReauthUserLimiter *auth.RateLimiter
	AdminReauthIPLimiter   *auth.RateLimiter
}

// FolderWatcher is the watcher surface the admin panel needs. It is an
// interface so this package keeps depending on nothing but the store,
// and so a test can add a folder without an inotify instance.
type FolderWatcher interface {
	Add(ctx context.Context, folder store.Folder)
	Remove(folderID string)
	// Scan asks for a pass over one folder now. It returns before the
	// pass finishes: reading a large folder takes longer than a request
	// should.
	Scan(folderID string)
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
	return checkCSRFValue(r.FormValue("csrf"), a)
}

// checkCSRFValue compares a token that has already been read. The upload
// form needs this: r.FormValue would parse the multipart body to find
// the field, and that body is a book being streamed to disk.
func checkCSRFValue(got string, a store.AuthSession) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(csrfFor(a))) == 1
}

// redirectRel emits a redirect with a relative Location header. Unlike
// http.Redirect (which would resolve the target against the stripped
// request path), the browser resolves it against its own URL — this is
// what keeps redirects under the proxy prefix.
func redirectRel(w http.ResponseWriter, loc string, code int) {
	w.Header().Set("Location", loc)
	w.WriteHeader(code)
}

// uiPolicy is the Content-Security-Policy every /ui page carries. It is
// as narrow as this UI actually needs, which is very narrow: the whole
// interface is server-rendered HTML, one vendored copy of htmx, one
// small script of our own, one stylesheet and same-origin cover images.
// There is no CDN, no analytics, no font service and no inline anything
// — ADR-0011 banned style attributes for exactly this reason, so the
// progress bars are width classes rather than styles a policy would
// have to permit.
//
// The reason it matters is that this UI displays metadata that arrived
// inside somebody's EPUB: titles, authors, and descriptions that are
// HTML in practice. The sanitizer parses that markup and lets almost
// nothing through, but a sanitizer is one mistake away from being no
// sanitizer at all, and this header is the fence behind it: a script
// that reaches the page still cannot run, and one that runs anyway
// cannot phone anywhere.
//
// The reader page is not covered by this: it writes its own, stricter,
// per-response nonce policy (setReaderPolicy) after this middleware
// runs, because it has to admit the blob: URLs a rendering engine needs
// while refusing everything a publication might try.
const uiPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// pagePolicy puts the UI's policy and the two headers that go with it
// on a response before the handler writes anything. A handler that
// needs a different policy — the reader does — simply sets its own,
// which replaces this one.
func pagePolicy(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", uiPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		h(w, r)
	}
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
	sec := func(h http.HandlerFunc) http.Handler { return secure(pagePolicy(h)) }

	// The re-verification budgets are stricter than the login limiter
	// (10 a minute): this verifier sits behind an authenticated session
	// and guards taking over another account, so an operator who
	// mistypes twice is expected, and a script is not.
	if s.AdminReauthUserLimiter == nil {
		s.AdminReauthUserLimiter = auth.NewRateLimiter(5, 15*time.Minute)
	}
	if s.AdminReauthIPLimiter == nil {
		s.AdminReauthIPLimiter = auth.NewRateLimiter(10, 15*time.Minute)
	}

	mux.Handle("GET /{$}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRel(w, "ui/", http.StatusMovedPermanently)
	}))
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/",
		// The files are embedded, so they carry no modification time
		// and the response has no validator a browser could ask about
		// — left alone, a browser that cached yesterday's reader keeps
		// running it against today's binary with no error anywhere.
		// no-cache means revalidate, which without a validator means
		// fetch: correct beats cached for a LAN-sized server.
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.FileServerFS(staticContent()).ServeHTTP(w, r)
		})))

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
		s.unauthenticatedLanding(w, r)
	}))
	mux.Handle("GET /ui/setup", sec(s.handleSetupPage))
	mux.Handle("POST /ui/setup", sec(s.rateLimited(s.handleSetup)))
	mux.Handle("GET /ui/library", sec(s.requireAuth(s.handleLibrary)))
	mux.Handle("GET /ui/works/{id}", sec(s.requireAuth(s.handleWork)))
	mux.Handle("GET /ui/books/{id}", sec(s.requireAuth(s.handleBook)))
	mux.Handle("GET /ui/books/{id}/download", sec(s.requireAuth(s.handleBookDownload)))
	mux.Handle("GET /ui/books/{id}/cover", sec(s.requireAuth(s.handleBookCover)))
	mux.Handle("GET /ui/books/{id}/series",
		sec(s.requireAuth(s.handleSeriesAssignForm)))
	// Registered ahead of the {kind} pattern only for the reader's sake;
	// the router prefers the literal segment either way.
	mux.Handle("GET /ui/folders/{folder}/search", sec(s.requireAuth(s.handleSearch)))
	// Entities are library-wide (ADR-0019), so they are browsed from the
	// root rather than from inside a folder. A series is read through
	// rather than browsed by, so it has a shelf of its own (ADR-0018)
	// where contributors and tags have a listing.
	mux.Handle("GET /ui/entities/series/suggest",
		sec(s.requireAuth(s.handleSeriesSuggest)))
	mux.Handle("GET /ui/entities/series/{entity}",
		sec(s.requireAuth(s.handleSeriesShelf)))
	mux.Handle("GET /ui/entities/{kind}", sec(s.requireAuth(s.handleEntities)))
	mux.Handle("GET /ui/entities/{kind}/{entity}",
		sec(s.requireAuth(s.handleEntityBooks)))
	mux.Handle("GET /ui/search", sec(s.requireAuth(s.handleTopSearch)))
	mux.Handle("GET /ui/devices", sec(s.requireAuth(s.handleDevices)))
	mux.Handle("GET /ui/settings", sec(s.requireAuth(s.handleSettings)))
	mux.Handle("GET /ui/admin", sec(s.requireAdmin(s.handleAdminOverview)))
	mux.Handle("GET /ui/admin/users", sec(s.requireAdmin(s.handleAdminUsers)))
	mux.Handle("GET /ui/admin/users/{id}", sec(s.requireAdmin(s.handleAdminUser)))
	mux.Handle("GET /ui/admin/folders", sec(s.requireAdmin(s.handleAdminFolders)))
	mux.Handle("GET /ui/admin/maintenance",
		sec(s.requireAdmin(s.handleAdminMaintenance)))

	mux.Handle("POST /ui/login", sec(s.rateLimited(s.handleLogin)))
	mux.Handle("POST /ui/logout", sec(s.handleLogout))
	mux.Handle("POST /ui/tokens", sec(s.requireAuth(s.handleCreateToken)))
	mux.Handle("POST /ui/tokens/{id}/scopes", sec(s.requireAuth(s.handleUpdateTokenScopes)))
	mux.Handle("POST /ui/tokens/{id}/revoke", sec(s.requireAuth(s.handleRevokeToken)))
	mux.Handle("POST /ui/browsers/revoke", sec(s.requireAuth(s.handleRevokeBrowsers)))
	mux.Handle("POST /ui/pairing", sec(s.requireAuth(s.handlePairing)))
	mux.Handle("POST /ui/koplugin", sec(s.requireAuth(s.handleCreateKoplugin)))
	mux.Handle("POST /ui/koplugin/{id}/revoke", sec(s.requireAuth(s.handleRevokeKoplugin)))
	mux.Handle("POST /ui/kosync/{slot}/revoke", sec(s.requireAuth(s.handleRevokeKosync)))
	mux.Handle("POST /ui/preferences", sec(s.requireAuth(s.handlePreferences)))
	mux.Handle("POST /ui/library/upload", sec(s.requireAuth(s.handleLibraryUpload)))
	mux.Handle("POST /ui/books/{id}/series", sec(s.requireAuth(s.handleSeriesAssign)))
	// Deleting (ADR-0024). A work is the reader's own, so it is
	// requireAuth and the store checks whose it is; a catalog row is
	// shared, so retiring one is admin work.
	mux.Handle("POST /ui/works/{id}/delete", sec(s.requireAuth(s.handleDeleteWork)))
	mux.Handle("POST /ui/books/{id}/delete",
		sec(s.requireAdmin(s.handleDeleteMissingBook)))
	// Deleting a book outright (ADR-0025). Admin, because a browser
	// session carries no scopes at all — the account carries the role,
	// a token carries capabilities — so there is nothing here to check
	// library-delete against.
	mux.Handle("POST /ui/books/{id}/destroy",
		sec(s.requireAdmin(s.handleDeleteBookFile)))
	mux.Handle("POST /ui/books/{id}/series/reset",
		sec(s.requireAuth(s.handleSeriesReset)))
	// Renaming a series (ADR-0020). The reset arrives on the same route
	// as a submit button, because the form is one decision: what this
	// shelf is called.
	mux.Handle("POST /ui/entities/series/{entity}/name",
		sec(s.requireAuth(s.handleSeriesRename)))
	// Merging and splitting a shelf (ADR-0021). They are admin work and
	// everybody sees the result, so unlike a rename they have no
	// personal form; the handler says so rather than the router, which
	// is how the shelf can explain the refusal in the reader's terms.
	mux.Handle("POST /ui/entities/series/{entity}/merge",
		sec(s.requireAuth(s.handleSeriesMerge)))
	mux.Handle("POST /ui/entities/series/{entity}/split",
		sec(s.requireAuth(s.handleSeriesSplit)))
	mux.Handle("POST /ui/entities/series/{entity}/unbind",
		sec(s.requireAuth(s.handleSeriesUnbind)))
	mux.Handle("POST /ui/settings", sec(s.requireAuth(s.handleSaveSettings)))
	mux.Handle("POST /ui/settings/password", sec(s.requireAuth(s.handleChangePassword)))
	mux.Handle("GET /ui/books/{id}/read", sec(s.handleReaderRoute))
	mux.Handle("POST /ui/reader/token", sec(s.requireAuth(s.handleReaderToken)))
	mux.Handle("POST /ui/admin/users", sec(s.requireAdmin(s.handleAdminCreateUser)))
	mux.Handle("POST /ui/admin/users/{id}/password",
		sec(s.requireAdmin(s.handleAdminSetPassword)))
	mux.Handle("POST /ui/admin/users/{id}/admin",
		sec(s.requireAdmin(s.handleAdminSetRole)))
	mux.Handle("POST /ui/admin/users/{id}/disabled",
		sec(s.requireAdmin(s.handleAdminSetDisabled)))
	mux.Handle("POST /ui/admin/users/{id}/credentials/revoke",
		sec(s.requireAdmin(s.handleAdminRevokeCredentials)))
	mux.Handle("POST /ui/admin/users/{id}/tokens/{tokenID}/revoke",
		sec(s.requireAdmin(s.handleAdminRevokeToken)))
	mux.Handle("POST /ui/admin/users/{id}/kosync/{slot}/revoke",
		sec(s.requireAdmin(s.handleAdminRevokeKosync)))
	mux.Handle("POST /ui/admin/users/{id}/koplugin/{deviceID}/revoke",
		sec(s.requireAdmin(s.handleAdminRevokeKoplugin)))
	mux.Handle("POST /ui/admin/users/{id}/tokens",
		sec(s.requireAdmin(s.handleAdminMintToken)))
	mux.Handle("POST /ui/admin/users/{id}/pairing",
		sec(s.requireAdmin(s.handleAdminPairingCode)))
	mux.Handle("POST /ui/admin/users/{id}/koplugin",
		sec(s.requireAdmin(s.handleAdminCreateKoplugin)))
	mux.Handle("POST /ui/admin/users/{id}/backfill",
		sec(s.requireAdmin(s.handleAdminBackfillWorks)))
	mux.Handle("POST /ui/admin/folders", sec(s.requireAdmin(s.handleAdminCreateFolder)))
	mux.Handle("POST /ui/admin/folders/{id}/scan",
		sec(s.requireAdmin(s.handleAdminScanFolder)))
	mux.Handle("POST /ui/admin/folders/{id}/uploads",
		sec(s.requireAdmin(s.handleAdminSetFolderUploads)))
	mux.Handle("POST /ui/admin/folders/{id}/delete",
		sec(s.requireAdmin(s.handleAdminDeleteFolder)))
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
		next(w, withAdmin(r, s.adminFlag(r, u)), a, u)
	}
}

// adminFlag resolves the signed-in user's admin rights once per
// request, so the shell can decide whether to draw the Admin rail entry
// and requireAdmin can gate on the same answer without asking twice. A
// store failure is nil rather than false: "we could not tell" is a 500
// on an admin route, not a silent denial.
func (s *Server) adminFlag(r *http.Request, u *store.User) *bool {
	isAdmin, err := s.Auth.IsAdmin(r.Context(), u.ID)
	if err != nil {
		return nil
	}
	return &isAdmin
}

// requireAdmin additionally demands admin rights, per the single
// definition in auth.Service.IsAdmin (an active admin-scope token,
// which only the admin CLI or an existing admin can mint). A user
// without them never sees a link here, so arriving is either a typed
// URL or a stale bookmark: both deserve a page that says what happened.
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, store.AuthSession, *store.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
		isAdmin := adminFromContext(r)
		if isAdmin == nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if !*isAdmin {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				forbiddenPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a)).
					Render(r.Context(), w)
				return
			}
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
				loginPage(relPrefix(r.URL.Path), uiCtx(r, nil), "too many attempts, try again later").
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
		loginPage(prefix, uiCtx(r, nil), "invalid credentials").Render(r.Context(), w)
		return
	}
	ok, err := auth.CheckPassword(r.FormValue("password"), u.Argon2Hash)
	if err != nil || !ok {
		loginPage(prefix, uiCtx(r, nil), "invalid credentials").Render(r.Context(), w)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		loginPage(prefix, uiCtx(r, nil), "internal error").Render(r.Context(), w)
		return
	}
	redirectRel(w, "./", http.StatusSeeOther)
}

// startSession mints a web session for u and sets the cookie. Sign-in
// and first-run setup share it so that an account created by setup is
// signed in on exactly the same terms as one that typed its password.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) error {
	secret, err := auth.NewSecret()
	if err != nil {
		return err
	}
	csrf, err := auth.NewSecret()
	if err != nil {
		return err
	}
	id, err := auth.NewSecret()
	if err != nil {
		return err
	}
	now := time.Now()
	if err := s.St.CreateAuthSession(r.Context(), store.AuthSession{
		ID: id, UserID: u.ID, SHA256: auth.HashSecret(secret), Kind: "web",
		CSRFHash: auth.HashSecret(csrf), CreatedAt: now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}); err != nil {
		return err
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
	return nil
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
