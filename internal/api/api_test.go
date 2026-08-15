package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

	// A second device resolving the same file sends identifiers that
	// *all* hit the same work. This must reuse the work, not fall
	// through to creation and die on duplicate aliases.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "abc123"},
			{"kind": "partial-md5", "value": "ffff"},
			{"kind": "ta", "value": "a memory called empire|arkady martine"},
		},
		"title":  "A Memory Called Empire",
		"author": "Arkady Martine",
	})
	if code != 200 || out["work_id"] != workID || out["created"] != false || out["confidence"] != "high" {
		t.Fatalf("second-device resolve: %d %v", code, out)
	}
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "abc123"},
			{"kind": "partial-md5", "value": "ffff"},
			{"kind": "ta", "value": "a memory called empire|arkady martine"},
		},
	})
	if code != 200 || out["work_id"] != workID {
		t.Fatalf("second-device re-resolve: %d %v", code, out)
	}

	// The first device also registers the catalog's own id for the book.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "abc123"},
			{"kind": "source", "value": "komga:0K1QJDF1TW3Rit"},
		},
	})
	if code != 200 || out["work_id"] != workID {
		t.Fatalf("source registration: %d %v", code, out)
	}

	// A device that browses the same catalog but has not downloaded the
	// file has no hashes to offer — only the catalog id and the title.
	// The catalog id alone must identify the book with high confidence.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "source", "value": "komga:0K1QJDF1TW3Rit"},
			{"kind": "ta", "value": "a memory called empire|arkady martine"},
		},
		"title":  "A Memory Called Empire",
		"author": "Arkady Martine",
	})
	if code != 200 || out["work_id"] != workID || out["confidence"] != "high" {
		t.Fatalf("catalog-only resolve: %d %v", code, out)
	}

	// A fuzzy, unconfirmed hit is a guess: the stronger identifiers
	// sent along with it must not be registered, or the next resolve
	// would match on one of them and launder the guess into a fact.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "sha256", "value": "dddd"},
			{"kind": "ta", "value": "a memory called empire|arkady martine"},
		},
	})
	if code != 200 || out["work_id"] != workID || out["confidence"] != "low" {
		t.Fatalf("fuzzy resolve: %d %v", code, out)
	}
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": "dddd"}},
		"title":       "Some Other Edition",
	})
	if code != 201 || out["created"] != true {
		t.Fatalf("guessed sha must not have been registered: %d %v", code, out)
	}

	// The reader's word makes the same fuzzy match a real one, and the
	// stronger identifiers are registered on it.
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{
			{"kind": "source", "value": "komga:9XkPa22TR"},
			{"kind": "ta", "value": "a memory called empire|arkady martine"},
		},
		"confirmed": true,
	})
	if code != 200 || out["work_id"] != workID || out["confidence"] != "high" {
		t.Fatalf("confirmed resolve: %d %v", code, out)
	}
	code, out = post(t, ts.URL+"/v1/works/resolve", devTok, map[string]any{
		"identifiers": []map[string]string{{"kind": "source", "value": "komga:9XkPa22TR"}},
	})
	if code != 200 || out["work_id"] != workID {
		t.Fatalf("confirmed registration: %d %v", code, out)
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
	_, out = post(t, ts.URL+"/v1/ops", devTok, map[string]any{
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

	// All work insights in one request for client dashboards.
	code, out = get(t, ts.URL+"/v1/insights/works", roSecret)
	works, ok := out["works"].([]any)
	if code != 200 || !ok || len(works) != 1 {
		t.Fatalf("all work insights: %d %v", code, out)
	}
	work := works[0].(map[string]any)
	if work["work_id"] != "w1" || work["sessions"].(float64) != 2 || work["last_read_at"] == nil {
		t.Fatalf("work aggregate: %v", work)
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

func TestSessionRollupPreservesTotals(t *testing.T) {
	_, st := testServer(t)
	ctx := t.Context()
	u := store.User{
		ID: "rollup-user", Name: "rollup-user", Argon2Hash: "x",
		Timezone: "Europe/Paris", KosyncEnabled: true, KopluginEnabled: true,
		CreatedAt: time.Now(),
	}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{
		ID: "rollup-work", UserID: u.ID, Title: "Old book", CreatedAt: time.Now(),
	}
	ed := store.Edition{
		UserID: u.ID, SHA256: "rollup-edition", WorkID: w.ID, PageCount: ptrI64(462),
	}
	if err := st.CreateWork(ctx, w, &ed, nil); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-200 * 24 * time.Hour).Truncate(time.Second)
	ses := store.Session{
		SessionID: "rollup-session", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "reader",
		StartedAt: old, EndedAt: old.Add(time.Hour), StartProg: 0.1, EndProg: 0.2,
		Origin: store.OriginNative,
	}
	if err := st.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{St: st}
	srv.rollupSessionsOnce(ctx, 180*24*time.Hour)

	raw, err := st.CurrentSessionsForWork(ctx, u.ID, w.ID, 10)
	if err != nil || len(raw) != 0 {
		t.Fatalf("raw sessions after rollup: %d %v", len(raw), err)
	}
	rollups, err := st.RollupsForWork(ctx, u.ID, w.ID)
	if err != nil || len(rollups) != 1 {
		t.Fatalf("rollups: %+v %v", rollups, err)
	}
	got := rollups[0]
	if got.ActiveSeconds != 3600 || got.SessionCount != 1 ||
		got.Pages < 46.19 || got.Pages > 46.21 ||
		got.ProgDelta < 0.099 || got.ProgDelta > 0.101 {
		t.Fatalf("unexpected totals: %+v", got)
	}
}

// TestAdminScopeNotSelfMintable is a regression test for a privilege
// escalation: POST /v1/tokens accepted scope=admin from any user
// holding an ordinary login credential. Admin scope implies every
// other scope and unlocks the instance-wide admin pages, so it must
// only be mintable by an existing admin (or the admin CLI).
func TestAdminScopeNotSelfMintable(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(ctx, store.User{
		ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	login := func() string {
		code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
			"username": "alice", "password": "hunter2hunter",
		})
		if code != 200 {
			t.Fatalf("login: %d %v", code, out)
		}
		return out["auth_token"].(string)
	}

	code, out := post(t, ts.URL+"/v1/tokens", login(), map[string]string{
		"name": "escalate", "scope": "admin",
	})
	if code != http.StatusForbidden {
		t.Fatalf("self-minted admin token: want 403, got %d %v", code, out)
	}
	// Ordinary scopes still work.
	if code, out = post(t, ts.URL+"/v1/tokens", login(), map[string]string{
		"name": "Boox Palma", "scope": "sync",
	}); code != 201 {
		t.Fatalf("sync token: want 201, got %d %v", code, out)
	}

	// An existing admin may mint another admin token.
	if _, _, err := auth.NewService(st).MintToken(ctx, "u1", "cli", store.ScopeAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if code, out = post(t, ts.URL+"/v1/tokens", login(), map[string]string{
		"name": "second admin", "scope": "admin",
	}); code != 201 {
		t.Fatalf("admin minting admin: want 201, got %d %v", code, out)
	}
}

// TestRedactPath pins the fix for the koplugin capability secret
// leaking into the server log: LogServerErrors logged r.URL.Path
// verbatim, and the upload handler answers 500 on any store error.
func TestRedactPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{
			"/adapter/koplugin/s3cr3tcapability/api/plugin/upload",
			"/adapter/koplugin/[redacted]/api/plugin/upload",
		},
		{"/adapter/koplugin/s3cr3tcapability", "/adapter/koplugin/[redacted]"},
		{"/adapter/koplugin/", "/adapter/koplugin/[redacted]"},
		{"/v1/ops", "/v1/ops"},
		{"/adapter/kosync/syncs/progress", "/adapter/kosync/syncs/progress"},
		{"/ui/settings", "/ui/settings"},
	} {
		if got := RedactPath(tc.in); got != tc.want {
			t.Errorf("RedactPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestServerErrorLogRedactsCapability checks the redaction where it
// matters: through the logging middleware itself.
func TestServerErrorLogRedactsCapability(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := LogServerErrors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	req := httptest.NewRequest("POST",
		"/adapter/koplugin/s3cr3tcapability/api/plugin/upload", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := buf.String(); strings.Contains(got, "s3cr3tcapability") {
		t.Fatalf("capability secret leaked into the log: %s", got)
	} else if !strings.Contains(got, "[redacted]") {
		t.Fatalf("path not logged redacted: %s", got)
	}
}
