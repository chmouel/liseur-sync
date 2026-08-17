package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The shell and its preferences (ADR-0011). These are the tests that
// keep the revamp's two non-cosmetic claims honest: the theme survives a
// reload without a script, and a preference toggle cannot be turned into
// a redirector.

func TestShellRendersRailAndTheme(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, body := page(t, ts, cookie, "/ui/library")
	for _, want := range []string{
		`data-theme="dark"`, // dark-first, rendered by the server
		`class="rail"`,
		`class="topbar"`,
		`aria-current="page"`, // the rail knows where it is
		`class="skip"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell missing %q", want)
		}
	}
	// The section marker must be on the Library link, not just anywhere.
	i := strings.Index(body, `aria-current="page"`)
	if i < 0 || !strings.Contains(body[i:min(i+40, len(body))], "Library") {
		t.Error("aria-current is not on the current section")
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	for _, theme := range []string{themeLight, themeTokyoNight, themeRosePine} {
		t.Run(theme, func(t *testing.T) {
			_, body := page(t, ts, cookie, "/ui/library")
			csrf := extractCSRF(t, body)

			code, _ := postForm(t, ts, cookie, "/ui/preferences", url.Values{
				"csrf": {csrf}, "theme": {theme}, "back": {"books"},
			})
			if code != http.StatusSeeOther {
				t.Fatalf("set theme: want 303, got %d", code)
			}
			pref := prefCookie(t, ts, cookie, csrf, url.Values{
				"csrf": {csrf}, "theme": {theme}, "back": {"books"},
			})
			if !strings.Contains(pref.Value, theme) {
				t.Fatalf("cookie did not record the theme: %q", pref.Value)
			}

			// And the next page renders in it, with no script involved.
			req, _ := http.NewRequest("GET", ts.URL+"/ui/library", nil)
			req.AddCookie(cookie)
			req.AddCookie(pref)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			buf := make([]byte, 4096)
			n, _ := resp.Body.Read(buf)
			if !strings.Contains(string(buf[:n]), `data-theme="`+theme+`"`) {
				t.Errorf("theme cookie did not reach the root element")
			}
		})
	}
}

func TestThemeCycleIncludesNamedThemes(t *testing.T) {
	t.Parallel()

	got := themeDark
	for _, want := range []string{
		themeLight, themeSystem, themeTokyoNight, themeRosePine, themeDark,
	} {
		got = nextTheme(got)
		if got != want {
			t.Fatalf("nextTheme: got %q, want %q", got, want)
		}
	}
}

// prefCookie repeats the POST and returns the preference cookie it set,
// because postForm only hands back the body.
func prefCookie(
	t *testing.T, ts *httptest.Server, session *http.Cookie, _ string, form url.Values,
) *http.Cookie {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/ui/preferences", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == prefsCookie {
			return c
		}
	}
	t.Fatal("no preference cookie set")
	return nil
}

func TestPreferencesRejectsForgeryAndOpenRedirect(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/library")
	csrf := extractCSRF(t, body)

	if code, _ := postForm(t, ts, cookie, "/ui/preferences", url.Values{
		"theme": {"light"},
	}); code != http.StatusForbidden {
		t.Errorf("missing csrf: want 403, got %d", code)
	}

	// Anything that could leave this UI is refused and replaced with
	// the UI root, so a toggle can never be a redirector.
	for _, bad := range []string{
		"https://evil.example/", "//evil.example/", "/etc/passwd", `\\evil.example`,
	} {
		req, _ := http.NewRequest("POST", ts.URL+"/ui/preferences", strings.NewReader(
			url.Values{"csrf": {csrf}, "theme": {"dark"}, "back": {bad}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		resp, err := noRedirect().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if loc := resp.Header.Get("Location"); loc != "." {
			t.Errorf("back=%q redirected to %q, want %q", bad, loc, ".")
		}
	}
}

// A cookie is user input. An unknown theme is somebody's typing, not a
// value to paste into an attribute.
func TestPreferenceCookieIsNotTrusted(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	req, _ := http.NewRequest("GET", ts.URL+"/ui/library", nil)
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{
		Name:  prefsCookie,
		Value: `"><script>alert(1)</script>.grid`,
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	head := string(buf[:n])
	if !strings.Contains(head, `data-theme="dark"`) {
		t.Error("unknown theme did not fall back to the default")
	}
	if strings.Contains(head, "<script>alert") {
		t.Fatal("cookie contents reached the document")
	}
}

// The top bar searches without knowing about libraries; it resolves one.
func TestTopSearchResolvesALibrary(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	req, _ := http.NewRequest("GET", ts.URL+"/ui/search?q=whale", nil)
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("top search: want 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.HasPrefix(loc, "/") || strings.Contains(loc, "://") {
		t.Fatalf("top search redirect is not relative: %q", loc)
	}
	if !strings.Contains(loc, "q=whale") && !strings.HasPrefix(loc, "library") {
		t.Fatalf("top search lost the query: %q", loc)
	}
}

// The two routes the revamp added are ordinary UI routes: signed in, or
// sent to the login page like everything else.
func TestNewRoutesRequireASession(t *testing.T) {
	ts, _ := testServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/ui/search?q=x", nil)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "login" {
		t.Errorf("unauth /ui/search: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	code, _ := postForm(t, ts, nil, "/ui/preferences", url.Values{"theme": {"light"}})
	if code != http.StatusSeeOther {
		t.Errorf("unauth POST /ui/preferences: want a redirect to login, got %d", code)
	}
}
