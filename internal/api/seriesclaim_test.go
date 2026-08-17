//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Series claims over the API (ADR-0018). The store suite already covers
// layering itself; these tests are about the things only the HTTP edge
// can get wrong: who may write which layer, that one reader's claim
// never leaks into another's catalog, and that bad input is a 4xx.

// sendJSON is a small local helper: the fixture's req sends no body, and
// every route here takes one.
func (f *folderFixture) sendJSON(
	t *testing.T, method, path, token string, body any,
) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: body is not JSON: %s", method, path, raw)
		}
	}
	return resp.StatusCode, out
}

// seriesNames reads the series names off a book payload, which is the
// shape every catalog route returns.
func seriesNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["series"].([]any)
	if !ok {
		t.Fatalf("no series in %v", body)
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.(map[string]any)["name"].(string))
	}
	return out
}

func seriesSources(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, _ := body["series"].([]any)
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.(map[string]any)["source"].(string))
	}
	return out
}

// TestSeriesClaimIsPrivateToItsClaimant is the tenant-isolation test for
// the bounded user-scoping ADR-0018 introduces. The catalog is shared,
// so both readers see the same book; a personal claim about it is not
// catalog data and must not travel.
func TestSeriesClaimIsPrivateToItsClaimant(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))

	mine := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	theirs := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	pos := 1.0
	status, _ := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series",
		mine, map[string]any{"series": []map[string]any{
			{"name": "Dune Chronicles", "position": pos},
		}})
	if status != http.StatusOK {
		t.Fatalf("setting a personal claim: %d", status)
	}

	status, body := getJSON(t, f.ts.URL+"/v1/books/"+bookID, mine)
	if status != http.StatusOK {
		t.Fatalf("reading my own book: %d", status)
	}
	if got := seriesNames(t, body); len(got) != 1 || got[0] != "Dune Chronicles" {
		t.Errorf("claimant sees %v, want the claimed series", got)
	}
	if got := seriesSources(t, body); got[0] != "personal" {
		t.Errorf("source is %q, want personal", got[0])
	}

	status, body = getJSON(t, f.ts.URL+"/v1/books/"+bookID, theirs)
	if status != http.StatusOK {
		t.Fatalf("stranger reading the shared catalog: %d", status)
	}
	if got := seriesNames(t, body); len(got) != 0 {
		t.Errorf("a personal series claim leaked to another reader: %v", got)
	}

	// The entity listing is the other way a claim could leak: it counts
	// books per series, and a count is a fact about somebody's library.
	status, listing := getJSON(t,
		f.ts.URL+"/v1/entities/series", theirs)
	if status != http.StatusOK {
		t.Fatalf("stranger listing series: %d", status)
	}
	for _, e := range listing["entities"].([]any) {
		row := e.(map[string]any)
		if row["name"] == "Dune Chronicles" && row["book_count"].(float64) > 0 {
			t.Errorf("a personal claim gave a stranger a non-empty series: %v", row)
		}
	}
}

// TestSharedSeriesClaimNeedsAdmin: library-manage lets a reader state
// what they think; only an admin states what everybody sees.
func TestSharedSeriesClaimNeedsAdmin(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))

	reader := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	// An admin token only exists on an admin account (ADR-0013).
	boss := f.createUser(t, "boss")
	if err := f.st.SetUserAdmin(t.Context(), boss.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := f.mintToken(t, boss.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage, store.ScopeAdmin)
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	claim := map[string]any{"scope": "shared", "series": []map[string]any{
		{"name": "Dune Chronicles"},
	}}
	if status, _ := f.sendJSON(t, http.MethodPut,
		"/v1/books/"+bookID+"/series", reader, claim); status != http.StatusForbidden {
		t.Fatalf("non-admin writing the shared layer: %d, want 403", status)
	}
	if status, _ := f.sendJSON(t, http.MethodPut,
		"/v1/books/"+bookID+"/series", admin, claim); status != http.StatusOK {
		t.Fatalf("admin writing the shared layer: %d, want 200", status)
	}
	// The shared layer is what everyone without a claim of their own
	// sees, including a reader who cannot write it.
	_, body := getJSON(t, f.ts.URL+"/v1/books/"+bookID, stranger)
	if got := seriesNames(t, body); len(got) != 1 || got[0] != "Dune Chronicles" {
		t.Errorf("stranger sees %v, want the shared claim", got)
	}
	if got := seriesSources(t, body); got[0] != "shared" {
		t.Errorf("source is %q, want shared", got[0])
	}
	// And clearing it is admin's too.
	if status, _ := f.sendJSON(t, http.MethodDelete,
		"/v1/books/"+bookID+"/series?scope=shared", reader, nil,
	); status != http.StatusForbidden {
		t.Errorf("non-admin clearing the shared layer: %d, want 403", status)
	}
}

// TestSeriesClaimLayersRoute covers the read an editor needs: what the
// folder said underneath, so a client knows whether a reset does
// anything.
func TestSeriesClaimLayersRoute(t *testing.T) {
	f := newFolderFixture(t)
	// A book in a subdirectory: a plain folder reads the directory name
	// as a series, which is the folder layer this test rests on.
	bookID, _ := f.writeBook(t, "Foundation/one.epub", []byte("one"))

	tok := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	status, body := getJSON(t, f.ts.URL+"/v1/books/"+bookID+"/series", tok)
	if status != http.StatusOK {
		t.Fatalf("reading layers: %d", status)
	}
	if body["source"] != "folder" {
		t.Errorf("source is %v, want folder", body["source"])
	}
	if body["personal"] != nil || body["shared"] != nil {
		t.Errorf("unclaimed layers should be null, got %v / %v",
			body["personal"], body["shared"])
	}
	if len(body["folder"].([]any)) != 1 {
		t.Fatalf("folder layer is %v, want the scanned series", body["folder"])
	}

	// An empty claim is not the absence of one: it says "in no series",
	// and it must override the folder rather than fall through to it.
	if status, _ := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series",
		tok, map[string]any{"series": []map[string]any{}}); status != http.StatusOK {
		t.Fatalf("claiming no series: %d", status)
	}
	_, body = getJSON(t, f.ts.URL+"/v1/books/"+bookID+"/series", tok)
	if body["personal"] == nil {
		t.Error("an empty claim read back as no claim at all")
	}
	if body["personal_updated_at"] == nil {
		t.Error("an existing personal claim has no revision")
	}
	if got := len(body["series"].([]any)); got != 0 {
		t.Errorf("effective series is %d long, want empty", got)
	}
	if len(body["folder"].([]any)) != 1 {
		t.Error("a claim rewrote what the folder observed")
	}

	// Clearing falls back to the folder.
	status, body = f.sendJSON(t, http.MethodDelete,
		"/v1/books/"+bookID+"/series", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("clearing: %d", status)
	}
	if body["source"] != "folder" {
		t.Errorf("after a reset the source is %v, want folder", body["source"])
	}
	if len(body["series"].([]any)) != 1 {
		t.Errorf("a reset did not restore the folder's series: %v", body["series"])
	}
}

func TestCatalogSeriesClaimMarkerAndRevision(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.writeBook(t, "Foundation/one.epub", []byte("one"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeLibraryManage)
	clientTS := "1970-01-01T00:00:00Z"
	status, body := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series", tok,
		map[string]any{"series": []map[string]any{}, "client_ts": clientTS})
	if status != http.StatusOK || body["outcome"] != "applied" {
		t.Fatalf("setting empty claim: %d %v", status, body)
	}
	_, body = getJSON(t, f.ts.URL+"/v1/books/"+bookID, tok)
	if body["series_source"] != "personal" {
		t.Errorf("series_source = %v, want personal", body["series_source"])
	}
	revision, ok := body["series_claim_updated_at"].(string)
	if !ok || revision == clientTS {
		t.Errorf("series claim revision = %v, want server revision distinct from %s", body["series_claim_updated_at"], clientTS)
	}
	if len(body["series"].([]any)) != 0 {
		t.Errorf("empty claim returned memberships: %v", body["series"])
	}
	other := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	_, body = getJSON(t, f.ts.URL+"/v1/books/"+bookID, other)
	if body["series_source"] != "folder" {
		t.Errorf("other reader source = %v, want folder", body["series_source"])
	}
	if _, ok := body["series_claim_updated_at"]; ok {
		t.Error("personal claim revision leaked to another reader")
	}
}

func TestSeriesClaimIdempotencyAndPreconditions(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeLibraryManage)
	path := "/v1/books/" + bookID + "/series"
	firstKey := "2100-01-01T00:00:00Z"
	status, body := f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"client_ts": firstKey, "series": []map[string]any{{"name": "First"}},
	})
	if status != http.StatusOK || body["outcome"] != "applied" {
		t.Fatalf("first put: %d %v", status, body)
	}
	revision := body["personal_updated_at"].(string)
	if revision == firstKey {
		t.Fatalf("client timestamp became server revision: %s", revision)
	}
	status, body = f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"client_ts": firstKey, "series": []map[string]any{{"name": "First"}},
	})
	if status != http.StatusOK || body["outcome"] != "duplicate" {
		t.Fatalf("duplicate put: %d %v", status, body)
	}
	if body["personal_updated_at"] != revision {
		t.Errorf("duplicate changed revision: %v, want %s", body["personal_updated_at"], revision)
	}
	status, body = f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"client_ts": firstKey, "series": []map[string]any{{"name": "Different"}},
	})
	if status != http.StatusConflict {
		t.Fatalf("reused key with different claim: %d %v", status, body)
	}
	status, body = f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"client_ts": "new-key", "if_updated_at": "1970-01-01T00:00:00Z",
		"series": []map[string]any{{"name": "Stale"}},
	})
	if status != http.StatusOK || body["outcome"] != "stale" {
		t.Fatalf("stale precondition put: %d %v", status, body)
	}
	if got := seriesNames(t, body); len(got) != 1 || got[0] != "First" {
		t.Errorf("stale put replaced current claim: %v", got)
	}
	status, body = f.sendJSON(t, http.MethodDelete,
		path+"?client_ts=delete-key&if_updated_at=1970-01-01T00:00:00Z", tok, nil)
	if status != http.StatusOK || body["outcome"] != "stale" {
		t.Fatalf("stale delete: %d %v", status, body)
	}
	if got := seriesNames(t, body); len(got) != 1 || got[0] != "First" {
		t.Errorf("stale delete removed current claim: %v", got)
	}
	status, body = f.sendJSON(t, http.MethodDelete,
		path+"?client_ts=delete-key&if_updated_at="+revision, tok, nil)
	if status != http.StatusOK || body["outcome"] != "applied" {
		t.Fatalf("delete: %d %v", status, body)
	}
	status, body = f.sendJSON(t, http.MethodDelete, path+"?client_ts=delete-key", tok, nil)
	if status != http.StatusOK || body["outcome"] != "duplicate" {
		t.Fatalf("duplicate delete: %d %v", status, body)
	}
	status, body = f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"client_ts": "old-put", "if_updated_at": revision,
		"series": []map[string]any{{"name": "Revived"}},
	})
	if status != http.StatusOK || body["outcome"] != "stale" {
		t.Fatalf("old put after delete: %d %v", status, body)
	}
	if body["source"] != "folder" {
		t.Errorf("old put revived deleted claim: %v", body)
	}
}

// TestSeriesClaimRejectsBadInput: malformed input is a precise 4xx and
// never a 5xx.
func TestSeriesClaimRejectsBadInput(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))
	tok := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	cases := []struct {
		name string
		body any
		want int
	}{
		{"unknown scope", map[string]any{
			"scope": "everyone", "series": []map[string]any{{"name": "X"}},
		}, http.StatusBadRequest},
		{"folder scope is not writable", map[string]any{
			"scope": "folder", "series": []map[string]any{{"name": "X"}},
		}, http.StatusBadRequest},
		{"neither id nor name", map[string]any{
			"series": []map[string]any{{"position": 1}},
		}, http.StatusBadRequest},
		{"both id and name", map[string]any{
			"series": []map[string]any{{"series_id": "abc", "name": "X"}},
		}, http.StatusBadRequest},
		{"blank name", map[string]any{
			"series": []map[string]any{{"name": "   "}},
		}, http.StatusBadRequest},
		{"unknown series id", map[string]any{
			"series": []map[string]any{{"series_id": "nope"}},
		}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.sendJSON(t, http.MethodPut,
				"/v1/books/"+bookID+"/series", tok, tc.body)
			if status != tc.want {
				t.Fatalf("got %d, want %d (%v)", status, tc.want, body)
			}
			if _, ok := body["error"]; !ok {
				t.Errorf("no error message in %v", body)
			}
		})
	}

	// A claim about a book that is not there is a 404, not a 500.
	if status, _ := f.sendJSON(t, http.MethodPut, "/v1/books/nope/series",
		tok, map[string]any{"series": []map[string]any{{"name": "X"}}},
	); status != http.StatusNotFound {
		t.Errorf("claiming a missing book: %d, want 404", status)
	}
}

// TestSeriesReorderRoute drives the bulk renumbering the app's
// drag-reorder needs, and checks it comes back out in reading order.
func TestSeriesReorderRoute(t *testing.T) {
	f := newFolderFixture(t)
	first, _ := f.writeBook(t, "Foundation/b.epub", []byte("b"))
	second, _ := f.writeBook(t, "Foundation/a.epub", []byte("a"))
	tok := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	_, layers := getJSON(t, f.ts.URL+"/v1/books/"+first+"/series", tok)
	seriesID := layers["folder"].([]any)[0].(map[string]any)["id"].(string)

	path := "/v1/entities/series/" + seriesID + "/order"
	status, body := f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"order": []map[string]any{
			{"book_id": second, "position": 1},
			{"book_id": first, "position": 2},
		},
	})
	if status != http.StatusNoContent {
		t.Fatalf("reordering: %d (%v)", status, body)
	}

	status, listing := getJSON(t, f.ts.URL+"/v1/entities/series/"+seriesID+"/books", tok)
	if status != http.StatusOK {
		t.Fatalf("listing a series: %d", status)
	}
	books := listing["books"].([]any)
	if len(books) != 2 {
		t.Fatalf("got %d books, want 2", len(books))
	}
	if got := books[0].(map[string]any)["book_id"]; got != second {
		t.Errorf("reading order starts with %v, want the book placed first", got)
	}

	// The same reorder twice is the same result: a client that retries a
	// dropped response must not renumber twice.
	if status, _ := f.sendJSON(t, http.MethodPut, path, tok, map[string]any{
		"order": []map[string]any{
			{"book_id": second, "position": 1},
			{"book_id": first, "position": 2},
		},
	}); status != http.StatusNoContent {
		t.Fatalf("replaying a reorder: %d", status)
	}

	cases := []struct {
		name string
		body any
	}{
		{"empty order", map[string]any{"order": []map[string]any{}}},
		{"no book id", map[string]any{
			"order": []map[string]any{{"position": 1}},
		}},
		{"the same book twice", map[string]any{
			"order": []map[string]any{
				{"book_id": first, "position": 1},
				{"book_id": first, "position": 2},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if status, _ := f.sendJSON(t, http.MethodPut, path, tok, tc.body); status != http.StatusBadRequest {
				t.Errorf("got %d, want 400", status)
			}
		})
	}

	// Only a series can be reordered; the route's {kind} is shared with
	// the entity routes and must refuse the rest.
	if status, _ := f.sendJSON(t, http.MethodPut,
		"/v1/entities/tags/"+seriesID+"/order",
		tok, map[string]any{"order": []map[string]any{{"book_id": first}}},
	); status != http.StatusNotFound {
		t.Errorf("reordering tags: %d, want 404", status)
	}
}
