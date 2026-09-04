package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// deviceInheritFixture: two users who can each log in, and a helper that
// mints a token for one through the API with an optional device_id.
func deviceInheritFixture(t *testing.T) (url string, st store.Store, login func(name string) string) {
	t.Helper()
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	for _, u := range []store.User{
		{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()},
		{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()},
	} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	login = func(name string) string {
		code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
			"username": name, "password": "hunter2hunter",
		})
		if code != http.StatusOK {
			t.Fatalf("login %s: %d %v", name, code, out)
		}
		return out["auth_token"].(string)
	}
	return ts.URL, st, login
}

func mintWithDevice(t *testing.T, url, login, deviceID string) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"name": "phone", "scopes": []string{"sync"}}
	if deviceID != "" {
		body["device_id"] = deviceID
	}
	return post(t, url+"/v1/tokens", login, body)
}

// TestTokenInheritsADeviceID: a client whose credential lapsed mints a
// replacement and asks for the device id it stored. The op log then sees
// one device, and an op it pushed before the lapse replays as a
// duplicate, not a conflict.
func TestTokenInheritsADeviceID(t *testing.T) {
	url, st, login := deviceInheritFixture(t)
	ctx := t.Context()

	code, first := mintWithDevice(t, url, login("alice"), "")
	if code != http.StatusCreated {
		t.Fatalf("first mint: %d %v", code, first)
	}
	device := first["device_id"].(string)
	firstSecret := first["secret"].(string)

	// Reading happens on the first credential.
	code, out := post(t, url+"/v1/works/resolve", firstSecret, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": "abc123"}},
		"title":       "A Book",
	})
	if code != http.StatusCreated {
		t.Fatalf("resolve: %d %v", code, out)
	}
	workID := out["work_id"].(string)
	op := map[string]any{
		"op_id": "op-1", "work_id": workID, "client_ts": "2026-01-01T00:00:00Z",
		"progression": 0.5,
	}
	if code, out = post(t, url+"/v1/ops", firstSecret, map[string]any{"ops": []any{op}}); code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}

	// The first credential dies; a revoked predecessor is still a valid
	// source of identity.
	toks, err := st.ListTokens(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeToken(ctx, "u1", toks[0].ID); err != nil {
		t.Fatal(err)
	}

	code, second := mintWithDevice(t, url, login("alice"), device)
	if code != http.StatusCreated {
		t.Fatalf("inheriting mint: %d %v", code, second)
	}
	if second["device_id"] != device {
		t.Fatalf("device not inherited: got %v want %s", second["device_id"], device)
	}
	secondSecret := second["secret"].(string)
	code, self := get(t, url+"/v1/token", secondSecret)
	if code != http.StatusOK || self["device_id"] != device {
		t.Fatalf("self-read after inherit: %d %v", code, self)
	}

	// The replay the client never got an answer for is a duplicate.
	code, out = post(t, url+"/v1/ops", secondSecret, map[string]any{"ops": []any{op}})
	if code != http.StatusOK {
		t.Fatalf("replay: %d %v", code, out)
	}
	res := out["results"].([]any)[0].(map[string]any)
	if res["status"] != "duplicate" {
		t.Fatalf("replay from the inherited device: want duplicate, got %v", res)
	}

	// Heads see one device.
	code, out = get(t, url+"/v1/heads", secondSecret)
	if code != http.StatusOK {
		t.Fatalf("heads: %d %v", code, out)
	}
	if n := len(out["ops"].([]any)); n != 1 {
		t.Fatalf("heads: want one device head, got %d: %v", n, out)
	}
}

// TestTokenDeviceIDIsARequestNotACredential: a device id the account has
// never carried, or one belonging to another account, is refused with a
// structured code, and an inherited id may be shared by two live tokens.
func TestTokenDeviceIDIsARequestNotACredential(t *testing.T) {
	url, _, login := deviceInheritFixture(t)

	code, out := mintWithDevice(t, url, login("alice"), "never-seen")
	if code != http.StatusBadRequest || out["code"] != "unknown_device" {
		t.Fatalf("unknown device: want 400 unknown_device, got %d %v", code, out)
	}

	code, bobs := mintWithDevice(t, url, login("bob"), "")
	if code != http.StatusCreated {
		t.Fatalf("bob's mint: %d %v", code, bobs)
	}
	code, out = mintWithDevice(t, url, login("alice"), bobs["device_id"].(string))
	if code != http.StatusBadRequest || out["code"] != "unknown_device" {
		t.Fatalf("another account's device: want 400 unknown_device, got %d %v", code, out)
	}

	// Two live tokens on one device: both work, both are that device.
	code, alices := mintWithDevice(t, url, login("alice"), "")
	if code != http.StatusCreated {
		t.Fatalf("alice's mint: %d %v", code, alices)
	}
	device := alices["device_id"].(string)
	code, twin := mintWithDevice(t, url, login("alice"), device)
	if code != http.StatusCreated || twin["device_id"] != device {
		t.Fatalf("live twin: %d %v", code, twin)
	}
	for _, secret := range []string{alices["secret"].(string), twin["secret"].(string)} {
		if code, self := get(t, url+"/v1/token", secret); code != http.StatusOK || self["device_id"] != device {
			t.Fatalf("twin self-read: %d %v", code, self)
		}
	}
}
