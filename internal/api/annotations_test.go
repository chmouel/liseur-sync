package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// annotationTestUser creates a user, mints a sync-scoped device token
// and resolves one work, returning the token and the work id.
func annotationTestUser(t *testing.T, ts string, st store.Store, id, name string) (string, string) {
	t.Helper()
	ctx := t.Context()
	if err := st.CreateUser(ctx, store.User{ID: id, Name: name, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	secret, _, err := svc.MintToken(ctx, id, name+"-device", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, out := post(t, ts+"/v1/works/resolve", secret, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": "sha-" + id}},
		"title":       "Book of " + name,
	})
	if code != 201 {
		t.Fatalf("resolve for %s: %d %v", name, code, out)
	}
	return secret, out["work_id"].(string)
}

func del(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	return requestJSON(t, http.MethodDelete, url, token, nil)
}

func annItem(id, workID string, baseRev int64) map[string]any {
	return map[string]any{
		"id":        id,
		"base_rev":  baseRev,
		"work_id":   workID,
		"kind":      "highlight",
		"locator":   map[string]any{"href": "ch1.xhtml", "text": map[string]any{"highlight": "sea"}},
		"excerpt":   "the sea was calm",
		"color":     "yellow",
		"client_ts": "2025-06-01T10:00:00Z",
	}
}

func pushAnns(t *testing.T, ts, tok string, items ...map[string]any) (int, map[string]any) {
	t.Helper()
	return post(t, ts+"/v1/annotations", tok, map[string]any{"annotations": items})
}

func firstResult(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	results, ok := out["results"].([]any)
	if !ok || len(results) == 0 {
		t.Fatalf("no results in %v", out)
	}
	return results[0].(map[string]any)
}

// TestAnnotationFullFlow walks the whole ADR-0028 dance: create, pull,
// idempotent retry, edit with the right rev, stale rev 409 with the
// server copy, delete, tombstone in the feed.
func TestAnnotationFullFlow(t *testing.T) {
	ts, st := testServer(t)
	tok, workID := annotationTestUser(t, ts.URL, st, "u1", "alice")

	// Create.
	code, out := pushAnns(t, ts.URL, tok, annItem("a1", workID, 0))
	if code != 200 {
		t.Fatalf("push: %d %v", code, out)
	}
	res := firstResult(t, out)
	if res["status"] != "applied" || res["rev"].(float64) != 1 {
		t.Fatalf("create result: %v", res)
	}
	seq1 := res["seq"].(float64)

	// Byte-identical retry acknowledges without a new row.
	code, out = pushAnns(t, ts.URL, tok, annItem("a1", workID, 0))
	res = firstResult(t, out)
	if code != 200 || res["status"] != "duplicate" || res["rev"].(float64) != 1 || res["seq"].(float64) != seq1 {
		t.Fatalf("retry: %d %v", code, res)
	}

	// Pull since 0.
	code, out = get(t, ts.URL+"/v1/annotations/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("changes: %d %v", code, out)
	}
	anns := out["annotations"].([]any)
	if len(anns) != 1 {
		t.Fatalf("changes count: %v", out)
	}
	a := anns[0].(map[string]any)
	if a["id"] != "a1" || a["kind"] != "highlight" || a["excerpt"] != "the sea was calm" || a["deleted"] == true {
		t.Fatalf("changes payload: %v", a)
	}
	if a["device_id"] == "" || a["device_id"] == nil {
		t.Fatalf("annotation lost its device stamp: %v", a)
	}
	if out["high_water"].(float64) != seq1 || out["has_more"] != false {
		t.Fatalf("page contract: %v", out)
	}

	// Edit with the current rev.
	edit := annItem("a1", workID, 1)
	edit["body"] = "lovely image"
	code, out = pushAnns(t, ts.URL, tok, edit)
	res = firstResult(t, out)
	if code != 200 || res["status"] != "applied" || res["rev"].(float64) != 2 {
		t.Fatalf("edit: %d %v", code, res)
	}

	// A second device editing from the stale rev conflicts and gets
	// the server's copy back.
	stale := annItem("a1", workID, 1)
	stale["body"] = "competing edit"
	code, out = pushAnns(t, ts.URL, tok, stale)
	res = firstResult(t, out)
	if code != 200 || res["status"] != "conflict" {
		t.Fatalf("stale edit: %d %v", code, res)
	}
	server := res["server"].(map[string]any)
	if server["rev"].(float64) != 2 || server["body"] != "lovely image" {
		t.Fatalf("server copy: %v", server)
	}

	// The live set for the work.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/annotations", tok)
	if code != 200 || len(out["annotations"].([]any)) != 1 {
		t.Fatalf("work annotations: %d %v", code, out)
	}

	// Delete needs the current rev.
	code, out = del(t, ts.URL+"/v1/annotations/a1?rev=1", tok)
	if code != 409 {
		t.Fatalf("stale delete: %d %v", code, out)
	}
	if out["server"].(map[string]any)["rev"].(float64) != 2 {
		t.Fatalf("stale delete server copy: %v", out)
	}
	code, out = del(t, ts.URL+"/v1/annotations/a1?rev=2", tok)
	if code != 200 || out["status"] != "applied" {
		t.Fatalf("delete: %d %v", code, out)
	}
	// Deleting again is already accepted.
	code, out = del(t, ts.URL+"/v1/annotations/a1?rev=3", tok)
	if code != 200 || out["status"] != "duplicate" {
		t.Fatalf("re-delete: %d %v", code, out)
	}
	// Unknown id is a 404; missing rev a 400.
	if code, _ = del(t, ts.URL+"/v1/annotations/nope?rev=1", tok); code != 404 {
		t.Fatalf("unknown delete: %d", code)
	}
	if code, _ = del(t, ts.URL+"/v1/annotations/a1", tok); code != 400 {
		t.Fatalf("revless delete: %d", code)
	}

	// The feed now carries a tombstone: identity and when, nothing else.
	code, out = get(t, ts.URL+"/v1/annotations/changes?since="+"0", tok)
	if code != 200 {
		t.Fatalf("changes after delete: %d %v", code, out)
	}
	anns = out["annotations"].([]any)
	tomb := anns[len(anns)-1].(map[string]any)
	if tomb["id"] != "a1" || tomb["deleted"] != true {
		t.Fatalf("tombstone: %v", tomb)
	}
	for _, leak := range []string{"body", "excerpt", "locator", "color", "kind", "client_ts", "device_id", "work_id", "edition_sha"} {
		if v, ok := tomb[leak]; ok && v != "" {
			t.Fatalf("tombstone leaked %s: %v", leak, tomb)
		}
	}

	// The live set no longer lists it.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/annotations", tok)
	if code != 200 || len(out["annotations"].([]any)) != 0 {
		t.Fatalf("work annotations after delete: %d %v", code, out)
	}

	// The op-log feed never mentions annotations (regression: the two
	// feeds are separate counters, separate tables, separate routes).
	code, out = get(t, ts.URL+"/v1/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("op changes: %d %v", code, out)
	}
	if _, leaked := out["annotations"]; leaked {
		t.Fatalf("/v1/changes grew an annotations key: %v", out)
	}
	if out["high_water"].(float64) != 0 {
		t.Fatalf("annotation writes moved the op high-water: %v", out)
	}
}

// TestAnnotationBounds exercises the refusals. Shape errors are
// per-item results — one bad item fails alone, exactly like a bad
// reference — while only a request nothing in it can excuse (malformed
// JSON, an empty or oversized batch, an oversized body) is a 4xx.
// Nothing here may ever be a 5xx.
func TestAnnotationBounds(t *testing.T) {
	ts, st := testServer(t)
	tok, workID := annotationTestUser(t, ts.URL, st, "u1", "alice")

	refuse := func(name string, mutate func(map[string]any)) {
		t.Helper()
		item := annItem("b-"+name, workID, 0)
		mutate(item)
		// The bad item travels with a healthy neighbor to prove it
		// fails alone.
		code, out := pushAnns(t, ts.URL, tok, item, annItem("ok-"+name, workID, 0))
		if code != 200 {
			t.Fatalf("%s: want 200 with per-item results, got %d %v", name, code, out)
		}
		results := out["results"].([]any)
		bad := results[0].(map[string]any)
		if bad["status"] != "invalid" || bad["reason"] == nil || bad["reason"] == "" {
			t.Fatalf("%s: want invalid with a reason, got %v", name, bad)
		}
		if good := results[1].(map[string]any); good["status"] != "applied" {
			t.Fatalf("%s: the bad item did not fail alone: %v", name, good)
		}
	}

	refuse("no-id", func(m map[string]any) { m["id"] = "" })
	refuse("long-id", func(m map[string]any) { m["id"] = strings.Repeat("x", 65) })
	refuse("bad-kind", func(m map[string]any) { m["kind"] = "scribble" })
	refuse("no-work", func(m map[string]any) { delete(m, "work_id") })
	refuse("negative-base-rev", func(m map[string]any) { m["base_rev"] = -1 })
	refuse("highlight-without-locator", func(m map[string]any) { delete(m, "locator") })
	refuse("highlight-with-null-locator", func(m map[string]any) { m["locator"] = nil })
	refuse("long-work-id", func(m map[string]any) { m["work_id"] = strings.Repeat("w", 129) })
	refuse("long-edition-sha", func(m map[string]any) { m["edition_sha"] = strings.Repeat("e", 129) })
	refuse("bookmark-with-body", func(m map[string]any) {
		m["kind"] = "bookmark"
		m["body"] = "not allowed"
		delete(m, "color")
	})
	refuse("note-with-locator", func(m map[string]any) {
		m["kind"] = "note"
		m["body"] = "a thought"
		delete(m, "color")
	})
	refuse("note-without-body", func(m map[string]any) {
		m["kind"] = "note"
		delete(m, "locator")
		delete(m, "color")
	})
	refuse("bad-color", func(m map[string]any) { m["color"] = "#ff0000" })
	refuse("color-on-bookmark", func(m map[string]any) {
		m["kind"] = "bookmark"
		delete(m, "body")
	})
	refuse("progression-out-of-range", func(m map[string]any) { m["progression"] = 1.5 })
	refuse("oversize-excerpt", func(m map[string]any) { m["excerpt"] = strings.Repeat("x", 1<<10+1) })
	refuse("oversize-body", func(m map[string]any) { m["body"] = strings.Repeat("x", 16<<10+1) })
	refuse("bad-ts", func(m map[string]any) { m["client_ts"] = "yesterday" })
	refuse("long-ts", func(m map[string]any) {
		m["client_ts"] = "2024-01-02T03:04:05." + strings.Repeat("9", 60) + "Z"
	})
	refuse("wrong-type", func(m map[string]any) { m["base_rev"] = "not-a-number" })
	refuse("overflow-rev", func(m map[string]any) { m["base_rev"] = json.Number("9223372036854775808") })
	refuse("future-ts", func(m map[string]any) {
		m["client_ts"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	})

	// A JSON null locator is an absent one, so a note may carry it.
	nullNote := annItem("null-note", workID, 0)
	nullNote["kind"], nullNote["body"], nullNote["locator"] = "note", "a thought", nil
	delete(nullNote, "color")
	if code, out := pushAnns(t, ts.URL, tok, nullNote); code != 200 || firstResult(t, out)["status"] != "applied" {
		t.Fatalf("note with null locator: %d %v", code, out)
	}

	// Empty and oversized batches.
	if code, _ := post(t, ts.URL+"/v1/annotations", tok, map[string]any{"annotations": []any{}}); code != 400 {
		t.Fatalf("empty batch: %d", code)
	}
	big := make([]map[string]any, 101)
	for i := range big {
		big[i] = annItem("big", workID, 0)
	}
	if code, _ := pushAnns(t, ts.URL, tok, big...); code != 400 {
		t.Fatalf("oversized batch: %d", code)
	}

	// A body no legal batch could ever need is a 413, named as such —
	// never a misleading "invalid JSON body". The cap accounts for
	// worst-case JSON escaping, so it takes well over the decoded
	// field limits to reach it.
	{
		raw := `{"annotations":[{"id":"huge","body":"` + strings.Repeat("A", 16<<20) + `"}]}`
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/annotations", strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge || body["error"] != "request body too large" {
			t.Fatalf("oversized request: %d %v", resp.StatusCode, body)
		}
	}

	// An unknown work is a per-item semantic outcome, not a batch 400:
	// the shape is fine, the reference is not.
	code, out := pushAnns(t, ts.URL, tok, annItem("orphan", "w-not-there", 0))
	res := firstResult(t, out)
	if code != 200 || res["status"] != "invalid" || res["reason"] == "" {
		t.Fatalf("unknown work: %d %v", code, res)
	}

	// One bad reference fails alone: its neighbor still lands.
	code, out = pushAnns(t, ts.URL, tok,
		annItem("keeper", workID, 0), annItem("orphan2", "w-not-there", 0))
	if code != 200 {
		t.Fatalf("mixed batch: %d %v", code, out)
	}
	results := out["results"].([]any)
	if results[0].(map[string]any)["status"] != "applied" ||
		results[1].(map[string]any)["status"] != "invalid" {
		t.Fatalf("mixed batch results: %v", results)
	}
}

// TestAnnotationIsPrivateToItsAuthor is the tenant-isolation check:
// on every one of the four routes, another user's records are
// invisible and untouchable.
func TestAnnotationIsPrivateToItsAuthor(t *testing.T) {
	ts, st := testServer(t)
	aliceTok, aliceWork := annotationTestUser(t, ts.URL, st, "u1", "alice")
	bobTok, _ := annotationTestUser(t, ts.URL, st, "u2", "bob")

	code, out := pushAnns(t, ts.URL, aliceTok, annItem("a1", aliceWork, 0))
	if code != 200 || firstResult(t, out)["status"] != "applied" {
		t.Fatalf("alice push: %d %v", code, out)
	}

	// Bob's feed is empty and his high-water untouched.
	code, out = get(t, ts.URL+"/v1/annotations/changes?since=0", bobTok)
	if code != 200 || len(out["annotations"].([]any)) != 0 || out["high_water"].(float64) != 0 {
		t.Fatalf("bob's feed sees alice: %d %v", code, out)
	}

	// Alice's work does not exist for bob, so neither do its notes.
	if code, _ = get(t, ts.URL+"/v1/works/"+aliceWork+"/annotations", bobTok); code != 404 {
		t.Fatalf("bob read alice's work annotations: %d", code)
	}

	// Bob cannot delete alice's annotation — for him it does not exist.
	if code, _ = del(t, ts.URL+"/v1/annotations/a1?rev=1", bobTok); code != 404 {
		t.Fatalf("bob deleted alice's annotation: %d", code)
	}

	// Bob cannot attach an annotation to alice's work.
	code, out = pushAnns(t, ts.URL, bobTok, annItem("b1", aliceWork, 0))
	res := firstResult(t, out)
	if code != 200 || res["status"] != "invalid" {
		t.Fatalf("bob wrote into alice's work: %d %v", code, res)
	}

	// And bob pushing alice's annotation id creates his own record,
	// never a CAS race against hers.
	code, out = get(t, ts.URL+"/v1/annotations/changes?since=0", aliceTok)
	if code != 200 || len(out["annotations"].([]any)) != 1 {
		t.Fatalf("alice's record disturbed: %d %v", code, out)
	}
}
