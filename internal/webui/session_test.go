package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// mintSession writes a web session with a chosen expiry and hands back
// the cookie a browser holding it would send. Going through the store
// rather than through the login form is the only way to test a session
// that is already old.
func mintSession(t *testing.T, st store.Store, id, secret string, expires time.Time) *http.Cookie {
	t.Helper()
	if err := st.CreateAuthSession(t.Context(), store.AuthSession{
		ID: id, UserID: "u1", SHA256: auth.HashSecret(secret), Kind: "web",
		CSRFHash:  auth.HashSecret("csrf-" + id),
		CreatedAt: time.Now(), ExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: cookieName, Value: secret}
}

// getWithCookie returns the response itself, because what this file is
// about is a header rather than a body.
func getWithCookie(t *testing.T, ts *httptest.Server, c *http.Cookie, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.AddCookie(c)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func sessionExpiry(t *testing.T, st store.Store, secret string) time.Time {
	t.Helper()
	a, err := st.AuthSessionByHash(t.Context(), auth.HashSecret(secret))
	if err != nil {
		t.Fatal(err)
	}
	return a.ExpiresAt
}

// TestSessionLifetimeIsConfigured pins the window a fresh sign-in gets,
// on the cookie and on the row alike, and that the setting is what
// decides it.
func TestSessionLifetimeIsConfigured(t *testing.T) {
	ts, st := testServer(t)
	c := loginCookie(t, ts)
	want := time.Now().Add(config.Default().WebSessionTTL())
	if d := c.Expires.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("cookie expiry %v, want about %v", c.Expires, want)
	}
	if got := sessionExpiry(t, st, c.Value); got.Sub(want) > time.Minute {
		t.Fatalf("stored expiry %v, want about %v", got, want)
	}

	short, _ := testServerCfg(t, func(cfg *config.Config) { cfg.WebSessionTTLDays = 2 }, nil)
	c = loginCookie(t, short)
	want = time.Now().Add(2 * 24 * time.Hour)
	if d := c.Expires.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("short cookie expiry %v, want about %v", c.Expires, want)
	}
}

// TestSessionSlidesOnUse is the point of the long window: a browser
// somebody reads in keeps its session, and does not pay a write per
// page view for it.
func TestSessionSlidesOnUse(t *testing.T) {
	ts, st := testServer(t)
	ttl := config.Default().WebSessionTTL()

	// A session near the end of its life is pushed back out, in the
	// cookie and in the store.
	old := mintSession(t, st, "old", "old-secret", time.Now().Add(time.Hour))
	resp := getWithCookie(t, ts, old, "/ui/library")
	if resp.StatusCode != 200 {
		t.Fatalf("library: %d", resp.StatusCode)
	}
	got := sessionCookie(resp)
	if got == nil {
		t.Fatal("a stale session was not renewed")
	}
	want := time.Now().Add(ttl)
	if d := got.Expires.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("renewed cookie expiry %v, want about %v", got.Expires, want)
	}
	if stored := sessionExpiry(t, st, "old-secret"); stored.Sub(want) > time.Minute ||
		want.Sub(stored) > time.Minute {
		t.Fatalf("renewed row expiry %v, want about %v", stored, want)
	}
	if got.Value != old.Value {
		t.Fatal("renewal reissued the session secret instead of extending it")
	}

	// A session renewed moments ago is left alone: the expiry would
	// move by less than a day, which is not worth a write.
	fresh := mintSession(t, st, "fresh", "fresh-secret", time.Now().Add(ttl-time.Hour))
	resp = getWithCookie(t, ts, fresh, "/ui/library")
	if resp.StatusCode != 200 {
		t.Fatalf("library: %d", resp.StatusCode)
	}
	if c := sessionCookie(resp); c != nil {
		t.Fatalf("a fresh session was rewritten: %+v", c)
	}
}

// TestSignedOutSessionIsNotRenewed keeps renewal on the authenticated
// side of the fence: a session that no longer signs anybody in must not
// be handed a new expiry on its way to the login page.
func TestSignedOutSessionIsNotRenewed(t *testing.T) {
	ts, st := testServer(t)
	c := mintSession(t, st, "revoked", "revoked-secret", time.Now().Add(time.Hour))
	if err := st.RevokeAuthSession(t.Context(), "u1", "revoked"); err != nil {
		t.Fatal(err)
	}
	resp := getWithCookie(t, ts, c, "/ui/library")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoked session: want a bounce to login, got %d", resp.StatusCode)
	}
	if got := sessionCookie(resp); got != nil {
		t.Fatalf("revoked session was renewed: %+v", got)
	}
}
