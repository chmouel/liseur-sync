package api

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// tokenSelfFixture: a user, a login credential, and a service to mint
// bearer tokens with whatever scope set a case needs.
func tokenSelfFixture(t *testing.T) (url string, svc *auth.Service, userID, login string) {
	t.Helper()
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
		"username": "alice", "password": "hunter2hunter",
	})
	if code != http.StatusOK {
		t.Fatalf("login: %d %v", code, out)
	}
	return ts.URL, auth.NewService(st), u.ID, out["auth_token"].(string)
}

// TestTokenDescribesItselfWhateverItsScopes: the point of the route is
// that the narrowest credential can ask, so a sync-only token must get
// the same answer a four-scope one does — about itself.
func TestTokenDescribesItselfWhateverItsScopes(t *testing.T) {
	url, svc, userID, login := tokenSelfFixture(t)
	ctx := t.Context()

	for _, tc := range []struct {
		name   string
		scopes store.ScopeSet
		legacy any
	}{
		{"narrow", store.ScopeSet{store.ScopeSync}, "sync"},
		{"insights", store.ScopeSet{store.ScopeReadInsights}, "read-insights"},
		{"wide", store.ScopeSet{
			store.ScopeSync, store.ScopeLibraryRead, store.ScopeLibraryManage,
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secret, tok, err := svc.MintToken(ctx, userID, tc.name, tc.scopes, nil)
			if err != nil {
				t.Fatal(err)
			}
			code, out := get(t, url+"/v1/token", secret)
			if code != http.StatusOK {
				t.Fatalf("self-read with %v: %d %v", tc.scopes, code, out)
			}
			if out["id"] != tok.ID || out["device_id"] != tok.DeviceID ||
				out["name"] != tc.name {
				t.Fatalf("identity: %v (token %+v)", out, tok)
			}
			scopes, ok := out["scopes"].([]any)
			if !ok || len(scopes) != len(tc.scopes) {
				t.Fatalf("scopes: %v", out)
			}
			for i, want := range tc.scopes {
				if scopes[i] != string(want) {
					t.Fatalf("scopes[%d] = %v, want %s", i, scopes[i], want)
				}
			}
			// The deprecated scalar appears exactly when it can: a
			// single-scope token, never a multi-scope one.
			if tc.legacy == nil {
				if _, exists := out["scope"]; exists {
					t.Fatalf("multi-scope self-read exposed scalar scope: %v", out)
				}
			} else if out["scope"] != tc.legacy {
				t.Fatalf("scope = %v, want %v", out["scope"], tc.legacy)
			}

			// It agrees with what the account-wide listing says about
			// the same token: one shape, two routes.
			code, listing := get(t, url+"/v1/tokens", login)
			if code != http.StatusOK {
				t.Fatalf("listing: %d %v", code, listing)
			}
			var row map[string]any
			for _, entry := range listing["tokens"].([]any) {
				if e := entry.(map[string]any); e["id"] == tok.ID {
					row = e
				}
			}
			if row == nil {
				t.Fatalf("token %s missing from the listing: %v", tok.ID, listing)
			}
			for _, field := range []string{"id", "device_id", "name", "scope", "scopes"} {
				got, want := out[field], row[field]
				if field == "scopes" {
					if len(got.([]any)) != len(want.([]any)) {
						t.Fatalf("scopes disagree: %v vs %v", got, want)
					}
					continue
				}
				if got != want {
					t.Fatalf("%s disagrees: self %v, listing %v", field, got, want)
				}
			}
		})
	}
}

// TestTokenRevealsNoSecretAndNoOtherToken: the response is a description
// of a credential, not a copy of one, and it stops at the caller.
func TestTokenRevealsNoSecretAndNoOtherToken(t *testing.T) {
	url, svc, userID, _ := tokenSelfFixture(t)
	ctx := t.Context()
	secret, tok, err := svc.MintToken(ctx, userID, "asking", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherSecret, other, err := svc.MintToken(ctx, userID, "sibling",
		store.ScopeSet{store.ScopeLibraryRead}, nil)
	if err != nil {
		t.Fatal(err)
	}

	code, out := get(t, url+"/v1/token", secret)
	if code != http.StatusOK {
		t.Fatalf("self-read: %d %v", code, out)
	}
	for _, forbidden := range []string{
		"secret", "token", "auth_token", "sha256", "hash", "user_id", "tokens",
	} {
		if _, exists := out[forbidden]; exists {
			t.Fatalf("self-read exposed %q: %v", forbidden, out)
		}
	}
	for field, value := range out {
		s, ok := value.(string)
		if !ok {
			continue
		}
		if s == secret || s == otherSecret || s == tok.SHA256 || s == other.ID {
			t.Fatalf("field %s leaked a secret or a sibling token: %v", field, out)
		}
	}

	// The sibling describes itself, not the account.
	code, out = get(t, url+"/v1/token", otherSecret)
	if code != http.StatusOK || out["id"] != other.ID {
		t.Fatalf("sibling self-read: %d %v", code, out)
	}
}

// TestTokenSelfReadNeedsALiveCredential: absent, revoked and expired all
// get 401. Declaring the route scope-free must not make it open.
func TestTokenSelfReadNeedsALiveCredential(t *testing.T) {
	url, svc, userID, login := tokenSelfFixture(t)
	ctx := t.Context()

	if code, out := get(t, url+"/v1/token", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous self-read: %d %v", code, out)
	}
	if code, out := get(t, url+"/v1/token", "not-a-token"); code != http.StatusUnauthorized {
		t.Fatalf("bogus secret: %d %v", code, out)
	}

	expired := time.Now().UTC().Add(-time.Hour)
	stale, _, err := svc.MintToken(ctx, userID, "stale", store.ScopeSet{store.ScopeSync}, &expired)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := get(t, url+"/v1/token", stale); code != http.StatusUnauthorized {
		t.Fatalf("expired self-read: %d %v", code, out)
	}

	secret, tok, err := svc.MintToken(ctx, userID, "doomed", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := get(t, url+"/v1/token", secret); code != http.StatusOK {
		t.Fatalf("live self-read: %d %v", code, out)
	}
	code, out := requestJSON(t, http.MethodDelete, url+"/v1/tokens/"+tok.ID, login, nil)
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("revoke: %d %v", code, out)
	}
	if code, out := get(t, url+"/v1/token", secret); code != http.StatusUnauthorized {
		t.Fatalf("revoked self-read: %d %v", code, out)
	}
}

// TestTokenSelfReadIsReadOnly: introspection cannot become escalation.
// A bearer that can describe its scopes must not be able to change them,
// and the singular route offers no verb but GET — with HEAD served as
// that GET's status line, which is what makes it a cheap liveness check
// for a stored credential rather than a hole in the wall.
func TestTokenSelfReadIsReadOnly(t *testing.T) {
	url, svc, userID, _ := tokenSelfFixture(t)
	ctx := t.Context()
	secret, tok, err := svc.MintToken(ctx, userID, "curious", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{
		http.MethodPost, http.MethodPatch, http.MethodPut,
		http.MethodDelete, http.MethodOptions,
	} {
		code, _ := requestJSON(t, method, url+"/v1/token", secret,
			map[string]any{"scopes": []string{"admin"}})
		if code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /v1/token: want 405, got %d", method, code)
		}
	}

	// HEAD is authenticated like the GET it mirrors: a body-less 200 for
	// a live credential, 401 for none. It must not become a way to reach
	// the route without proving anything.
	resp := headRequest(t, url+"/v1/token", secret)
	if resp.code != http.StatusOK || resp.body != 0 {
		t.Fatalf("HEAD with a live token: %d, %d body bytes", resp.code, resp.body)
	}
	if anon := headRequest(t, url+"/v1/token", ""); anon.code != http.StatusUnauthorized {
		t.Fatalf("anonymous HEAD: want 401, got %d", anon.code)
	}

	// The plural route still refuses a bearer token: widening scopes
	// remains a login-credential act.
	if code, _ := patch(t, url+"/v1/tokens/"+tok.ID, secret,
		map[string]any{"scopes": []string{"admin"}}); code != http.StatusUnauthorized {
		t.Fatalf("bearer widening its own scopes: want 401, got %d", code)
	}
	code, out := get(t, url+"/v1/token", secret)
	if code != http.StatusOK {
		t.Fatalf("self-read after the attempts: %d %v", code, out)
	}
	if scopes := out["scopes"].([]any); len(scopes) != 1 || scopes[0] != "sync" {
		t.Fatalf("scopes changed under the token: %v", out)
	}

	// Everything about the credential's authority survived the attempts.
	// Its last-used timestamp moving is not authority: every bearer route
	// writes that, and a route that did not would be the one way to use a
	// token without leaving a trace.
	after, err := svc.St.ListTokens(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var now store.Token
	for _, candidate := range after {
		if candidate.ID == tok.ID {
			now = candidate
		}
	}
	if now.SHA256 != tok.SHA256 || now.DeviceID != tok.DeviceID ||
		now.RevokedAt != nil || now.ExpiresAt != nil ||
		len(now.Scopes) != 1 || now.Scopes[0] != store.ScopeSync {
		t.Fatalf("token authority changed: %+v, was %+v", now, tok)
	}
}

type headResult struct {
	code int
	body int
}

func headRequest(t *testing.T, url, token string) headResult {
	t.Helper()
	req, _ := http.NewRequest(http.MethodHead, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return headResult{code: resp.StatusCode, body: len(b)}
}
