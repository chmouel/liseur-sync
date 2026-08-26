package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// progressionFixture is the smallest setup the progression tests need: a
// user, a sync token, and one resolved work to hang positions off.
type progressionFixture struct {
	url   string
	token string
	work  string
}

func newProgressionFixture(t *testing.T) progressionFixture {
	t.Helper()
	ts, st := testServer(t)

	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	u := store.User{ID: "u1", Name: "reader", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	scopes, err := store.NormalizeScopes([]store.Scope{store.ScopeSync})
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.NewService(st).MintToken(t.Context(), u.ID, "device", scopes, nil)
	if err != nil {
		t.Fatal(err)
	}

	code, out := post(t, ts.URL+"/v1/works/resolve", secret, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": "abc123"}},
		"title":       "A Book",
		"author":      "An Author",
	})
	if code != 201 {
		t.Fatalf("resolve: %d %v", code, out)
	}
	return progressionFixture{url: ts.URL, token: secret, work: out["work_id"].(string)}
}

// pushOp posts a single op whose progression is set exactly as the
// caller hands it (a nil value means the key is omitted, matching an
// absent field on the wire). It returns the HTTP status and the parsed
// body.
func (f progressionFixture) pushOp(t *testing.T, opID string, progression any) (int, map[string]any) {
	t.Helper()
	op := map[string]any{
		"op_id":     opID,
		"work_id":   f.work,
		"client_ts": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if progression != nil {
		op["progression"] = progression
	}
	return post(t, f.url+"/v1/ops", f.token, map[string]any{"ops": []map[string]any{op}})
}

func (f progressionFixture) pushSession(t *testing.T, id string, start, end any) (int, map[string]any) {
	t.Helper()
	now := time.Now().UTC()
	sess := map[string]any{
		"session_id": id,
		"work_id":    f.work,
		"started_at": now.Add(-time.Hour).Format(time.RFC3339Nano),
		"ended_at":   now.Format(time.RFC3339Nano),
	}
	if start != nil {
		sess["start_progression"] = start
	}
	if end != nil {
		sess["end_progression"] = end
	}
	return post(t, f.url+"/v1/sessions", f.token, map[string]any{"sessions": []map[string]any{sess}})
}

// TestOpsProgressionMustBePresentAndInRange is the server-side gate on
// the position-jumps bug: a null or absent progression once decoded to a
// silent 0 and recorded a reader at the start of a book. A real 0 is a
// legitimate position and must still be accepted.
func TestOpsProgressionMustBePresentAndInRange(t *testing.T) {
	f := newProgressionFixture(t)

	if code, out := f.pushOp(t, "op-null", nil); code != 400 ||
		out["error"] != "op op-null: progression required" {
		t.Fatalf("null progression: want 400 progression required, got %d %v", code, out)
	}
	// A literal JSON null is the exact shape the four bad production ops
	// carried; it must be refused just like an absent key.
	if code, out := post(t, f.url+"/v1/ops", f.token, map[string]any{
		"ops": []map[string]any{{
			"op_id": "op-explicit-null", "work_id": f.work,
			"client_ts":   time.Now().UTC().Format(time.RFC3339Nano),
			"progression": nil,
		}},
	}); code != 400 || out["error"] != "op op-explicit-null: progression required" {
		t.Fatalf("explicit null progression: want 400, got %d %v", code, out)
	}

	if code, out := f.pushOp(t, "op-zero", 0); code != 200 {
		t.Fatalf("zero progression must be accepted: %d %v", code, out)
	}
	if code, out := f.pushOp(t, "op-mid", 0.47); code != 200 {
		t.Fatalf("mid-range progression: %d %v", code, out)
	}
	if code, out := f.pushOp(t, "op-neg", -0.1); code != 400 ||
		out["error"] != "op op-neg: progression out of range [0,1]" {
		t.Fatalf("negative progression: want 400 out of range, got %d %v", code, out)
	}
	if code, out := f.pushOp(t, "op-high", 1.5); code != 400 ||
		out["error"] != "op op-high: progression out of range [0,1]" {
		t.Fatalf("over-range progression: want 400 out of range, got %d %v", code, out)
	}

	// A literal NaN token is not valid JSON, so Go's decoder rejects the
	// whole body before any per-op check runs; the !(>=0 && <=1) form is
	// defence for a NaN that arrives as a valid JSON number through some
	// other path, not something reachable from this endpoint.
}

// TestOpToJSONNeverEmitsNullProgression guards the constraint the fix
// must not break: opJSON stays a plain float64 on the response path, so
// /v1/heads, /v1/changes and /v1/works/{id}/positions keep emitting a
// number — never null, which every existing client would misread.
func TestOpToJSONNeverEmitsNullProgression(t *testing.T) {
	for _, prog := range []float64{0, 0.47, 1} {
		raw, err := json.Marshal(opToJSON(store.Op{
			OpID:        "op",
			WorkID:      "w",
			Progression: prog,
			ClientTS:    time.Now(),
			ReceivedAt:  time.Now(),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"progression":null`) {
			t.Fatalf("response emitted a null progression: %s", raw)
		}
		if !strings.Contains(string(raw), `"progression":`) {
			t.Fatalf("response dropped progression entirely: %s", raw)
		}
	}
}

// TestSessionProgressionMustBePresentAndInRange is the sessions.go twin
// of the ops gate: a session silently recorded as 0→0 corrupts the
// statistics it feeds, so a null or absent progression is refused while
// a real 0 is kept.
func TestSessionProgressionMustBePresentAndInRange(t *testing.T) {
	f := newProgressionFixture(t)

	if code, out := f.pushSession(t, "s-null-start", nil, 0.2); code != 400 ||
		out["error"] != "session s-null-start: progression required" {
		t.Fatalf("null start_progression: want 400, got %d %v", code, out)
	}
	if code, out := f.pushSession(t, "s-null-end", 0.1, nil); code != 400 ||
		out["error"] != "session s-null-end: progression required" {
		t.Fatalf("null end_progression: want 400, got %d %v", code, out)
	}
	if code, out := f.pushSession(t, "s-zero", 0, 0); code != 200 {
		t.Fatalf("zero progression must be accepted: %d %v", code, out)
	}
	if code, out := f.pushSession(t, "s-mid", 0.1, 0.47); code != 200 {
		t.Fatalf("mid-range progression: %d %v", code, out)
	}
	if code, out := f.pushSession(t, "s-neg", -0.1, 0.2); code != 400 ||
		out["error"] != "session s-neg: progression out of range [0,1]" {
		t.Fatalf("negative start_progression: want 400, got %d %v", code, out)
	}
	if code, out := f.pushSession(t, "s-high", 0.1, 1.5); code != 400 ||
		out["error"] != "session s-high: progression out of range [0,1]" {
		t.Fatalf("over-range end_progression: want 400, got %d %v", code, out)
	}
}
