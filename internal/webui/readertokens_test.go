package webui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
)

// TestDevicesHidesReaderTokens is the whole point of the split. The
// reader mints a credential on every open and again every hour, so
// listing them beside a Boox's token turns the page into a log of the
// server talking to itself.
func TestDevicesHidesReaderTokens(t *testing.T) {
	ts, st := testServer(t)
	svc := auth.NewService(st)
	for range 3 {
		if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")

	if strings.Contains(body, auth.ReaderTokenName) {
		t.Fatalf("the tokens table still lists %q", auth.ReaderTokenName)
	}
	if !strings.Contains(body, "Browsers") {
		t.Fatal("no Browsers card")
	}
	if !strings.Contains(body, "Sign out of browser reading") {
		t.Fatal("no sign-out button for browser reading")
	}
}

// TestBrowsersAreOneRowPerBrowser: the device id is inherited across
// mints so that op log heads stay per browser, and the page has to
// report the same thing — one row, however many credentials it took.
func TestBrowsersAreOneRowPerBrowser(t *testing.T) {
	ts, st := testServer(t)
	svc := auth.NewService(st)
	var deviceID string
	for range 4 {
		_, tok, err := svc.MintReaderToken(t.Context(), "u1")
		if err != nil {
			t.Fatal(err)
		}
		deviceID = tok.DeviceID
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")

	if n := strings.Count(body, ">"+deviceID+"<"); n != 1 {
		t.Fatalf("four reader credentials rendered %d rows, want 1", n)
	}
}

// TestSignOutOfBrowserReadingRevokes checks the button does what it
// says: every live reader credential goes, so a browser left signed in
// on a machine you no longer have stops reading.
func TestSignOutOfBrowserReadingRevokes(t *testing.T) {
	ts, st := testServer(t)
	svc := auth.NewService(st)
	if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, body)

	if code, _ := postForm(t, ts, cookie, "/ui/browsers/revoke", url.Values{}); code != 403 {
		t.Fatalf("sign-out without CSRF: want 403, got %d", code)
	}
	code, body := postForm(t, ts, cookie, "/ui/browsers/revoke", url.Values{"csrf": {csrf}})
	if code != 200 || !strings.Contains(body, "signed out") {
		t.Fatalf("sign-out: %d", code)
	}

	toks, err := st.ListTokens(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		if tok.Name == auth.ReaderTokenName && tok.RevokedAt == nil {
			t.Fatal("a reader credential survived the sign-out")
		}
	}
}

// TestAdminUserPageCountsBrowsers: an admin looking at an account wants
// the credentials it was given and how many browsers it reads in, not a
// log of hourly refreshes.
func TestAdminUserPageCountsBrowsers(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	for range 3 {
		if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user=u1")
	if code != 200 {
		t.Fatalf("admin user page: %d", code)
	}
	if strings.Contains(body, auth.ReaderTokenName) {
		t.Fatalf("admin user page lists %q", auth.ReaderTokenName)
	}
	if !strings.Contains(body, "Reads in 1 browser.") {
		t.Fatal("admin user page does not count the browser")
	}
}
