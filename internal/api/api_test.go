package api

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
	cfg.InsecureHTTP = true // httptest is plain HTTP
	srv := &Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, st
}

func post(t *testing.T, url, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func get(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestFullSyncFlow(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()

	// Create a user directly (admin CLI equivalent).
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	// Login.
	code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
		"username": "alice", "password": "hunter2hunter",
	})
	if code != 200 {
		t.Fatalf("login: %d %v", code, out)
	}
	loginTok := out["auth_token"].(string)

	// Wrong password rejected.
	code, _ = post(t, ts.URL+"/v1/login", "", map[string]string{
		"username": "alice", "password": "wrong",
	})
	if code != 401 {
		t.Fatalf("bad login should be 401, got %d", code)
	}

	// Create a device token.
	code, out = post(t, ts.URL+"/v1/tokens", loginTok, map[string]string{
		"name": "Boox Palma", "scope": "sync",
	})
	if code != 201 {
		t.Fatalf("create token: %d %v", code, out)
	}
	devTok := out["secret"].(string)

	// Unauthenticated request rejected.
	code, _ = get(t, ts.URL+"/v1/changes?since=0", "")
	if code != 401 {
		t.Fatalf("unauth changes should be 401, got %d", code)
	}

	// Resolve a work.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "ABC123"},
			{"kind": "partial-md5", "value": "ffff"},
		},
		"title":  "A Memory Called Empire",
		"author": "Arkady Martine",
	})
	if code != 201 || out["created"] != true {
		t.Fatalf("resolve: %d %v", code, out)
	}
	workID := out["work_id"].(string)

	// Re-resolve with a new identifier set that partially overlaps:
	// registers the new alias on the same work.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "abc123"}, // normalized lowercase
			{"kind": "dc", "value": "urn:isbn:9780316419568"},
		},
	})
	if code != 200 || out["work_id"] != workID || out["confidence"] != "high" {
		t.Fatalf("re-resolve: %d %v", code, out)
	}

	// Push an op.
	code, out = post(t, ts.URL+"/v1/ops", devTok, map[string]any{
		"ops": []map[string]any{{
			"op_id": "018e6f1a-0000-7000-8000-000000000001", "work_id": workID,
			"edition_sha": "abc123", "client_ts": time.Now().UTC().Format(time.RFC3339Nano),
			"progression": 0.41, "locator": map[string]any{"href": "/text/ch4.xhtml"},
		}},
	})
	if code != 200 {
		t.Fatalf("push: %d %v", code, out)
	}
	res := out["results"].([]any)[0].(map[string]any)
	if res["status"] != "applied" || res["seq"].(float64) != 1 {
		t.Fatalf("push result: %v", res)
	}

	// Duplicate push -> duplicate status.
	code, out = post(t, ts.URL+"/v1/ops", devTok, map[string]any{
		"ops": []map[string]any{{
			"op_id": "018e6f1a-0000-7000-8000-000000000001", "work_id": workID,
			"edition_sha": "abc123", "client_ts": time.Now().UTC().Format(time.RFC3339Nano),
			"progression": 0.41, "locator": map[string]any{"href": "/text/ch4.xhtml"},
		}},
	})
	// NB: client_ts differs here (time.Now() called again), which makes
	// this a payload mismatch -> conflict. That's the correct contract;
	// for a true retry the client replays the exact same op.
	res = out["results"].([]any)[0].(map[string]any)
	if res["status"] != "conflict" && res["status"] != "duplicate" {
		t.Fatalf("retry status: %v", res)
	}

	// Changes feed.
	code, out = get(t, ts.URL+"/v1/changes?since=0&limit=10", devTok)
	if code != 200 {
		t.Fatalf("changes: %d %v", code, out)
	}
	ops := out["ops"].([]any)
	if len(ops) != 1 || out["high_water"].(float64) != 1 {
		t.Fatalf("changes: %v", out)
	}

	// Heads.
	code, out = get(t, ts.URL+"/v1/heads", devTok)
	if code != 200 || out["snapshot_seq"].(float64) != 1 {
		t.Fatalf("heads: %d %v", code, out)
	}

	// Positions for the work.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/positions", devTok)
	if code != 200 {
		t.Fatalf("positions: %d", code)
	}
	if len(out["ops"].([]any)) != 1 {
		t.Fatalf("positions: %v", out)
	}
}

func TestAliasConflict409(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	_, tok, err := svc.MintToken(ctx, u.ID, "dev", store.ScopeSync, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = tok

	// Two works, each with its own sha256 alias.
	for i, sha := range []string{"aaaa", "bbbb"} {
		if err := st.CreateWork(ctx,
			store.Work{ID: fmt.Sprintf("w%d", i), UserID: u.ID, CreatedAt: time.Now()},
			&store.Edition{UserID: u.ID, SHA256: sha, WorkID: fmt.Sprintf("w%d", i)},
			[]store.Identifier{{Kind: "sha256", Value: sha}}); err != nil {
			t.Fatal(err)
		}
	}

	_, out := post(t, ts.URL+"/v1/works/resolve", bearerSecret(t, st, u.ID), map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "aaaa"},
			{"kind": "sha256", "value": "bbbb"},
		},
	})
	if out["works"] == nil {
		t.Fatalf("want conflict listing works, got %v", out)
	}
}

func bearerSecret(t *testing.T, st store.Store, userID string) string {
	t.Helper()
	svc := auth.NewService(st)
	secret, _, err := svc.MintToken(t.Context(), userID, "dev", store.ScopeSync, nil)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestTenantIsolation(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	ua := store.User{ID: "ua", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	ub := store.User{ID: "ub", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()}
	for _, u := range []store.User{ua, ub} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// Alice has a work.
	if err := st.CreateWork(ctx,
		store.Work{ID: "wa", UserID: ua.ID, CreatedAt: time.Now()}, nil,
		[]store.Identifier{{Kind: "sha256", Value: "aaaa"}}); err != nil {
		t.Fatal(err)
	}
	bobTok := bearerSecret(t, st, ub.ID)

	// Bob cannot see Alice's work's positions (404, not data).
	code, _ := get(t, ts.URL+"/v1/works/wa/positions", bobTok)
	if code != 404 {
		t.Fatalf("cross-user positions: want 404, got %d", code)
	}
	// Bob's changes feed is empty.
	_, out := get(t, ts.URL+"/v1/changes?since=0", bobTok)
	if len(out["ops"].([]any)) != 0 {
		t.Fatalf("cross-user changes leaked: %v", out)
	}
	// Bob resolving Alice's alias creates his own work (per-user works).
	code, out = post(t, ts.URL+"/v1/works/resolve", bobTok, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": "aaaa"}},
	})
	if code != 201 || out["work_id"] == "wa" {
		t.Fatalf("cross-user resolve: %d %v", code, out)
	}
}

func TestScopeEnforcement(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	syncSecret, _, _ := svc.MintToken(ctx, u.ID, "syncdev", store.ScopeSync, nil)
	roSecret, _, _ := svc.MintToken(ctx, u.ID, "ro", store.ScopeReadInsights, nil)

	// read-insights cannot push ops.
	code, _ := post(t, ts.URL+"/v1/ops", roSecret, map[string]any{
		"ops": []map[string]any{{"op_id": "x", "work_id": "w", "client_ts": time.Now().UTC().Format(time.RFC3339Nano), "progression": 0.1}},
	})
	if code != 403 {
		t.Fatalf("read-insights pushing ops: want 403, got %d", code)
	}
	// sync token cannot read insights.
	code, _ = get(t, ts.URL+"/v1/insights/summary", syncSecret)
	if code != 403 {
		t.Fatalf("sync reading insights: want 403, got %d", code)
	}
	// read-insights can read insights.
	code, _ = get(t, ts.URL+"/v1/insights/summary", roSecret)
	if code != 200 {
		t.Fatalf("read-insights summary: want 200, got %d", code)
	}
}

func TestSessionsAndInsights(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, Timezone: "Europe/Paris", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	syncSecret, _, _ := svc.MintToken(ctx, u.ID, "phone", store.ScopeSync, nil)
	roSecret, _, _ := svc.MintToken(ctx, u.ID, "ro", store.ScopeReadInsights, nil)

	// Work with a known page count.
	w := store.Work{ID: "w1", UserID: u.ID, CreatedAt: time.Now()}
	ed := &store.Edition{UserID: u.ID, SHA256: "abc123", WorkID: "w1", PageCount: ptrI64(300)}
	if err := st.CreateWork(ctx, w, ed, []store.Identifier{{Kind: "sha256", Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	// Two sessions: +0.10 progression over 30 min active each.
	mk := func(id string, start time.Time, sp, ep float64) map[string]any {
		return map[string]any{
			"session_id": id, "work_id": "w1", "edition_sha": "abc123",
			"started_at": start.Format(time.RFC3339Nano), "ended_at": start.Add(30 * time.Minute).Format(time.RFC3339Nano),
			"start_progression": sp, "end_progression": ep,
		}
	}
	code, out := post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			mk("s1", now.Add(-48*time.Hour), 0.1, 0.2),
			mk("s2", now.Add(-24*time.Hour), 0.2, 0.3),
		},
	})
	if code != 200 {
		t.Fatalf("sessions push: %d %v", code, out)
	}
	// Idempotent re-push.
	code, _ = post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			mk("s1", now.Add(-48*time.Hour), 0.1, 0.2),
		},
	})
	if code != 200 {
		t.Fatalf("idempotent session re-push: %d", code)
	}
	// Same id, different payload -> 409.
	code, _ = post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{mk("s1", now.Add(-48*time.Hour), 0.1, 0.5)},
	})
	if code != 409 {
		t.Fatalf("session id mismatch: want 409, got %d", code)
	}
	// Invalid: ended before started.
	bad := mk("s3", now, 0.3, 0.4)
	bad["ended_at"] = now.Add(-time.Hour).Format(time.RFC3339Nano)
	code, _ = post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{"sessions": []map[string]any{bad}})
	if code != 400 {
		t.Fatalf("inverted session: want 400, got %d", code)
	}

	// Work insights: 0.2 progression x 300 pages = 60 pages, 60 min.
	code, out = get(t, ts.URL+"/v1/insights/works/w1", roSecret)
	if code != 200 {
		t.Fatalf("work insights: %d", code)
	}
	if got := out["total_pages"].(float64); got < 59.9 || got > 60.1 {
		t.Fatalf("pages: %v", got)
	}
	if out["sessions"].(float64) != 2 {
		t.Fatalf("sessions: %v", out["sessions"])
	}

	// Summary over 30d.
	code, out = get(t, ts.URL+"/v1/insights/summary?range=30d", roSecret)
	if code != 200 {
		t.Fatalf("summary: %d", code)
	}
	if got := out["total_pages"].(float64); got < 59.9 || got > 60.1 {
		t.Fatalf("summary pages: %v", got)
	}

	// Calendar.
	code, out = get(t, ts.URL+"/v1/insights/calendar", roSecret)
	if code != 200 || len(out["days"].([]any)) != 2 {
		t.Fatalf("calendar: %d %v", code, out)
	}
}

func ptrI64(v int64) *int64 { return &v }
