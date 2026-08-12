package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestTokenScopeSetCompatibilityAndUpdate(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(ctx, store.User{
		ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
		"username": "alice", "password": "hunter2hunter",
	})
	if code != http.StatusOK {
		t.Fatalf("login: %d %v", code, out)
	}
	login := out["auth_token"].(string)

	// The scalar request remains accepted and receives both wire fields.
	code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "legacy", "scope": "sync",
	})
	if code != http.StatusCreated || out["scope"] != "sync" {
		t.Fatalf("legacy token create: %d %v", code, out)
	}
	if scopes, ok := out["scopes"].([]any); !ok || len(scopes) != 1 || scopes[0] != "sync" {
		t.Fatalf("legacy scopes response: %v", out)
	}

	// A scope array is deduplicated and returned in canonical order. Multi-
	// scope responses omit the deprecated scalar field.
	code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "reader", "scopes": []string{"library-read", "sync", "library-read"},
	})
	if code != http.StatusCreated {
		t.Fatalf("multi-scope token create: %d %v", code, out)
	}
	if _, exists := out["scope"]; exists {
		t.Fatalf("multi-scope response exposed scalar scope: %v", out)
	}
	scopes := out["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "sync" || scopes[1] != "library-read" {
		t.Fatalf("canonical scopes response: %v", out)
	}
	tokenID := out["token_id"].(string)
	deviceID := out["device_id"].(string)
	secret := out["secret"].(string)

	// Equal scalar and array forms may coexist; contradictory forms fail.
	if code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "same", "scope": "sync", "scopes": []string{"sync", "sync"},
	}); code != http.StatusCreated {
		t.Fatalf("equivalent compatibility fields: %d %v", code, out)
	}
	if code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "conflict", "scope": "sync", "scopes": []string{"read-insights"},
	}); code != http.StatusBadRequest ||
		out["error"] != "scope and scopes must describe the same set" {
		t.Fatalf("contradictory compatibility fields: %d %v", code, out)
	}
	if code, _ = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "empty", "scopes": []string{},
	}); code != http.StatusBadRequest {
		t.Fatalf("empty scope set: want 400, got %d", code)
	}
	if code, out = patch(t, ts.URL+"/v1/tokens/"+tokenID, login, map[string]any{
		"scopes": []string{"sync", "admin"},
	}); code != http.StatusForbidden {
		t.Fatalf("scope update self-granted admin: want 403, got %d %v", code, out)
	}

	// In-place updates preserve the token secret and device identity.
	code, out = patch(t, ts.URL+"/v1/tokens/"+tokenID, login, map[string]any{
		"scopes": []string{"read-insights", "library-manage"},
	})
	if code != http.StatusOK {
		t.Fatalf("scope update: %d %v", code, out)
	}
	if _, exists := out["scope"]; exists {
		t.Fatalf("multi-scope update exposed scalar scope: %v", out)
	}
	updatedScopes := out["scopes"].([]any)
	if len(updatedScopes) != 2 ||
		updatedScopes[0] != "read-insights" ||
		updatedScopes[1] != "library-manage" {
		t.Fatalf("scope update response: %v", out)
	}
	if code, _ = get(t, ts.URL+"/v1/insights/summary", secret); code != http.StatusOK {
		t.Fatalf("updated secret lost read-insights access: %d", code)
	}
	if code, _ = get(t, ts.URL+"/v1/changes?since=0", secret); code != http.StatusForbidden {
		t.Fatalf("removed sync scope still authorized: %d", code)
	}
	code, out = get(t, ts.URL+"/v1/tokens", login)
	if code != http.StatusOK {
		t.Fatalf("list tokens: %d %v", code, out)
	}
	var updated map[string]any
	for _, raw := range out["tokens"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["id"] == tokenID {
			updated = candidate
			break
		}
	}
	if updated == nil || updated["device_id"] != deviceID {
		t.Fatalf("scope update changed token identity: %v", out)
	}
	if _, exists := updated["scope"]; exists {
		t.Fatalf("listed multi-scope token exposed scalar scope: %v", updated)
	}
}
