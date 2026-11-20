package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// vocabToken creates a user and mints a device token with the given
// scopes.
func vocabToken(t *testing.T, st store.Store, id, name string, scopes ...store.Scope) string {
	t.Helper()
	ctx := t.Context()
	if err := st.CreateUser(ctx, store.User{ID: id, Name: name, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.NewService(st).MintToken(ctx, id, name+"-device", store.ScopeSet(scopes), nil)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func vocabWordItem(language, normalized string, baseRev int64) map[string]any {
	return map[string]any{
		"language":   language,
		"normalized": normalized,
		"base_rev":   baseRev,
		"word":       normalized,
		"added_at":   "2025-06-01T10:00:00Z",
		"occurrences": []map[string]any{{
			"occurrence_key": "occ-1",
			"work_id":        "work-1",
			"book_title":     "A Memory Called Empire",
			"location_key":   "chapter-3",
			"locator":        map[string]any{"href": "ch3.xhtml"},
			"sentence":       "the sea was calm",
			"last_seen_at":   "2025-06-01T10:00:00Z",
			"encounters":     2,
		}},
	}
}

func vocabReviewItem(language, normalized, reviewID string, generation int64, rating string) map[string]any {
	return map[string]any{
		"review_id":        reviewID,
		"language":         language,
		"normalized":       normalized,
		"word_generation":  generation,
		"rating":           rating,
		"reviewed_at":      "2025-06-01T10:00:00Z",
		"interval_minutes": 10,
		"due_at":           "2025-06-11T10:00:00Z",
	}
}

func pushWords(t *testing.T, ts, tok string, items ...map[string]any) (int, map[string]any) {
	t.Helper()
	return post(t, ts+"/v1/vocabulary/words", tok, map[string]any{"words": items})
}

func pushReviews(t *testing.T, ts, tok string, items ...map[string]any) (int, map[string]any) {
	t.Helper()
	return post(t, ts+"/v1/vocabulary/reviews", tok, map[string]any{"reviews": items})
}

// postRaw sends a body verbatim, so a test can drive bytes that
// json.Marshal would rewrite (invalid UTF-8) or trailing tokens.
func postRaw(t *testing.T, url, token string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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

func vocabResults(t *testing.T, out map[string]any) []any {
	t.Helper()
	results, ok := out["results"].([]any)
	if !ok {
		t.Fatalf("no results in %v", out)
	}
	return results
}

func TestVocabularyScopeEnforcement(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)
	library := vocabToken(t, st, "u2", "bob", store.ScopeLibraryRead)

	if code, _ := pushWords(t, ts.URL, "", vocabWordItem("en", "word", 0)); code != http.StatusUnauthorized {
		t.Fatalf("anonymous push: %d", code)
	}
	if code, _ := pushWords(t, ts.URL, library, vocabWordItem("en", "word", 0)); code != http.StatusForbidden {
		t.Fatalf("library-read push: %d", code)
	}
	if code, _ := get(t, ts.URL+"/v1/vocabulary/changes?since=0", library); code != http.StatusForbidden {
		t.Fatalf("library-read changes: %d", code)
	}
	if code, _ := get(t, ts.URL+"/v1/vocabulary/changes?since=0", tok); code != http.StatusOK {
		t.Fatalf("sync changes: %d", code)
	}
}

func TestVocabularyFullFlow(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	// Create a word carrying an occurrence and an imported baseline.
	item := vocabWordItem("en", "ephemeral", 0)
	item["definition"] = "lasting a short time"
	item["imported_baseline"] = map[string]any{
		"due_at":           "2025-07-01T10:00:00Z",
		"streak":           3,
		"interval_minutes": 1440,
		"repetitions":      2,
	}
	code, out := pushWords(t, ts.URL, tok, item)
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	res := vocabResults(t, out)[0].(map[string]any)
	if res["status"] != "applied" || res["rev"].(float64) != 1 || res["generation"].(float64) != 1 {
		t.Fatalf("create result: %v", res)
	}
	seq1 := res["seq"].(float64)

	// Round-trip through the delta feed.
	code, out = get(t, ts.URL+"/v1/vocabulary/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("changes: %d %v", code, out)
	}
	if out["high_water"].(float64) != seq1 || out["has_more"] != false {
		t.Fatalf("page contract: %v", out)
	}
	words := out["words"].([]any)
	if len(words) != 1 {
		t.Fatalf("words: %v", out)
	}
	w := words[0].(map[string]any)
	if w["word"] != "ephemeral" || w["definition"] != "lasting a short time" {
		t.Fatalf("word payload: %v", w)
	}
	occs := w["occurrences"].([]any)
	if len(occs) != 1 || occs[0].(map[string]any)["occurrence_key"] != "occ-1" {
		t.Fatalf("occurrences: %v", w)
	}
	if loc, ok := occs[0].(map[string]any)["locator"].(map[string]any); !ok || loc["href"] != "ch3.xhtml" {
		t.Fatalf("locator round-trip: %v", occs[0])
	}
	baseline := w["imported_baseline"].(map[string]any)
	if baseline["streak"].(float64) != 3 || baseline["repetitions"].(float64) != 2 {
		t.Fatalf("baseline: %v", baseline)
	}

	// A review at generation 1 lands.
	code, out = pushReviews(t, ts.URL, tok, vocabReviewItem("en", "ephemeral", "r1", 1, "GOOD"))
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "applied" {
		t.Fatalf("review: %d %v", code, out)
	}

	// Undo it.
	undo := vocabReviewItem("en", "ephemeral", "r1", 1, "GOOD")
	undo["deleted"] = true
	code, out = pushReviews(t, ts.URL, tok, undo)
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "applied" {
		t.Fatalf("undo: %d %v", code, out)
	}

	// Delete the word; its review tombstones travel too.
	del := vocabWordItem("en", "ephemeral", 1)
	del["deleted"] = true
	code, out = pushWords(t, ts.URL, tok, del)
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "applied" {
		t.Fatalf("delete: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/vocabulary/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("changes after delete: %d %v", code, out)
	}
	words = out["words"].([]any)
	deletedWord := words[len(words)-1].(map[string]any)
	if deletedWord["deleted"] != true {
		t.Fatalf("word tombstone: %v", deletedWord)
	}
	reviews := out["reviews"].([]any)
	if len(reviews) == 0 || reviews[len(reviews)-1].(map[string]any)["deleted"] != true {
		t.Fatalf("review tombstone: %v", reviews)
	}

	// Resurrect with the tombstone's rev; generation bumps.
	resurrect := vocabWordItem("en", "ephemeral", 2)
	code, out = pushWords(t, ts.URL, tok, resurrect)
	res = vocabResults(t, out)[0].(map[string]any)
	if code != 200 || res["status"] != "applied" || res["rev"].(float64) != 3 || res["generation"].(float64) != 2 {
		t.Fatalf("resurrect: %d %v", code, res)
	}

	// A review against the dead generation is refused; the live one lands.
	code, out = pushReviews(t, ts.URL, tok, vocabReviewItem("en", "ephemeral", "r2", 1, "EASY"))
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "invalid" {
		t.Fatalf("stale generation review: %d %v", code, out)
	}
	code, out = pushReviews(t, ts.URL, tok, vocabReviewItem("en", "ephemeral", "r3", 2, "EASY"))
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "applied" {
		t.Fatalf("live generation review: %d %v", code, out)
	}
}

func TestVocabularyBounds(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	// Malformed JSON is a whole-request 400.
	if code, _ := post(t, ts.URL+"/v1/vocabulary/words", tok, "not-an-object"); code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d", code)
	}
	// An empty batch is refused.
	if code, _ := pushWords(t, ts.URL, tok); code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d", code)
	}
	// A batch over the configured maximum is refused whole.
	tooMany := make([]map[string]any, 0, 101)
	for i := 0; i < 101; i++ {
		tooMany = append(tooMany, vocabWordItem("en", "w"+strings.Repeat("x", i), 0))
	}
	if code, _ := pushWords(t, ts.URL, tok, tooMany...); code != http.StatusBadRequest {
		t.Fatalf("oversized batch: %d", code)
	}

	// A shape error is a per-item invalid, and its neighbor still lands.
	bad := vocabWordItem("en", "bad-added", 0)
	bad["added_at"] = "not-a-time"
	good := vocabWordItem("en", "good", 0)
	code, out := pushWords(t, ts.URL, tok, bad, good)
	if code != 200 {
		t.Fatalf("mixed batch: %d %v", code, out)
	}
	results := vocabResults(t, out)
	if results[0].(map[string]any)["status"] != "invalid" {
		t.Fatalf("bad added_at accepted: %v", results[0])
	}
	if results[1].(map[string]any)["status"] != "applied" {
		t.Fatalf("neighbor refused: %v", results[1])
	}

	// A bad rating is a per-item invalid.
	code, out = pushReviews(t, ts.URL, tok, vocabReviewItem("en", "good", "r1", 1, "MEH"))
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "invalid" {
		t.Fatalf("bad rating: %d %v", code, out)
	}

	// A word item larger than the per-item cap is refused without
	// unmarshalling, while its neighbor lands.
	huge := vocabWordItem("en", "huge", 0)
	huge["notes"] = strings.Repeat("z", vocabularyMaxPerItemBytes)
	code, out = pushWords(t, ts.URL, tok, huge, vocabWordItem("en", "small", 0))
	if code != 200 {
		t.Fatalf("huge item batch: %d %v", code, out)
	}
	results = vocabResults(t, out)
	if results[0].(map[string]any)["status"] != "invalid" {
		t.Fatalf("huge item accepted: %v", results[0])
	}
	if results[1].(map[string]any)["status"] != "applied" {
		t.Fatalf("neighbor of huge item: %v", results[1])
	}

	// A body larger than any legal batch could need is a 413, but a
	// single oversized item under the raised word body cap is a
	// per-item invalid, not a whole-request rejection.
	giant := vocabWordItem("en", "giant", 0)
	giant["notes"] = strings.Repeat("q", 7<<20)
	if code, _ := pushWords(t, ts.URL, tok, giant); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("giant body: %d", code)
	}
}

func TestVocabularyLiveEvent(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})
	token := f.user(t, "u1", store.ScopeSync)
	s, code := f.open(t, token)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	s.next(t, 5*time.Second) // the opening invalidation

	code, out := pushWords(t, f.ts.URL, token, vocabWordItem("en", "watched", 0))
	if code != 200 {
		t.Fatalf("push: %d %v", code, out)
	}
	if got := s.next(t, 5*time.Second); len(got) != 1 || got[0] != "vocabulary" {
		t.Fatalf("after a vocabulary push: %v", got)
	}

	// A duplicate changes no row and stays silent.
	code, out = pushWords(t, f.ts.URL, token, vocabWordItem("en", "watched", 0))
	if code != 200 || vocabResults(t, out)[0].(map[string]any)["status"] != "duplicate" {
		t.Fatalf("retry push: %d %v", code, out)
	}
	s.silent(t, 300*time.Millisecond)
}

func TestVocabularyLocatorNUL(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	// A NUL inside a nested locator value, one inside an object key, and
	// a valid neighbor: the bad items fail alone, HTTP 200.
	badValue := vocabWordItem("en", "nul-value", 0)
	badValue["occurrences"] = []map[string]any{{
		"occurrence_key": "occ-1",
		"locator":        map[string]any{"nested": map[string]any{"v": "\x00"}},
		"last_seen_at":   "2025-06-01T10:00:00Z",
		"encounters":     1,
	}}
	badKey := vocabWordItem("en", "nul-key", 0)
	badKey["occurrences"] = []map[string]any{{
		"occurrence_key": "occ-1",
		"locator":        map[string]any{"nested": map[string]any{"\x00": "x"}},
		"last_seen_at":   "2025-06-01T10:00:00Z",
		"encounters":     1,
	}}
	good := vocabWordItem("en", "fine", 0)

	code, out := pushWords(t, ts.URL, tok, badValue, badKey, good)
	if code != 200 {
		t.Fatalf("mixed NUL batch: %d %v", code, out)
	}
	results := vocabResults(t, out)
	for i := 0; i < 2; i++ {
		r := results[i].(map[string]any)
		if r["status"] != "invalid" || r["reason"] != "occurrence locator contains NUL" {
			t.Fatalf("NUL locator item %d: %v", i, r)
		}
	}
	if results[2].(map[string]any)["status"] != "applied" {
		t.Fatalf("valid neighbor refused: %v", results[2])
	}
}

func TestVocabularyLocatorNULDuplicateKeys(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	// Duplicate keys are legal JSON and a decoded map keeps only the last
	// one, so the shadowed NUL in each of these locators survives only to
	// a token walk. The neighbor still lands.
	shadowedValue := `{"language":"en","normalized":"nul-shadow-value","base_rev":0,` +
		`"word":"w","added_at":"2025-06-01T10:00:00Z","occurrences":[` +
		`{"occurrence_key":"occ-1","locator":{"nested":{"v":"\u0000","v":"ok"}},` +
		`"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]}`
	shadowedKey := `{"language":"en","normalized":"nul-shadow-key","base_rev":0,` +
		`"word":"w","added_at":"2025-06-01T10:00:00Z","occurrences":[` +
		`{"occurrence_key":"occ-1","locator":{"nested":{"\u0000":"x"},"nested":{}},` +
		`"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]}`
	good, err := json.Marshal(vocabWordItem("en", "fine", 0))
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"words":[` + shadowedValue + `,` + shadowedKey + `,` + string(good) + `]}`)
	code, out := postRaw(t, ts.URL+"/v1/vocabulary/words", tok, body)
	if code != http.StatusOK {
		t.Fatalf("mixed NUL batch: %d %v", code, out)
	}
	results := vocabResults(t, out)
	for i := 0; i < 2; i++ {
		r := results[i].(map[string]any)
		if r["status"] != "invalid" || r["reason"] != "occurrence locator contains NUL" {
			t.Fatalf("shadowed NUL locator item %d: %v", i, r)
		}
	}
	if results[2].(map[string]any)["status"] != "applied" {
		t.Fatalf("valid neighbor refused: %v", results[2])
	}
}

func TestVocabularyInvalidUTF8(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	item := []byte(`{"language":"en","normalized":"bad","base_rev":0,"word":"a`)
	item = append(item, 0xff)
	item = append(item, []byte(`b","added_at":"2025-06-01T10:00:00Z","occurrences":[]}`)...)
	body := append([]byte(`{"words":[`), item...)
	body = append(body, []byte(`]}`)...)

	code, out := postRaw(t, ts.URL+"/v1/vocabulary/words", tok, body)
	if code != 200 {
		t.Fatalf("invalid UTF-8 body: %d %v", code, out)
	}
	res := vocabResults(t, out)[0].(map[string]any)
	if res["status"] != "invalid" || res["reason"] != "invalid UTF-8" {
		t.Fatalf("invalid UTF-8 item: %v", res)
	}
}

func TestVocabularyTrailingTokens(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	item := []byte(`{"language":"en","normalized":"w","base_rev":0,` +
		`"word":"w","added_at":"2025-06-01T10:00:00Z","occurrences":[]}`)
	words := append([]byte(`{"words":[`), item...)
	words = append(words, []byte(`]} {"extra":true}`)...)
	if code, _ := postRaw(t, ts.URL+"/v1/vocabulary/words", tok, words); code != http.StatusBadRequest {
		t.Fatalf("word trailing token: %d", code)
	}

	review := []byte(`{"review_id":"r1","language":"en","normalized":"w",` +
		`"word_generation":1,"rating":"GOOD","reviewed_at":"2025-06-01T10:00:00Z",` +
		`"interval_minutes":10,"due_at":"2025-06-11T10:00:00Z"}`)
	reviews := append([]byte(`{"reviews":[`), review...)
	reviews = append(reviews, []byte(`]} junk`)...)
	if code, _ := postRaw(t, ts.URL+"/v1/vocabulary/reviews", tok, reviews); code != http.StatusBadRequest {
		t.Fatalf("review trailing token: %d", code)
	}
}

func TestVocabularyReviewBodyCap(t *testing.T) {
	ts, st := testServerWithConfig(t, func(c *config.Config) { c.Ops.VocabularyReviewMaxBatch = 1 })
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	if code, out := pushWords(t, ts.URL, tok, vocabWordItem("en", "w", 0)); code != http.StatusOK {
		t.Fatalf("create word: %d %v", code, out)
	}

	// JSON allows whitespace between tokens, so a legal item can be far
	// larger than its fields: the body cap must follow the per-item cap.
	item := `{"review_id":"r1",` + strings.Repeat(" ", 7000) +
		`"language":"en","normalized":"w","word_generation":1,"rating":"GOOD",` +
		`"reviewed_at":"2025-06-01T10:00:00Z","interval_minutes":10,` +
		`"due_at":"2025-06-11T10:00:00Z"}`
	body := []byte(`{"reviews":[` + item + `]}`)
	if len(body) < 7000 {
		t.Fatalf("test body too small: %d", len(body))
	}

	code, out := postRaw(t, ts.URL+"/v1/vocabulary/reviews", tok, body)
	if code != http.StatusOK {
		t.Fatalf("padded review body: %d %v", code, out)
	}
	if st := vocabResults(t, out)[0].(map[string]any)["status"]; st != "applied" {
		t.Fatalf("padded review status: %v", st)
	}
}

func TestVocabularyWordJSONShape(t *testing.T) {
	ts, st := testServer(t)
	tok := vocabToken(t, st, "u1", "alice", store.ScopeSync)

	// A live word with no occurrences still emits "occurrences": [].
	item := vocabWordItem("en", "empty-occ", 0)
	item["occurrences"] = []any{}
	if code, out := pushWords(t, ts.URL, tok, item); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	code, out := get(t, ts.URL+"/v1/vocabulary/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("changes: %d %v", code, out)
	}
	words := out["words"].([]any)
	live := words[len(words)-1].(map[string]any)
	occs, ok := live["occurrences"].([]any)
	if !ok || len(occs) != 0 {
		t.Fatalf("live word occurrences: %v", live)
	}

	// A tombstone omits occurrences entirely (no null) and carries only
	// its identity and bookkeeping.
	del := vocabWordItem("en", "empty-occ", 1)
	del["deleted"] = true
	if code, out := pushWords(t, ts.URL, tok, del); code != 200 {
		t.Fatalf("delete: %d %v", code, out)
	}
	code, out = get(t, ts.URL+"/v1/vocabulary/changes?since=0", tok)
	if code != 200 {
		t.Fatalf("changes after delete: %d %v", code, out)
	}
	words = out["words"].([]any)
	tomb := words[len(words)-1].(map[string]any)
	for _, k := range []string{"word", "notes", "definition", "added_at", "occurrences"} {
		if _, ok := tomb[k]; ok {
			t.Fatalf("tombstone carries %q: %v", k, tomb)
		}
	}
	want := map[string]bool{
		"language": true, "normalized": true, "rev": true, "generation": true,
		"seq": true, "updated_at": true, "deleted": true, "deleted_at": true,
	}
	for k := range tomb {
		if !want[k] {
			t.Fatalf("unexpected tombstone key %q: %v", k, tomb)
		}
	}
	for k := range want {
		if _, ok := tomb[k]; !ok {
			t.Fatalf("tombstone missing %q: %v", k, tomb)
		}
	}
}
