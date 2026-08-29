package kosync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	cfg := config.Default()
	cfg.InsecureHTTP = true
	s := &Server{St: st, Cfg: cfg, AuthRateLim: auth.NewRateLimiter(1000, time.Minute)}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler {
		return auth.RequireSecureTransport(cfg, h)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st
}

// pairDevice runs the pairing flow and returns the x-auth headers
// KOReader would use. It sends what a real kosync client sends --
// md5(code), not the code -- so the conformance tests exercise the
// wire protocol rather than a convenient fiction.
func pairDevice(t *testing.T, ts *httptest.Server, st store.Store, userID, slot string) (user, key, pairingCode string) {
	t.Helper()
	// Admin generates a pairing code.
	pairingCode, _ = auth.NewSecret()
	pairingCode = pairingCode[:32]
	id, _ := auth.NewSecret()
	if err := st.CreatePairingCode(t.Context(), store.PairingCode{
		ID: id, UserID: userID, CodeSHA256: auth.KosyncPairingHash(pairingCode),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// KOReader derives MD5(password) and sends that as the password.
	key = auth.KosyncUserKey(pairingCode)
	body, _ := json.Marshal(map[string]string{"username": slot, "password": key})
	resp, err := http.Post(ts.URL+"/adapter/kosync/users/create", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("users/create: %d", resp.StatusCode)
	}

	// The same derived key is presented as x-auth-key thereafter.
	return slot, key, pairingCode
}

func kreq(t *testing.T, method, url, user, key string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("x-auth-user", user)
	req.Header.Set("x-auth-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPairingFlow(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}

	user, key, pairingCode := pairDevice(t, ts, st, u.ID, "my-kobo")

	// users/auth succeeds with the derived key.
	code, out := kreq(t, "GET", ts.URL+"/adapter/kosync/users/auth", user, key, nil)
	if code != 200 || out["authorized"] != "OK" {
		t.Fatalf("auth: %d %v", code, out)
	}

	// Wrong key rejected.
	code, _ = kreq(t, "GET", ts.URL+"/adapter/kosync/users/auth", user, "deadbeef", nil)
	if code != 401 {
		t.Fatalf("bad key: want 401, got %d", code)
	}

	// Pairing code is single-use.
	codeBody, _ := json.Marshal(map[string]string{"username": "other", "password": auth.KosyncUserKey(pairingCode)})
	resp, _ := http.Post(ts.URL+"/adapter/kosync/users/create", "application/json", bytes.NewReader(codeBody))
	if resp.StatusCode != 403 {
		t.Fatalf("bad pairing code: want 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	devices, err := st.ListKosyncDevices(t.Context(), u.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("single-use pairing created more than one device: %+v err=%v", devices, err)
	}
}

// TestPairingCodeWhitespaceTrimmed: a stray leading/trailing space or
// newline in the transmitted key must not defeat redemption.
func TestPairingCodeWhitespaceTrimmed(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	pairingCode, _ := auth.NewSecret()
	pairingCode = pairingCode[:32]
	id, _ := auth.NewSecret()
	if err := st.CreatePairingCode(t.Context(), store.PairingCode{
		ID: id, UserID: u.ID, CodeSHA256: auth.KosyncPairingHash(pairingCode),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"username": "kobo-ws", "password": "  " + auth.KosyncUserKey(pairingCode) + "\n",
	})
	resp, err := http.Post(ts.URL+"/adapter/kosync/users/create", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("whitespace-wrapped pairing code: want 201, got %d", resp.StatusCode)
	}
	devices, err := st.ListKosyncDevices(t.Context(), u.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("device not created from whitespace-wrapped code: %+v err=%v", devices, err)
	}
}

// TestClientSendsMD5DerivedKeyNotTheCode is the named regression test
// for the pairing bug this adapter shipped with: users/create hashed
// the value it received as if it were the pairing code the user typed,
// but no kosync client sends that. KOReader's plugin computes
// `userkey = md5(password)` and Readest's KOSyncClient does the same,
// so registration could never match a stored code and every pairing
// attempt returned 403 with the code left unredeemed.
//
// The code a human types must therefore never be accepted on the wire,
// and the md5-derived key must be.
func TestClientSendsMD5DerivedKeyNotTheCode(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}

	mint := func(t *testing.T) string {
		t.Helper()
		code, _ := auth.NewSecret()
		code = code[:32]
		id, _ := auth.NewSecret()
		if err := st.CreatePairingCode(t.Context(), store.PairingCode{
			ID: id, UserID: u.ID, CodeSHA256: auth.KosyncPairingHash(code),
			ExpiresAt: time.Now().Add(15 * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		return code
	}
	create := func(slot, sent string) int {
		body, _ := json.Marshal(map[string]string{"username": slot, "password": sent})
		resp, err := http.Post(ts.URL+"/adapter/kosync/users/create", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// The plaintext code is not what a client sends: it must not pair.
	if got := create("slot-plain", mint(t)); got != http.StatusForbidden {
		t.Fatalf("plaintext pairing code: want 403, got %d", got)
	}

	// The md5-derived key is what every kosync client sends.
	code := mint(t)
	if got := create("slot-md5", auth.KosyncUserKey(code)); got != http.StatusCreated {
		t.Fatalf("md5-derived key: want 201, got %d", got)
	}

	// And that same key is the credential presented thereafter.
	status, out := kreq(t, "GET", ts.URL+"/adapter/kosync/users/auth",
		"slot-md5", auth.KosyncUserKey(code), nil)
	if status != 200 || out["authorized"] != "OK" {
		t.Fatalf("auth with derived key: %d %v", status, out)
	}
}

func TestAccountPasswordCannotRegisterOrPair(t *testing.T) {
	ts, st := testServer(t)
	password := "hunter2hunter"
	hash, _ := auth.HashPassword(password)
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}

	for _, username := range []string{"alice", "new-account"} {
		// Both the raw account password and the md5-derived form a
		// real client would send must be refused.
		for _, sent := range []string{password, auth.KosyncUserKey(password)} {
			body, _ := json.Marshal(map[string]string{"username": username, "password": sent})
			resp, err := http.Post(ts.URL+"/adapter/kosync/users/create", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("account password for %q: want 403, got %d", username, resp.StatusCode)
			}
		}
	}
	if _, err := st.UserByName(t.Context(), "new-account"); err == nil {
		t.Fatal("kosync account password created an account")
	}
	devices, err := st.ListKosyncDevices(t.Context(), u.ID)
	if err != nil || len(devices) != 0 {
		t.Fatalf("kosync account password created a device: %+v err=%v", devices, err)
	}
}

func TestProgressRoundTrip(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	user, key, _ := pairDevice(t, ts, st, u.ID, "kobo-1")

	doc := "aabbccdd"
	xpointer := "/body/DocFragment[11]/body/div/p[3]/text()[1].0"

	// Push progress.
	code, _ := kreq(t, "PUT", ts.URL+"/adapter/kosync/syncs/progress", user, key, map[string]any{
		"document": doc, "progress": xpointer, "percentage": 0.4137,
		"device": "kobo-1", "timestamp": time.Now().Unix(),
	})
	if code != 200 {
		t.Fatalf("put progress: %d", code)
	}

	// Pull it back: xpointer must round-trip verbatim.
	code, out := kreq(t, "GET", ts.URL+"/adapter/kosync/syncs/progress/"+doc, user, key, nil)
	if code != 200 {
		t.Fatalf("get progress: %d", code)
	}
	if out["progress"] != xpointer {
		t.Fatalf("xpointer mangled: %q", out["progress"])
	}
	if out["percentage"].(float64) != 0.4137 {
		t.Fatalf("percentage: %v", out["percentage"])
	}
}

// TestFalsyZeroPercentage is the named regression test for the KoInsight
// falsy-zero bug: percentage 0 must be accepted and stored.
func TestFalsyZeroPercentage(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	user, key, _ := pairDevice(t, ts, st, u.ID, "kobo-2")

	code, _ := kreq(t, "PUT", ts.URL+"/adapter/kosync/syncs/progress", user, key, map[string]any{
		"document": "deadbeef01", "progress": "/body/DocFragment[1]/body/p[1]/text()[1].0",
		"percentage": 0, "device": "kobo-2", "timestamp": time.Now().Unix(),
	})
	if code != 200 {
		t.Fatalf("zero percentage rejected: %d", code)
	}
	_, out := kreq(t, "GET", ts.URL+"/adapter/kosync/syncs/progress/deadbeef01", user, key, nil)
	if out["percentage"].(float64) != 0 {
		t.Fatalf("zero percentage not stored: %v", out)
	}
}

// TestPendingWorkLaterResolves: a KOReader-first book creates a pending
// work; when a native client later resolves the same partial-md5 alias,
// the history merges into the resolved work.
func TestPendingWorkLaterResolves(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	user, key, _ := pairDevice(t, ts, st, u.ID, "kobo-3")

	doc := "cafe1234"
	kreq(t, "PUT", ts.URL+"/adapter/kosync/syncs/progress", user, key, map[string]any{
		"document": doc, "progress": "/x", "percentage": 0.2,
		"device": "kobo-3", "timestamp": time.Now().Unix(),
	})

	// Pending work exists, keyed on the alias.
	workID, err := st.WorkIDByAlias(ctx, u.ID, "partial-md5", doc)
	if err != nil {
		t.Fatal(err)
	}
	w, err := st.WorkByID(ctx, u.ID, workID)
	if err != nil || !w.Pending {
		t.Fatalf("want pending work, got %+v err=%v", w, err)
	}

	// Native resolve with sha256 + the same partial-md5: single hit on
	// the pending work, pending cleared, sha256 alias registered.
	_ = workID
	sha := "1234abcd"
	if err := st.AddAliases(ctx, u.ID, workID, []store.Identifier{{Kind: "sha256", Value: sha}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearPending(ctx, u.ID, workID); err != nil {
		t.Fatal(err)
	}
	w, _ = st.WorkByID(ctx, u.ID, workID)
	if w.Pending {
		t.Fatal("pending not cleared")
	}
	// History intact.
	pos, err := st.Positions(ctx, u.ID, workID, 10)
	if err != nil || len(pos) != 1 || pos[0].Progression != 0.2 {
		t.Fatalf("history lost: %+v err=%v", pos, err)
	}
	fmt.Println("pending work merged ok")
}

// TestNoOpenRoutes: adapter routes without credentials are rejected.
func TestNoOpenRoutes(t *testing.T) {
	ts, _ := testServer(t)
	for _, path := range []string{
		"/adapter/kosync/users/auth",
		"/adapter/kosync/syncs/progress/abc",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s: want 401, got %d", path, resp.StatusCode)
		}
	}
}
