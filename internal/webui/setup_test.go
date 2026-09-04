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

// emptyServer is a freshly migrated instance with no accounts at all —
// the state every deployment starts in and that no other test covers,
// because every other helper plants alice first.
func emptyServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.InsecureHTTP = true
	s := &Server{St: st, Auth: auth.NewService(st), Cfg: cfg}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler {
		return auth.RequireSecureTransport(cfg, h)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st
}

// get performs a GET without following redirects, so a test can see
// where a handler sent the visitor rather than what it eventually
// rendered.
func get(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestFirstRunSetupOnboarding walks the whole first-run path: an empty
// instance points at setup, setup makes an administrator, and the
// visitor is signed in when it lands.
func TestFirstRunSetupOnboarding(t *testing.T) {
	ts, st := emptyServer(t)

	// An empty instance never shows a sign-in form: there is no
	// password that could work.
	resp, _ := get(t, ts, nil, "/ui/login")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "setup" {
		t.Fatalf("empty instance /ui/login: got %d %q",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	// So does the dashboard, via the sign-in redirect.
	resp, _ = get(t, ts, nil, "/ui/")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "login" {
		t.Fatalf("empty instance /ui/: got %d %q",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, body := get(t, ts, nil, "/ui/setup")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup page: got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Create administrator") {
		t.Fatalf("setup page does not offer the form:\n%s", body)
	}

	// Validation refusals re-render the form rather than half-creating.
	code, body := postForm(t, ts, nil, "/ui/setup", url.Values{
		"username": {"founder"}, "password": {"short"}, "repeat": {"short"},
	})
	if code != http.StatusOK || !strings.Contains(body, "at least") {
		t.Fatalf("short password: got %d\n%s", code, body)
	}
	code, body = postForm(t, ts, nil, "/ui/setup", url.Values{
		"username": {"founder"}, "password": {"hunter2hunter"}, "repeat": {"typo2typo9"},
	})
	if code != http.StatusOK || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatched repeat: got %d\n%s", code, body)
	}
	code, body = postForm(t, ts, nil, "/ui/setup", url.Values{
		"username": {"bad name!"}, "password": {"hunter2hunter"}, "repeat": {"hunter2hunter"},
	})
	if code != http.StatusOK || !strings.Contains(body, "may contain") {
		t.Fatalf("invalid name: got %d\n%s", code, body)
	}
	if users, _ := st.ListUsersPage(t.Context(), "", 5); len(users) != 0 {
		t.Fatalf("a refused setup created %d accounts", len(users))
	}

	// The real thing.
	req, _ := http.NewRequest("POST", ts.URL+"/ui/setup", strings.NewReader(url.Values{
		"username": {"founder"}, "password": {"hunter2hunter"}, "repeat": {"hunter2hunter"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup: got %d", resp.StatusCode)
	}
	base, _ := url.Parse(ts.URL + "/ui/setup")
	landed, err := base.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if landed.Path != "/ui/settings" ||
		landed.Query().Get("section") != settingsAdmin ||
		landed.Query().Get("view") != settingsAdminFolders ||
		landed.Query().Get(folderOnboardingQuery) != folderOnboardingValue {
		t.Fatalf("setup landed at %q, want the first-folder onboarding view",
			resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("setup did not sign the new administrator in")
	}

	u, err := st.UserByName(t.Context(), "founder")
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsAdmin {
		t.Fatal("the first account is not an administrator")
	}

	// Signed in, and the admin section is open to them.
	if code, _ := page(t, ts, cookie, "/ui/settings?section=admin"); code != http.StatusOK {
		t.Fatalf("admin overview after setup: got %d", code)
	}
	code, body = page(t, ts, cookie,
		"/ui/settings?section=admin&view=folders&onboarding=folder")
	if code != http.StatusOK ||
		!strings.Contains(body, `id="folder-dialog"`) ||
		!strings.Contains(body, `data-auto-open="true"`) ||
		!strings.Contains(body, "Add your book folder") {
		t.Fatalf("first-folder onboarding view is incomplete: %d\n%s", code, body)
	}
}

func TestFirstRunSetupWithExistingFolderKeepsDashboard(t *testing.T) {
	ts, st := emptyServer(t)
	if err := st.CreateFolder(t.Context(), store.Folder{
		ID: "existing-folder", Name: "Existing", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := noRedirect().PostForm(ts.URL+"/ui/setup", url.Values{
		"username": {"founder"}, "password": {"hunter2hunter"},
		"repeat": {"hunter2hunter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup: got %d", resp.StatusCode)
	}
	base, _ := url.Parse(ts.URL + "/ui/setup")
	landed, err := base.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if landed.Path != "/ui/" {
		t.Fatalf("setup landed at %q, want the dashboard", resp.Header.Get("Location"))
	}
}

// TestSetupClosesAfterFirstAccount pins the property that matters: once
// anybody has an account, the open endpoint that makes administrators
// is gone.
func TestSetupClosesAfterFirstAccount(t *testing.T) {
	ts, st := testServerCfg(t, nil, nil) // has alice

	resp, _ := get(t, ts, nil, "/ui/setup")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "login" {
		t.Fatalf("setup page on a used instance: got %d %q",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	code, _ := postForm(t, ts, nil, "/ui/setup", url.Values{
		"username": {"interloper"}, "password": {"hunter2hunter"},
		"repeat": {"hunter2hunter"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("setup POST on a used instance: got %d", code)
	}
	if _, err := st.UserByName(t.Context(), "interloper"); err == nil {
		t.Fatal("setup created an account on an instance that already had one")
	}
	// And a normal instance still shows the sign-in form.
	if code, body := page(t, ts, nil, "/ui/login"); code != http.StatusOK ||
		!strings.Contains(body, "Sign in") {
		t.Fatalf("sign-in page: got %d\n%s", code, body)
	}
}
