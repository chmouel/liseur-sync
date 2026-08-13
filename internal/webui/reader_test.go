package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// mintReaderToken drives the endpoint the way the reader page will:
// session cookie plus the CSRF token lifted from a rendered page.
func mintReaderToken(t *testing.T, ts *httptest.Server, cookie *http.Cookie) readerTokenResponse {
	t.Helper()
	_, body := page(t, ts, cookie, "/ui/settings")
	csrf := extractCSRF(t, body)
	code, out := postForm(t, ts, cookie, "/ui/reader/token", url.Values{"csrf": {csrf}})
	if code != 200 {
		t.Fatalf("mint reader token: want 200, got %d (%s)", code, out)
	}
	var got readerTokenResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("reader token response is not JSON: %v (%s)", err, out)
	}
	return got
}

// listTokens returns every token alice holds, so a test can inspect
// what was actually persisted rather than trusting the response body.
func listTokens(t *testing.T, st store.Store) []store.Token {
	t.Helper()
	u, err := st.UserByName(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	toks, err := st.ListTokens(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return toks
}

// TestReaderTokenIsNarrowAndShortLived pins the three properties
// ADR-0007 relies on for the browser reader's credential: it can read
// the catalog and sync, it can do nothing else, and it expires.
func TestReaderTokenIsNarrowAndShortLived(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	got := mintReaderToken(t, ts, cookie)

	if strings.Join(got.Scopes, ",") != "sync,library-read" {
		t.Errorf("reader scopes: want exactly sync,library-read, got %v", got.Scopes)
	}
	if got.Token == "" || got.DeviceID == "" {
		t.Error("reader token response must carry both a secret and a device id")
	}
	if got.ExpiresAt == "" {
		t.Error("a reader token with no expiry is a permanent credential minted by a page load")
	}

	// The stored side: hashed, scoped, and expiring.
	toks := listTokens(t, st)
	var reader *store.Token
	for i := range toks {
		if toks[i].Name == auth.ReaderTokenName {
			reader = &toks[i]
		}
	}
	if reader == nil {
		t.Fatal("no reader token was stored")
	}
	if reader.ExpiresAt == nil {
		t.Fatal("stored reader token has no expiry")
	}
	if reader.Scopes.Allows(store.ScopeLibraryManage) || reader.Scopes.Allows(store.ScopeAdmin) {
		t.Error("a reader token must not be able to manage or administer a library")
	}
	if reader.Scopes.Allows(store.ScopeReadInsights) {
		t.Error("the reader needs positions, not other people's statistics")
	}
	if strings.Contains(reader.SHA256, got.Token) {
		t.Error("the token secret must be stored hashed, never verbatim")
	}
}

// TestReaderTokenKeepsOneDevicePerBrowser is the op-log property: heads
// are per work and device, so re-minting must not invent a new device
// or one person reading one book grows a head per hour and per tab.
func TestReaderTokenKeepsOneDevicePerBrowser(t *testing.T) {
	ts, st := testServer(t)

	first := mintReaderToken(t, ts, loginCookie(t, ts))
	// A second, independent sign-in: a different tab, or tomorrow.
	second := mintReaderToken(t, ts, loginCookie(t, ts))

	if first.DeviceID != second.DeviceID {
		t.Errorf("device id changed between mints: %q then %q", first.DeviceID, second.DeviceID)
	}
	if first.Token == second.Token {
		t.Error("each mint must be a fresh secret; only the device identity is reused")
	}
	// The earlier token stays usable: a mint that revoked its
	// predecessor would let two open tabs invalidate each other.
	for _, tok := range listTokens(t, st) {
		if tok.Name == auth.ReaderTokenName && tok.RevokedAt != nil {
			t.Error("minting a reader token must not revoke a live one")
		}
	}
}

// TestReaderTokenRequiresCSRF: the endpoint hands out a working sync
// credential, so a cross-site POST riding the session cookie must not
// be able to collect one.
func TestReaderTokenRequiresCSRF(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	if code, _ := postForm(t, ts, cookie, "/ui/reader/token", url.Values{}); code != 403 {
		t.Errorf("no CSRF token: want 403, got %d", code)
	}
	if code, _ := postForm(t, ts, cookie, "/ui/reader/token", url.Values{"csrf": {"wrong"}}); code != 403 {
		t.Errorf("bad CSRF token: want 403, got %d", code)
	}
	// Unauthenticated: redirected to login, never issued a credential.
	if code, _ := postForm(t, ts, nil, "/ui/reader/token", url.Values{}); code != 303 {
		t.Errorf("no session: want a redirect to login, got %d", code)
	}
	for _, tok := range listTokens(t, st) {
		if tok.Name == auth.ReaderTokenName {
			t.Fatal("a refused request minted a token anyway")
		}
	}
}

// TestSigningOutEndsBrowserReading: a token derived from a session must
// not outlive it, or signing out leaves working access behind.
func TestSigningOutEndsBrowserReading(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	mintReaderToken(t, ts, cookie)
	_, body := page(t, ts, cookie, "/ui/settings")
	csrf := extractCSRF(t, body)

	if code, _ := postForm(t, ts, cookie, "/ui/logout", url.Values{"csrf": {csrf}}); code != 303 {
		t.Fatalf("logout: want 303, got %d", code)
	}

	found := false
	for _, tok := range listTokens(t, st) {
		if tok.Name != auth.ReaderTokenName {
			continue
		}
		found = true
		if tok.RevokedAt == nil {
			t.Error("reader token survived the sign-out that authorised it")
		}
	}
	if !found {
		t.Fatal("test did not exercise a reader token")
	}
}
