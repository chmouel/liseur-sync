package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// trKeyHex is a valid 64-hex push key: the shape every client of the
// cache derives its keys with.
func trKeyHex(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// pushTr pushes one batch of translations, failing the test on anything
// but a 200.
func pushTr(t *testing.T, ts, tok, workID string, items ...map[string]any) (int, map[string]any) {
	t.Helper()
	return post(t, ts+"/v1/works/"+workID+"/translations", tok,
		map[string]any{"translations": items})
}

// TestWorkTranslationRoutes walks the ADR-0052 routes: push, idempotent
// push, delta pull with a cursor, and the refusals.
func TestWorkTranslationRoutes(t *testing.T) {
	ts, st := testServer(t)
	tok, workID := annotationTestUser(t, ts.URL, st, "u1", "alice")

	// Auth and scope.
	code, out := post(t, ts.URL+"/v1/works/"+workID+"/translations", "",
		map[string]any{"translations": []map[string]any{}})
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated push: %d %v", code, out)
	}
	other, _ := annotationTestUser(t, ts.URL, st, "u2", "bob")
	svc := auth.NewService(st)
	// A read-only token proves the routes are sync-scoped: minted
	// directly because the login route only mints the scopes a reader
	// signs in with.
	roTok, _, err := svc.MintToken(t.Context(), "u2", "bob-reader",
		store.ScopeSet{store.ScopeReadInsights}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, out = post(t, ts.URL+"/v1/works/"+workID+"/translations", roTok,
		map[string]any{"translations": []map[string]any{
			{"key": trKeyHex("k1"), "translation": "x"},
		}})
	if code != http.StatusForbidden {
		t.Fatalf("read-insights token pushed: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations", roTok)
	if code != http.StatusForbidden {
		t.Fatalf("read-insights token pulled: %d %v", code, out)
	}

	// Push.
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("k1"), "translation": "la mer était calme"},
		map[string]any{"key": trKeyHex("k2"), "translation": "le ciel était bas"},
	)
	if code != http.StatusOK || out["status"] != "applied" {
		t.Fatalf("push: %d %v", code, out)
	}

	// Validation: a key that is not 64 hex characters names the item.
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": "not-hex", "translation": "x"})
	if code != http.StatusBadRequest || out["code"] != "bad_key" {
		t.Fatalf("bad key: %d %v", code, out)
	}
	if out["item_index"].(float64) != 0 || out["key"] != "not-hex" {
		t.Fatalf("bad key refusal does not name the item: %v", out)
	}
	// A missing field, and a value over the per-value bound.
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"translation": "x"})
	if code != http.StatusBadRequest || out["code"] != "missing_field" {
		t.Fatalf("missing key: %d %v", code, out)
	}
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("k3"), "translation": ""})
	if code != http.StatusBadRequest || out["code"] != "missing_field" {
		t.Fatalf("missing translation: %d %v", code, out)
	}
	huge := strings.Repeat("é", 5000) // 10,000 bytes over UTF-8, over the 8 KiB default
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("k3"), "translation": huge})
	if code != http.StatusBadRequest || out["code"] != "too_large" {
		t.Fatalf("too large: %d %v", code, out)
	}
	// A NUL in the value: PostgreSQL refuses it outright and SQLite
	// cannot measure it for the budget, so the edge refuses it for both.
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("k3"), "translation": "a\u0000b"})
	if code != http.StatusBadRequest || out["code"] != "bad_value" {
		t.Fatalf("nul value: %d %v", code, out)
	}
	// An empty or oversized batch.
	code, out = pushTr(t, ts.URL, tok, workID)
	if code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d %v", code, out)
	}
	var bigBatch []map[string]any
	for i := 0; i < 501; i++ {
		bigBatch = append(bigBatch, map[string]any{
			"key": trKeyHex("batch"), "translation": "x",
		})
	}
	code, out = pushTr(t, ts.URL, tok, workID, bigBatch...)
	if code != http.StatusBadRequest || out["code"] != "batch_too_large" {
		t.Fatalf("batch too large: %d %v", code, out)
	}

	// A work the user does not have is the ops-shaped unknown_work.
	code, out = pushTr(t, ts.URL, tok, "nope",
		map[string]any{"key": trKeyHex("k9"), "translation": "x"})
	if code != http.StatusBadRequest || out["code"] != "unknown_work" || out["work_id"] != "nope" {
		t.Fatalf("unknown work: %d %v", code, out)
	}
	// A work the user does not have is a 404 with the ops-shaped JSON
	// body; the client tells an old server (404, no body) from a dropped
	// work by that body, so a bare 404 here is a regression.
	// The garbage cursor must not outrank the missing work: the store
	// checks the work first, so this stays a 404 rather than a 400.
	code, out = get(t, ts.URL+"/v1/works/nope/translations?after=garbage", tok)
	if code != http.StatusNotFound || out["error"] != "work not found" {
		t.Fatalf("pull of unknown work: %d %v", code, out)
	}

	// Pull: the whole work first.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations", tok)
	if code != http.StatusOK {
		t.Fatalf("pull: %d %v", code, out)
	}
	rows := out["translations"].([]any)
	if len(rows) != 2 {
		t.Fatalf("pull count: %v", out)
	}
	// Rows pushed in one batch share a timestamp, so the order between
	// them is the keys' — assert by key, not by position.
	byKey := map[string]map[string]any{}
	for _, row := range rows {
		r := row.(map[string]any)
		byKey[r["key"].(string)] = r
	}
	first := byKey[trKeyHex("k1")]
	if first == nil || first["translation"] != "la mer était calme" {
		t.Fatalf("pull payload: %v", first)
	}
	if first["device_id"] == "" || first["updated_at"] == "" {
		t.Fatalf("pull lost the server stamps: %v", first)
	}
	if out["has_more"] != false {
		t.Fatalf("page contract: %v", out)
	}
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("pull named no cursor: %v", out)
	}

	// A cursor that does not parse is refused.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations?after=garbage", tok)
	if code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d %v", code, out)
	}

	// The delta after the cursor is empty until something changes.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations?after="+cursorEscape(t, cursor), tok)
	if code != http.StatusOK {
		t.Fatalf("delta pull: %d %v", code, out)
	}
	if rows := out["translations"].([]any); len(rows) != 0 {
		t.Fatalf("delta not empty: %v", out)
	}

	// An edit moves its row past the cursor.
	code, out = pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("k2"), "translation": "le ciel était haut"})
	if code != http.StatusOK {
		t.Fatalf("edit push: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations?after="+cursorEscape(t, cursor), tok)
	if code != http.StatusOK {
		t.Fatalf("delta pull after edit: %d %v", code, out)
	}
	rows = out["translations"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["key"] != trKeyHex("k2") ||
		rows[0].(map[string]any)["translation"] != "le ciel était haut" {
		t.Fatalf("delta after edit: %v", out)
	}

	// Paging: a limit under the row count answers has_more and a cursor.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations?limit=1", tok)
	if code != http.StatusOK || out["has_more"] != true {
		t.Fatalf("paged pull: %d %v", code, out)
	}
	next, _ := out["next_cursor"].(string)
	if next == "" {
		t.Fatalf("paged pull named no cursor: %v", out)
	}

	// Another account sees nothing of this one's cache, even under a
	// work id of its own that happens to match.
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations", other)
	if code != http.StatusNotFound {
		t.Fatalf("another account's pull of a foreign work id: %d %v", code, out)
	}
}

// cursorEscape escapes a cursor for a query string. The cursor is a
// timestamp and a hex key joined by a space, so only the space needs it.
func cursorEscape(t *testing.T, cursor string) string {
	t.Helper()
	return strings.ReplaceAll(cursor, " ", "%20")
}

// TestWorkTranslationClearRoutes walks the ADR-0053 clears: one work,
// then the whole account, both idempotent and sync-scoped, and neither
// reaching another account.
func TestWorkTranslationClearRoutes(t *testing.T) {
	ts, st := testServer(t)
	tok, workID := annotationTestUser(t, ts.URL, st, "u1", "alice")
	other, otherWork := annotationTestUser(t, ts.URL, st, "u2", "bob")

	if code, out := del(t, ts.URL+"/v1/works/"+workID+"/translations", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated clear: %d %v", code, out)
	}
	roTok, _, err := auth.NewService(st).MintToken(t.Context(), "u1", "alice-reader",
		store.ScopeSet{store.ScopeReadInsights}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := del(t, ts.URL+"/v1/works/"+workID+"/translations", roTok); code != http.StatusForbidden {
		t.Fatalf("read-insights token cleared a work: %d %v", code, out)
	}
	if code, out := del(t, ts.URL+"/v1/translations", roTok); code != http.StatusForbidden {
		t.Fatalf("read-insights token cleared the account: %d %v", code, out)
	}

	if code, out := pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("c1"), "translation": "one"},
		map[string]any{"key": trKeyHex("c2"), "translation": "two"},
	); code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}
	if code, out := pushTr(t, ts.URL, other, otherWork,
		map[string]any{"key": trKeyHex("c1"), "translation": "theirs"},
	); code != http.StatusOK {
		t.Fatalf("other push: %d %v", code, out)
	}

	code, out := del(t, ts.URL+"/v1/works/"+workID+"/translations", tok)
	if code != http.StatusOK || out["deleted"] != float64(2) {
		t.Fatalf("clear work: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/works/"+workID+"/translations", tok)
	if code != http.StatusOK || len(out["translations"].([]any)) != 0 {
		t.Fatalf("pull after clear: %d %v", code, out)
	}
	code, out = del(t, ts.URL+"/v1/works/"+workID+"/translations", tok)
	if code != http.StatusOK || out["deleted"] != float64(0) {
		t.Fatalf("repeated clear: %d %v", code, out)
	}
	// The JSON body is what tells a dropped work from a server without
	// the route, the same contract as the pull.
	code, out = del(t, ts.URL+"/v1/works/nope/translations", tok)
	if code != http.StatusNotFound || out["error"] != "work not found" {
		t.Fatalf("clear of unknown work: %d %v", code, out)
	}
	code, out = del(t, ts.URL+"/v1/works/"+otherWork+"/translations", tok)
	if code != http.StatusNotFound {
		t.Fatalf("clear of another account's work: %d %v", code, out)
	}

	if code, out := pushTr(t, ts.URL, tok, workID,
		map[string]any{"key": trKeyHex("c3"), "translation": "three"},
	); code != http.StatusOK {
		t.Fatalf("push after clear: %d %v", code, out)
	}
	code, out = del(t, ts.URL+"/v1/translations", tok)
	if code != http.StatusOK || out["deleted"] != float64(1) {
		t.Fatalf("clear account: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/works/"+otherWork+"/translations", other)
	if code != http.StatusOK || len(out["translations"].([]any)) != 1 {
		t.Fatalf("account clear reached another account: %d %v", code, out)
	}
}
