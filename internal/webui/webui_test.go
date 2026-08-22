package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func testServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	return testServerCfg(t, nil, nil)
}

// testServerCfg builds the UI against a config and a Server the caller
// can tweak, so transport security and rate limiting can be exercised
// both ways.
func testServerCfg(t *testing.T, mutate func(*config.Config), tune func(*Server)) (*httptest.Server, store.Store) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{
		ID: "u1", Name: "alice", Argon2Hash: hash,
		KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now(),
	}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.InsecureHTTP = true
	if mutate != nil {
		mutate(&cfg)
	}
	s := &Server{St: st, Auth: auth.NewService(st), Cfg: cfg}
	if tune != nil {
		tune(s)
	}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler {
		return auth.RequireSecureTransport(cfg, h)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func loginCookie(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	resp, err := noRedirect().PostForm(ts.URL+"/ui/login", url.Values{
		"username": {"alice"}, "password": {"hunter2hunter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func page(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func postForm(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string, form url.Values) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func extractCSRF(t *testing.T, html string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatal("no csrf field in page")
	}
	rest := html[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func secretFromPage(t *testing.T, html string) string {
	t.Helper()
	const marker = `<code class="big">`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("no shown-once secret in page: %s", html)
	}
	rest := html[i+len(marker):]
	end := strings.Index(rest, "</code>")
	if end < 0 {
		t.Fatalf("unterminated shown-once secret in page: %s", html)
	}
	return rest[:end]
}

func TestAuthFlowAndPages(t *testing.T) {
	ts, _ := testServer(t)

	// Root 301s with a relative Location (subpath-safe).
	resp, _ := noRedirect().Get(ts.URL + "/")
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "ui/" {
		t.Fatalf("root: want 301 -> ui/, got %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Following the root redirect lands on the UI (login page when
	// signed out).
	resp, err := http.DefaultClient.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Sign in") {
		t.Fatalf("root follow: %d", resp.StatusCode)
	}

	// /ui normalizes to /ui/ so relative links share one base.
	resp, _ = noRedirect().Get(ts.URL + "/ui")
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "ui/" {
		t.Fatalf("/ui: want 301 -> ui/, got %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Unauthenticated pages redirect (relatively) to the login page.
	resp, _ = noRedirect().Get(ts.URL + "/ui/library")
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "login" {
		t.Fatalf("unauth /ui/works: want 303 -> login, got %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Depth-aware: from a nested page the relative target needs "../".
	resp, _ = noRedirect().Get(ts.URL + "/ui/works/xyz")
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "../login" {
		t.Fatalf("unauth /ui/works/xyz: want 303 -> ../login, got %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Following redirects renders the login form.
	code, body := page(t, ts, nil, "/ui")
	if code != 200 || !strings.Contains(body, "Sign in") {
		t.Fatalf("unauth /ui: %d", code)
	}

	// Bad login re-renders with error.
	code, body = postForm(t, ts, nil, "/ui/login", url.Values{
		"username": {"alice"}, "password": {"wrong"},
	})
	if code != 200 || !strings.Contains(body, "invalid credentials") {
		t.Fatalf("bad login: %d", code)
	}

	cookie := loginCookie(t, ts)

	for _, path := range []string{"/ui", "/ui/", "/ui/library", "/ui/settings", "/ui/settings?section=devices"} {
		code, body = page(t, ts, cookie, path)
		if code != 200 || !strings.Contains(body, "liseur-sync") {
			t.Fatalf("%s: %d", path, code)
		}
		// Pages must render only relative URLs so they work behind a
		// prefix-stripping proxy.
		for _, abs := range []string{`href="/`, `action="/`, `src="/`} {
			if strings.Contains(body, abs) {
				t.Fatalf("%s: absolute URL %q in page", path, abs)
			}
		}
	}
	// Dashboard shows the heatmap and stat cards.
	_, body = page(t, ts, cookie, "/ui")
	if !strings.Contains(body, "heatmap") || !strings.Contains(body, "day streak") {
		t.Fatal("dashboard missing heatmap/stats")
	}
	// The admin tab is forbidden to an ordinary account, and the rail does
	// not advertise it in the first place.
	_, body = page(t, ts, cookie, "/ui")
	if strings.Contains(body, `>Administration<`) {
		t.Fatal("rail offers Administration to a non-admin")
	}
	code, body = page(t, ts, cookie, "/ui/settings?section=admin")
	if code != 403 {
		t.Fatalf("admin Settings tab as a non-admin: want 403, got %d", code)
	}
	if !strings.Contains(body, "Not allowed") {
		t.Fatalf("admin Settings denial is not the rendered page: %q", body)
	}
	for _, p := range []string{
		"/ui/admin", "/ui/admin/users", "/ui/admin/users/u1", "/ui/admin/folders",
		"/ui/admin/maintenance",
	} {
		code, _ = page(t, ts, cookie, p)
		if code != http.StatusNotFound {
			t.Fatalf("removed GET %s as a non-admin: want 404, got %d", p, code)
		}
	}
	for _, p := range []string{
		"/ui/admin/invites", "/ui/admin/users", "/ui/admin/users/u1/password",
		"/ui/admin/users/u1/admin", "/ui/admin/users/u1/disabled",
		"/ui/admin/users/u1/credentials/revoke",
		"/ui/admin/users/u1/tokens/t1/revoke", "/ui/admin/users/u1/kosync/s1/revoke",
		"/ui/admin/users/u1/koplugin/k1/revoke",
		"/ui/admin/folders", "/ui/admin/folders/f1/delete",
		"/ui/admin/folders/f1/scan",
		"/ui/admin/users/u1/tokens", "/ui/admin/users/u1/pairing",
		"/ui/admin/users/u1/koplugin", "/ui/admin/users/u1/backfill",
	} {
		if code, _ := postForm(t, ts, cookie, p, url.Values{}); code != 403 {
			t.Fatalf("POST %s as a non-admin: want 403, got %d", p, code)
		}
	}
}

func TestDevicesCRUDAndCSRF(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, body)

	// Mutation without CSRF is rejected.
	code, _ := postForm(t, ts, cookie, "/ui/tokens", url.Values{"name": {"x"}, "scope": {"sync"}})
	if code != 403 {
		t.Fatalf("no-CSRF token create: want 403, got %d", code)
	}

	// Create a token with CSRF.
	code, body = postForm(t, ts, cookie, "/ui/tokens", url.Values{
		"name": {"Test Dev"}, "scopes": {"sync", "library-read"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "shown once") {
		t.Fatalf("token create: %d", code)
	}

	// The new token appears in the store.
	toks, _ := st.ListTokens(t.Context(), "u1")
	if len(toks) != 1 || toks[0].Name != "Test Dev" ||
		toks[0].Scopes.String() != "sync,library-read" {
		t.Fatalf("tokens: %+v", toks)
	}
	deviceID := toks[0].DeviceID

	// Updating scopes preserves the token and device identities.
	code, body = postForm(t, ts, cookie, "/ui/tokens/"+toks[0].ID+"/scopes", url.Values{
		"scopes": {"read-insights", "library-read"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "Token scopes updated") {
		t.Fatalf("token scope update: %d", code)
	}
	toks, _ = st.ListTokens(t.Context(), "u1")
	if len(toks) != 1 || toks[0].DeviceID != deviceID ||
		toks[0].Scopes.String() != "read-insights,library-read" {
		t.Fatalf("updated token: %+v", toks)
	}

	// Pairing code generation.
	code, body = postForm(t, ts, cookie, "/ui/pairing", url.Values{"csrf": {csrf}})
	if code != 200 || !strings.Contains(body, "pairing code") {
		t.Fatalf("pairing: %d", code)
	}

	// koplugin capability.
	code, body = postForm(t, ts, cookie, "/ui/koplugin", url.Values{
		"name": {"Kobo"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "capability") {
		t.Fatalf("koplugin create: %d", code)
	}
	devs, _ := st.ListKopluginDevices(t.Context(), "u1")
	if len(devs) != 1 {
		t.Fatalf("koplugin devices: %+v", devs)
	}

	// Revoke it.
	code, _ = postForm(t, ts, cookie, "/ui/koplugin/"+devs[0].ID+"/revoke", url.Values{"csrf": {csrf}})
	if code != 200 {
		t.Fatalf("koplugin revoke: %d", code)
	}
	devs, _ = st.ListKopluginDevices(t.Context(), "u1")
	if devs[0].RevokedAt == nil {
		t.Fatal("capability not revoked")
	}
}

// A scope the store knows but no page offers is a scope only the admin
// CLI can grant, which is how library-upload shipped: the reader could
// see the offer in the app and had no way to authorise it.
func TestEveryGrantableScopeIsOfferedByTheUI(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, body)

	for _, scope := range []store.Scope{
		store.ScopeSync, store.ScopeReadInsights, store.ScopeLibraryRead,
		store.ScopeLibraryManage, store.ScopeLibraryUpload,
	} {
		if !strings.Contains(body, `value="`+string(scope)+`"`) {
			t.Errorf("the devices page offers no checkbox for %q", scope)
		}
	}

	// And the one that was missing can be granted, not just displayed.
	code, _ := postForm(t, ts, cookie, "/ui/tokens", url.Values{
		"name": {"Phone"}, "scopes": {"sync", "library-upload"}, "csrf": {csrf},
	})
	if code != 200 {
		t.Fatalf("minting a library-upload token: %d", code)
	}
	toks, _ := st.ListTokens(t.Context(), "u1")
	if len(toks) != 1 || !toks[0].Scopes.Contains(store.ScopeLibraryUpload) {
		t.Fatalf("token did not keep the upload scope: %+v", toks)
	}
}

func TestSettingsSave(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/settings", url.Values{
		"timezone": {"Europe/Paris"}, "kosync_enabled": {"on"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "Saved") {
		t.Fatalf("settings save: %d", code)
	}
	u, _ := st.UserByID(t.Context(), "u1")
	if u.Timezone != "Europe/Paris" || !u.KosyncEnabled || u.KopluginEnabled {
		t.Fatalf("settings: %+v", u)
	}
}

func TestAdminInvites(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	// Admin is an account property (ADR-0013), not a token.
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if code != 200 || !strings.Contains(body, "Invite codes") {
		t.Fatalf("admin users page: %d", code)
	}
	if !strings.Contains(body, `>Administration<`) {
		t.Fatal("Settings hides Administration from an admin")
	}
	csrf := extractCSRF(t, body)
	if got, _ := postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
		"admin_password": {"hunter2hunter"},
	}); got != http.StatusForbidden {
		t.Fatalf("invite without CSRF: want 403, got %d", got)
	}
	for _, password := range []string{"", "wrong-password"} {
		code, body = postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
			"csrf": {csrf}, "admin_password": {password},
		})
		if code != http.StatusOK || !strings.Contains(body, "your password is wrong") {
			t.Fatalf("invite with admin password %q: %d %q", password, code, body)
		}
		if invs, err := st.ListInvites(t.Context(), "u1"); err != nil || len(invs) != 0 {
			t.Fatalf("refused invite created state: %+v err=%v", invs, err)
		}
	}
	code, body = postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
		"csrf": {csrf}, "admin_password": {"hunter2hunter"},
	})
	if code != 200 || !strings.Contains(body, "Invite code") {
		t.Fatalf("invite create: %d", code)
	}
	secret := secretFromPage(t, body)
	if len(secret) != 32 || strings.Count(body, secret) != 1 {
		t.Fatalf("invite code should be shown exactly once: %q", secret)
	}
	invs, _ := st.ListInvites(t.Context(), "u1")
	if len(invs) != 1 || invs[0].CodeSHA256 != auth.HashSecret(secret) {
		t.Fatalf("invites: %+v", invs)
	}
	_, nextBody := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if strings.Contains(nextBody, secret) {
		t.Fatal("invite code was shown after its creation response")
	}
	// Revocation reduces authority, so CSRF is enough; no password
	// re-verification field is sent.
	code, body = postForm(t, ts, cookie, "/ui/admin/invites/"+invs[0].ID+"/revoke",
		url.Values{"csrf": {csrf}})
	if code != http.StatusOK || !strings.Contains(body, "Invite revoked") {
		t.Fatalf("invite revoke without password: %d %q", code, body)
	}
}

// adminTestDatabaseURL is the planted secret: if the config card ever
// walks the config struct instead of naming its fields, this string
// lands on a web page and the test below says so.
const adminTestDatabaseURL = "postgres://liseur:hunter2secret@db.example/liseur?sslmode=require"

// TestAdminOverview covers the section's landing page: it names the
// build, it counts what the instance holds, and it shows configuration
// without ever showing the database URL — which carries the PostgreSQL
// password and is the one field the card is written by hand to exclude
// (ADR-0013).
func TestAdminOverview(t *testing.T) {
	ts, st := testServerCfg(t, func(c *config.Config) {
		c.Database.Driver = "postgres"
		c.Database.URL = adminTestDatabaseURL
	}, nil)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin")
	if code != 200 {
		t.Fatalf("admin overview: %d", code)
	}
	for _, want := range []string{
		"Overview", "This build", "Accounts", "Folders", "Configuration",
		"Database driver", "postgres",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("overview is missing %q", want)
		}
	}
	// Nested navigation reaches every page of the section.
	for _, want := range []string{
		`settings?section=admin&amp;view=users`,
		`settings?section=admin&amp;view=folders`,
		`settings?section=admin&amp;view=maintenance`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("subnav is missing %s", want)
		}
	}
	if strings.Contains(body, adminTestDatabaseURL) ||
		strings.Contains(strings.ToLower(body), "password=") {
		t.Fatal("the overview rendered the database connection string")
	}
}

func TestStaticAssets(t *testing.T) {
	ts, _ := testServer(t)
	for _, name := range []string{"style.css", "htmx.min.js"} {
		resp, err := http.Get(ts.URL + "/ui/static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("/ui/static/%s: want 200, got %d", name, resp.StatusCode)
		}
	}
}

func TestChangePassword(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings")
	csrf := extractCSRF(t, body)

	// Wrong current password.
	code, body := postForm(t, ts, cookie, "/ui/settings/password", url.Values{
		"current": {"wrong"}, "new": {"new-password-123"}, "repeat": {"new-password-123"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "Current password is wrong") {
		t.Fatalf("wrong current: %d", code)
	}

	// Mismatched new passwords.
	code, body = postForm(t, ts, cookie, "/ui/settings/password", url.Values{
		"current": {"hunter2hunter"}, "new": {"new-password-123"}, "repeat": {"different"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatch: %d", code)
	}

	// Happy path.
	code, body = postForm(t, ts, cookie, "/ui/settings/password", url.Values{
		"current": {"hunter2hunter"}, "new": {"new-password-123"}, "repeat": {"new-password-123"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "Password changed") {
		t.Fatalf("change: %d", code)
	}

	// Old password no longer works; new one does.
	u, _ := st.UserByID(t.Context(), "u1")
	ok, _ := auth.CheckPassword("hunter2hunter", u.Argon2Hash)
	if ok {
		t.Fatal("old password still valid")
	}
	ok, _ = auth.CheckPassword("new-password-123", u.Argon2Hash)
	if !ok {
		t.Fatal("new password not valid")
	}
}

func TestCrossUserIsolation(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	// Second user.
	hash, _ := auth.HashPassword("hunter2hunter")
	ub := store.User{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, ub); err != nil {
		t.Fatal(err)
	}
	// Alice has a work.
	if err := st.CreateWork(ctx,
		store.Work{ID: "wa", UserID: "u1", Title: "Alice's Book", CreatedAt: time.Now()},
		nil, []store.Identifier{{Kind: "sha256", Value: "aaaa"}}); err != nil {
		t.Fatal(err)
	}
	// Bob signs in.
	resp, _ := noRedirect().PostForm(ts.URL+"/ui/login", url.Values{
		"username": {"bob"}, "password": {"hunter2hunter"},
	})
	var bobCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			bobCookie = c
		}
	}
	resp.Body.Close()
	if bobCookie == nil {
		t.Fatal("bob login failed")
	}
	// Bob's library is empty; Alice's work 404s for him.
	_, body := page(t, ts, bobCookie, "/ui/library")
	if strings.Contains(body, "Alice's Book") {
		t.Fatal("cross-user leak in library")
	}
	code, _ := page(t, ts, bobCookie, "/ui/works/wa")
	if code != 404 {
		t.Fatalf("cross-user work page: want 404, got %d", code)
	}
}

// TestSecureTransportOnAllUIRoutes is a regression test: only the
// login POST used to be behind RequireSecureTransport, so every other
// session-cookie-bearing route accepted the credential over plain
// HTTP even when insecure_http was false.
func TestSecureTransportOnAllUIRoutes(t *testing.T) {
	ts, _ := testServerCfg(t, func(c *config.Config) { c.InsecureHTTP = false }, nil)
	for _, p := range []string{
		"/ui/", "/ui/login", "/ui/setup", "/ui/library", "/ui/settings",
		"/ui/settings?section=devices", "/ui/settings?section=admin",
		"/ui/library", "/ui/books/x", "/ui/books/x/download", "/ui/books/x/read",
		"/ui/search",
	} {
		if code, _ := page(t, ts, nil, p); code != http.StatusForbidden {
			t.Errorf("GET %s over plain HTTP: want 403, got %d", p, code)
		}
	}
	for _, p := range []string{
		"/ui/login", "/ui/setup", "/ui/logout", "/ui/tokens", "/ui/pairing", "/ui/koplugin",
		"/ui/tokens/example/scopes", "/ui/browsers/revoke",
		"/ui/settings", "/ui/settings/password",
		"/ui/admin/invites", "/ui/admin/users", "/ui/admin/users/u1/password",
		"/ui/admin/users/u1/admin", "/ui/admin/users/u1/disabled",
		"/ui/admin/users/u1/credentials/revoke",
		"/ui/admin/users/u1/tokens/t1/revoke", "/ui/admin/users/u1/kosync/s1/revoke",
		"/ui/admin/users/u1/koplugin/k1/revoke",
		"/ui/admin/folders", "/ui/admin/folders/f1/delete",
		"/ui/admin/folders/f1/scan",
		"/ui/admin/users/u1/tokens", "/ui/admin/users/u1/pairing",
		"/ui/admin/users/u1/koplugin", "/ui/admin/users/u1/backfill",
		"/ui/reader/token",
		"/ui/preferences",
	} {
		if code, _ := postForm(t, ts, nil, p, url.Values{}); code != http.StatusForbidden {
			t.Errorf("POST %s over plain HTTP: want 403, got %d", p, code)
		}
	}
	// Static assets carry no credential and must stay reachable so the
	// rejection page renders.
	if code, _ := page(t, ts, nil, "/ui/static/style.css"); code != http.StatusOK {
		t.Errorf("static asset: want 200, got %d", code)
	}
}

// TestSessionCookieSecureAttribute pins the fix for a cookie whose
// Secure flag came from r.TLS: behind a TLS-terminating proxy r.TLS is
// nil, so the session secret was sent in the clear.
func TestSessionCookieSecureAttribute(t *testing.T) {
	// insecure_http instances are plain HTTP by definition; a Secure
	// cookie would be dropped by the browser and lock the user out.
	ts, _ := testServer(t)
	if c := loginCookie(t, ts); c.Secure {
		t.Error("insecure_http instance: cookie must not be Secure")
	}

	// The proxy case: the backend speaks plain HTTP but the browser is
	// on HTTPS, so the cookie must still be Secure.
	ts2, _ := testServerCfg(t, func(c *config.Config) {
		c.InsecureHTTP = false
		c.TrustedProxies = []string{"127.0.0.0/8", "::1/128"}
	}, nil)
	req, _ := http.NewRequest("POST", ts2.URL+"/ui/login", strings.NewReader(
		url.Values{"username": {"alice"}, "password": {"hunter2hunter"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no session cookie behind proxy")
	}
	if !got.Secure {
		t.Error("cookie must be Secure when the instance requires HTTPS")
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteStrictMode {
		t.Error("cookie must stay HttpOnly and SameSite=Strict")
	}
}

// TestLoginFormRateLimited is a regression test: POST /v1/login was
// rate-limited but POST /ui/login was not, so the form was an
// unthrottled way around the API's limit.
func TestLoginFormRateLimited(t *testing.T) {
	ts, _ := testServerCfg(t, nil, func(s *Server) {
		s.LoginLimiter = auth.NewRateLimiter(3, time.Minute)
	})
	form := url.Values{"username": {"alice"}, "password": {"wrong"}}
	for i := range 3 {
		if code, _ := postForm(t, ts, nil, "/ui/login", form); code != http.StatusOK {
			t.Fatalf("attempt %d: want 200 with an error page, got %d", i+1, code)
		}
	}
	code, body := postForm(t, ts, nil, "/ui/login", form)
	if code != http.StatusTooManyRequests {
		t.Fatalf("fourth attempt: want 429, got %d", code)
	}
	if !strings.Contains(body, "too many attempts") {
		t.Errorf("limited response should render the login page: %s", body)
	}
	// The limiter must not become an authentication bypass.
	if code, _ := postForm(t, ts, nil, "/ui/login", url.Values{
		"username": {"alice"}, "password": {"hunter2hunter"},
	}); code == http.StatusSeeOther {
		t.Error("valid credentials accepted while rate limited")
	}
}

// TestLoginDoesNotLeakUserExistence pins the constant-work check: the
// unknown-user path returned before hashing anything, and argon2id at
// 64 MiB is slow enough that the gap is a usable enumeration oracle.
func TestLoginDoesNotLeakUserExistence(t *testing.T) {
	ts, _ := testServer(t)
	unknownCode, unknownBody := postForm(t, ts, nil, "/ui/login", url.Values{
		"username": {"nosuchuser"}, "password": {"hunter2hunter"},
	})
	knownCode, knownBody := postForm(t, ts, nil, "/ui/login", url.Values{
		"username": {"alice"}, "password": {"wrong"},
	})
	if unknownCode != knownCode || unknownBody != knownBody {
		t.Fatal("unknown user and wrong password must be indistinguishable")
	}

	timeOnce := func(username string) time.Duration {
		start := time.Now()
		postForm(t, ts, nil, "/ui/login", url.Values{
			"username": {username}, "password": {"hunter2hunter"},
		})
		return time.Since(start)
	}
	// Wall-clock timing is noisy, so this only catches the gross case
	// the bug produced: a near-instant return with no hashing at all.
	unknown, known := timeOnce("nosuchuser"), timeOnce("alice")
	if unknown < known/4 {
		t.Errorf("unknown user answered in %v vs %v for a known one; "+
			"the dummy hash check is not running", unknown, known)
	}
}

func TestWorkCardShowsBookCoverWhenMapped(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ctx := t.Context()

	now := time.Now().UTC()
	bookID := seedFolderBook(t, st, "lib1", "dune.epub", "Dune")

	work := store.Work{
		ID: "w1", UserID: "u1", Title: "Dune", CreatedAt: now,
	}
	if _, err := st.ResolveCatalogBookWork(ctx, "u1", bookID, work, nil, nil, true, now); err != nil {
		t.Fatal(err)
	}

	code, body := page(t, ts, cookie, "/ui/library")
	if code != 200 {
		t.Fatalf("works page: %d", code)
	}
	if !strings.Contains(body, "books/"+bookID+"/cover?size=thumbnail") {
		t.Fatalf("reading page does not render cover for mapped work:\n%s", body)
	}
}

// seedFolderBook puts one book in the catalog the only way books get
// there: a folder, and a pass over it that reports what it saw. The
// store mints the id, so the caller is told what it was.
func seedFolderBook(
	t *testing.T, st store.Store, folderID, relative, title string,
) string {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	if _, err := st.FolderByID(ctx, "", folderID); err != nil {
		if err := st.CreateFolder(ctx, store.Folder{
			ID: folderID, Name: "Test Folder", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if err := st.AssignUserFolder(ctx, user.ID, folderID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.ReconcileFolder(ctx, folderID, []store.ObservedBook{{
		RelativePath: relative, SizeBytes: 1024, MTime: now,
		ContentSHA256:    strings.Repeat("a", 64),
		OriginalFilename: relative, MediaType: "application/epub+zip",
		Title: title,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	books, err := st.ListCatalogBooks(ctx, "", folderID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.RelativePath == relative {
			return b.ID
		}
	}
	t.Fatalf("no catalogued book at %q", relative)
	return ""
}

// TestCreditStopsAtThree pins the one-line rule on a card: three names,
// then a count. A card is one line of small type, and an anthology with
// nineteen contributors would push the title off it.
func TestCreditStopsAtThree(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"Ann"}, "Ann"},
		{[]string{"Ann", "Bo", "Cy"}, "Ann, Bo, Cy"},
		{[]string{"Ann", "Bo", "Cy", "Dee"}, "Ann, Bo, Cy and one other"},
		{[]string{"Ann", "Bo", "Cy", "Dee", "Eli"}, "Ann, Bo, Cy and 2 others"},
	} {
		if got := credit(c.names); got != c.want {
			t.Errorf("credit(%v) = %q want %q", c.names, got, c.want)
		}
	}
}

// TestDevicesShowsTheOPDSAddress checks the catalog address is a URL a
// person can retype into a reading app, not a documentation placeholder.
// It is built from the request, so the page has to show the host the
// browser actually reached — a LAN address, a Tailscale name and a
// public hostname are all correct, and only the one in front of them is
// useful.
func TestDevicesShowsTheOPDSAddress(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")

	want := ts.URL + "/opds/v1.2"
	if !strings.Contains(body, want) {
		t.Fatalf("devices page does not show the OPDS address %q", want)
	}
	if strings.Contains(body, "&lt;host&gt;") {
		t.Fatal("devices page still shows a <host> placeholder")
	}
	// The koplugin address is built the same way and keeps only the
	// capability as a placeholder, since that one is per-device.
	if !strings.Contains(body, ts.URL+"/adapter/koplugin/") {
		t.Fatal("devices page does not show the koplugin server address")
	}
}

// TestSettingsRailAndAdminTab pins the account navigation after the
// account and instance controls were consolidated.
func TestSettingsRailAndAdminTab(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/library")

	if !strings.Contains(body, `href="settings"`) {
		t.Fatal("the rail does not point at Settings")
	}
	if strings.Contains(body, `href="admin`) {
		t.Fatal("the rail still exposes a separate Admin entry")
	}
	_, body = page(t, ts, cookie, "/ui/settings?section=admin")
	if !strings.Contains(body, `settings?section=admin&amp;view=folders`) {
		t.Fatal("the Settings Administration tab does not reach Folders")
	}
}
