package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// syncFixture: one user with one work and a sync token; a batch item
// factory for each of the two batch routes.
func syncFixture(t *testing.T) (url string, st store.Store, tok string) {
	t.Helper()
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	tok, _, _ = auth.NewService(st).MintToken(ctx, u.ID, "phone", store.ScopeSet{store.ScopeSync}, nil)
	w := store.Work{ID: "w1", UserID: u.ID, CreatedAt: time.Now()}
	if err := st.CreateWork(ctx, w, &store.Edition{UserID: u.ID, SHA256: "abc123", WorkID: "w1"},
		[]store.Identifier{{Kind: "sha256", Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}
	return ts.URL, st, tok
}

func anOp(id string, prog float64) map[string]any {
	return map[string]any{
		"op_id": id, "work_id": "w1", "client_ts": "2026-01-01T00:00:00Z", "progression": prog,
	}
}

func aSession(id string, endProg float64) map[string]any {
	return map[string]any{
		"session_id": id, "work_id": "w1",
		"started_at": "2026-01-01T00:00:00Z", "ended_at": "2026-01-01T01:00:00Z",
		"start_progression": 0.1, "end_progression": endProg, "idle_ms": 0,
	}
}

// TestPushOpsRefusalsNameTheItem: every item-level refusal on POST
// /v1/ops carries a code, the item's position and its id, and the whole
// batch is refused. A client can then set that item aside without
// parsing the message.
func TestPushOpsRefusalsNameTheItem(t *testing.T) {
	url, _, tok := syncFixture(t)
	cases := []struct {
		name string
		bad  map[string]any
		code string
		id   any
	}{
		{"no op_id", map[string]any{"work_id": "w1", "client_ts": "2026-01-01T00:00:00Z", "progression": 0.1}, "missing_field", nil},
		{"long op_id", anOp(strings.Repeat("x", 65), 0.1), "missing_field", nil},
		{"no progression", map[string]any{"op_id": "p", "work_id": "w1", "client_ts": "2026-01-01T00:00:00Z"}, "missing_field", "p"},
		{"progression 1.5", anOp("q", 1.5), "progression_out_of_range", "q"},
		{"bad ts", func() map[string]any { o := anOp("r", 0.1); o["client_ts"] = "yesterday"; return o }(), "bad_time", "r"},
		{"future ts", func() map[string]any {
			o := anOp("s", 0.1)
			o["client_ts"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
			return o
		}(), "time_in_future", "s"},
		{"locator too large", func() map[string]any {
			o := anOp("u", 0.1)
			o["locator"] = map[string]any{"href": strings.Repeat("a", 17*1024)}
			return o
		}(), "locator_too_large", "u"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := post(t, url+"/v1/ops", tok, map[string]any{
				"ops": []map[string]any{anOp("fine", 0.2), c.bad},
			})
			if code != http.StatusBadRequest || out["code"] != c.code || out["item_index"] != 1.0 {
				t.Fatalf("%s: %d %v", c.name, code, out)
			}
			if got := out["op_id"]; got != c.id {
				t.Fatalf("%s: op_id %v, want %v", c.name, got, c.id)
			}
			if c.code == "locator_too_large" && out["limit"] != float64(16*1024) {
				t.Fatalf("locator limit not named: %v", out)
			}
		})
	}
	// Nothing rode along.
	if _, out := get(t, url+"/v1/changes?since=0", tok); len(out["ops"].([]any)) != 0 {
		t.Fatalf("a refused batch stored something: %v", out)
	}

	// Batch-level refusals name the limit, not an item.
	var many []map[string]any
	for i := 0; i < 501; i++ {
		many = append(many, anOp(fmt.Sprintf("m-%d", i), 0.1))
	}
	code, out := post(t, url+"/v1/ops", tok, map[string]any{"ops": many})
	if code != http.StatusBadRequest || out["code"] != "batch_too_large" || out["limit"] != 500.0 || out["item_index"] != nil {
		t.Fatalf("batch too large: %d %v", code, out)
	}

	// A body past the byte bound is 413, as on the annotations route.
	huge := anOp("h", 0.1)
	huge["locator"] = map[string]any{"pad": strings.Repeat("a", 2<<20)}
	code, out = post(t, url+"/v1/ops", tok, map[string]any{"ops": []map[string]any{huge}})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d %v", code, out)
	}
}

// TestPushOpsConflictLeavesTheRestApplied: a per-item conflict is a
// result, not a refusal — the other items in the batch are applied and
// a byte-identical replay is a duplicate.
func TestPushOpsConflictLeavesTheRestApplied(t *testing.T) {
	url, _, tok := syncFixture(t)
	code, out := post(t, url+"/v1/ops", tok, map[string]any{"ops": []map[string]any{anOp("a", 0.1)}})
	if code != http.StatusOK {
		t.Fatalf("first push: %d %v", code, out)
	}
	code, out = post(t, url+"/v1/ops", tok, map[string]any{
		"ops": []map[string]any{anOp("a", 0.9), anOp("b", 0.2), anOp("a", 0.1)},
	})
	if code != http.StatusOK {
		t.Fatalf("mixed push: %d %v", code, out)
	}
	res := out["results"].([]any)
	want := []string{"conflict", "applied", "duplicate"}
	for i, w := range want {
		if got := res[i].(map[string]any)["status"]; got != w {
			t.Fatalf("result %d: want %s, got %v (%v)", i, w, got, res)
		}
	}
	if seq := res[2].(map[string]any)["seq"]; seq != 1.0 {
		t.Fatalf("duplicate must return the original seq: %v", res[2])
	}
	_, page := get(t, url+"/v1/changes?since=0", tok)
	if n := len(page["ops"].([]any)); n != 2 || page["high_water"] != 2.0 {
		t.Fatalf("want ops a,b stored: %v", page)
	}
}

// TestPushSessionsConflictNamesTheItem: a session_id replayed with a
// different payload is 409 with the item named, including by position
// when the same id appears twice in one batch; and the batch is atomic.
func TestPushSessionsConflictNamesTheItem(t *testing.T) {
	url, _, tok := syncFixture(t)
	if code, out := post(t, url+"/v1/sessions", tok, map[string]any{
		"sessions": []map[string]any{aSession("s1", 0.2)},
	}); code != http.StatusOK {
		t.Fatalf("first push: %d %v", code, out)
	}
	code, out := post(t, url+"/v1/sessions", tok, map[string]any{
		"sessions": []map[string]any{aSession("s2", 0.3), aSession("s1", 0.2), aSession("s1", 0.5)},
	})
	if code != http.StatusConflict || out["code"] != "id_reused" || out["session_id"] != "s1" || out["item_index"] != 2.0 {
		t.Fatalf("conflict: %d %v", code, out)
	}
	// s2 did not ride along: pushing it alone is a fresh accept.
	code, out = post(t, url+"/v1/sessions", tok, map[string]any{
		"sessions": []map[string]any{aSession("s2", 0.3)},
	})
	if code != http.StatusOK || out["accepted"] != 1.0 {
		t.Fatalf("s2 after the refused batch: %d %v", code, out)
	}
	// Too many, and too big, as for ops.
	var many []map[string]any
	for i := 0; i < 1001; i++ {
		many = append(many, aSession(fmt.Sprintf("m-%d", i), 0.2))
	}
	if code, out := post(t, url+"/v1/sessions", tok, map[string]any{"sessions": many}); code != http.StatusBadRequest ||
		out["code"] != "batch_too_large" || out["limit"] != 1000.0 {
		t.Fatalf("batch too large: %d %v", code, out)
	}
	bad := aSession("i", 0.2)
	bad["idle_ms"] = 2 * 3600 * 1000
	if code, out := post(t, url+"/v1/sessions", tok, map[string]any{"sessions": []map[string]any{bad}}); code != http.StatusBadRequest ||
		out["code"] != "idle_out_of_range" || out["session_id"] != "i" || out["item_index"] != 0.0 {
		t.Fatalf("idle: %d %v", code, out)
	}
}

// TestUnknownWorkIsDecidedInTheAppend: the work check happens inside the
// append transaction, so a work deleted after the handler would have
// checked it still yields the recoverable 400, never a 500. Exercised
// directly: the work is gone before the push.
func TestUnknownWorkIsDecidedInTheAppend(t *testing.T) {
	url, st, tok := syncFixture(t)
	if err := st.DeleteWork(t.Context(), "u1", "w1"); err != nil {
		t.Fatal(err)
	}
	code, out := post(t, url+"/v1/ops", tok, map[string]any{"ops": []map[string]any{anOp("a", 0.1)}})
	if code != http.StatusBadRequest || out["code"] != "unknown_work" || out["work_id"] != "w1" || out["op_id"] != "a" {
		t.Fatalf("op for a deleted work: %d %v", code, out)
	}
	code, out = post(t, url+"/v1/sessions", tok, map[string]any{"sessions": []map[string]any{aSession("s", 0.2)}})
	if code != http.StatusBadRequest || out["code"] != "unknown_work" || out["work_id"] != "w1" || out["session_id"] != "s" {
		t.Fatalf("session for a deleted work: %d %v", code, out)
	}
}

// TestChangesCursorAndResync: a malformed or negative cursor is 400,
// not a silent full replay; a cursor below the compaction horizon is
// 410 pointing at /v1/heads, whose snapshot_seq resumes the feed.
func TestChangesCursorAndResync(t *testing.T) {
	url, st, tok := syncFixture(t)
	for _, since := range []string{"abc", "-1", "1.5"} {
		if code, out := get(t, url+"/v1/changes?since="+since, tok); code != http.StatusBadRequest {
			t.Fatalf("since=%s: %d %v", since, code, out)
		}
	}
	var ops []map[string]any
	for i := 0; i < 5; i++ {
		ops = append(ops, anOp(fmt.Sprintf("o-%d", i), float64(i)/10))
	}
	if code, out := post(t, url+"/v1/ops", tok, map[string]any{"ops": ops}); code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}
	// Everything is "old" against a cutoff in the future: only the head
	// survives and the horizon lands on seq 4.
	if _, err := st.Compact(t.Context(), "u1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, out := get(t, url+"/v1/changes?since=0", tok)
	if code != http.StatusGone || out["error"] != "resync_required" || out["heads_endpoint"] != "/v1/heads" {
		t.Fatalf("below the horizon: %d %v", code, out)
	}
	code, heads := get(t, url+"/v1/heads", tok)
	if code != http.StatusOK || len(heads["ops"].([]any)) != 1 || heads["snapshot_seq"] != 5.0 {
		t.Fatalf("heads: %d %v", code, heads)
	}
	code, out = get(t, url+"/v1/changes?since=5", tok)
	if code != http.StatusOK || len(out["ops"].([]any)) != 0 || out["high_water"] != 5.0 {
		t.Fatalf("resume from snapshot_seq: %d %v", code, out)
	}
	code, out = get(t, url+"/v1/changes?since=4", tok)
	if code != http.StatusOK || len(out["ops"].([]any)) != 1 {
		t.Fatalf("at the horizon: %d %v", code, out)
	}
}

// TestPositionsLimitClamps: a limit past the maximum is clamped to it,
// not reset to the default.
func TestPositionsLimitClamps(t *testing.T) {
	url, _, tok := syncFixture(t)
	var ops []map[string]any
	for i := 0; i < 60; i++ {
		ops = append(ops, anOp(fmt.Sprintf("o-%d", i), 0.5))
	}
	if code, out := post(t, url+"/v1/ops", tok, map[string]any{"ops": ops}); code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}
	for _, c := range []struct {
		q    string
		want int
	}{{"", 50}, {"?limit=0", 50}, {"?limit=10", 10}, {"?limit=201", 60}, {"?limit=200", 60}} {
		code, out := get(t, url+"/v1/works/w1/positions"+c.q, tok)
		if code != http.StatusOK || len(out["ops"].([]any)) != c.want {
			t.Fatalf("positions%s: %d, %d ops (want %d)", c.q, code, len(out["ops"].([]any)), c.want)
		}
	}
}
