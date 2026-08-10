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
	s := &Server{St: st, Auth: auth.NewService(st)}
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
	resp, _ = noRedirect().Get(ts.URL + "/ui/works")
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

	for _, path := range []string{"/ui", "/ui/", "/ui/works", "/ui/devices", "/ui/settings"} {
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
	// Admin page is forbidden without an admin token.
	code, _ = page(t, ts, cookie, "/ui/admin")
	if code != 403 {
		t.Fatalf("admin without admin token: want 403, got %d", code)
	}
}

func TestDevicesCRUDAndCSRF(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/devices")
	csrf := extractCSRF(t, body)

	// Mutation without CSRF is rejected.
	code, _ := postForm(t, ts, cookie, "/ui/tokens", url.Values{"name": {"x"}, "scope": {"sync"}})
	if code != 403 {
		t.Fatalf("no-CSRF token create: want 403, got %d", code)
	}

	// Create a token with CSRF.
	code, body = postForm(t, ts, cookie, "/ui/tokens", url.Values{
		"name": {"Test Dev"}, "scope": {"sync"}, "csrf": {csrf},
	})
	if code != 200 || !strings.Contains(body, "shown once") {
		t.Fatalf("token create: %d", code)
	}

	// The new token appears in the store.
	toks, _ := st.ListTokens(t.Context(), "u1")
	if len(toks) != 1 || toks[0].Name != "Test Dev" {
		t.Fatalf("tokens: %+v", toks)
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
	ts, st := testServer(t)
	// Grant the user an admin token so admin pages unlock.
	svc := auth.NewService(st)
	if _, _, err := svc.MintToken(t.Context(), "u1", "adm", store.ScopeAdmin, nil); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin")
	if code != 200 || !strings.Contains(body, "Invite codes") {
		t.Fatalf("admin page: %d", code)
	}
	csrf := extractCSRF(t, body)
	code, body = postForm(t, ts, cookie, "/ui/admin/invites", url.Values{"csrf": {csrf}})
	if code != 200 || !strings.Contains(body, "Invite code") {
		t.Fatalf("invite create: %d", code)
	}
	invs, _ := st.ListInvites(t.Context(), "u1")
	if len(invs) != 1 {
		t.Fatalf("invites: %+v", invs)
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
	_, body := page(t, ts, bobCookie, "/ui/works")
	if strings.Contains(body, "Alice's Book") {
		t.Fatal("cross-user leak in library")
	}
	code, _ := page(t, ts, bobCookie, "/ui/works/wa")
	if code != 404 {
		t.Fatalf("cross-user work page: want 404, got %d", code)
	}
}
