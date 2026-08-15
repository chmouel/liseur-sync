//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// send posts or puts a JSON body, which the catalog fixture's helpers do
// not do: every other catalog route reads its input from the path.
func (f *uploadFixture) send(
	t *testing.T, method, path, token string, body any,
) (*http.Response, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (f *uploadFixture) metadata(t *testing.T, bookID, token string) map[string]any {
	t.Helper()
	resp, raw := f.get(t, "/v1/books/"+bookID+"/metadata", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata: %d %s", resp.StatusCode, raw)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMetadataEditLocksTheFieldAgainstLaterExtraction(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "wrong", "dune (retail) (v2)", []byte("dune bytes"))
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	before := f.metadata(t, bookID, manage)
	resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", manage,
		map[string]any{
			"revision": before["revision"],
			"title":    map[string]any{"value": "Dune"},
			"tags": map[string]any{"entries": []map[string]any{
				{"name": "Science Fiction"}, {"name": "science  fiction"},
			}},
			"contributors": map[string]any{"entries": []map[string]any{
				{"name": "Frank Herbert", "role": "author"},
			}},
			"series": map[string]any{"entries": []map[string]any{
				{"name": "Dune", "position": 1},
			}},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}

	title, _ := out["title"].(map[string]any)
	if title["value"] != "Dune" || title["source"] != "manual" ||
		title["locked"] != true {
		t.Fatalf("edited title: %v", title)
	}
	// A form that sends one tag twice must not produce two, because the
	// store rejects the duplicate rather than tidying it.
	if tags, _ := out["tags"].([]any); len(tags) != 1 {
		t.Fatalf("tags: %v", out["tags"])
	}
	series, _ := out["series"].([]any)
	if len(series) != 1 {
		t.Fatalf("series: %v", out["series"])
	}
	if first, _ := series[0].(map[string]any); first["position"] != 1.0 {
		t.Fatalf("series position: %v", series[0])
	}
	// The revision advanced, so the form that made this edit cannot
	// unknowingly make another on top of it.
	if out["revision"] == before["revision"] {
		t.Fatalf("revision did not advance: %v", out["revision"])
	}

	// The edit is what the catalog now serves.
	resp, raw := f.get(t, "/v1/books/"+bookID, manage)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("book: %d", resp.StatusCode)
	}
	book := map[string]any{}
	if err := json.Unmarshal(raw, &book); err != nil {
		t.Fatal(err)
	}
	if book["title"] != "Dune" {
		t.Fatalf("catalog title after edit: %v", book["title"])
	}
}

// TestMetadataEditRefusesAStaleRevision is the reason the revision is
// required: two librarians editing one book is ordinary, and the loser
// deserves to be told rather than to have their work vanish.
func TestMetadataEditRefusesAStaleRevision(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "contested", "Contested", []byte("contested"))
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	stale := f.metadata(t, bookID, manage)["revision"]
	if resp, _ := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata",
		manage, map[string]any{
			"revision": stale,
			"title":    map[string]any{"value": "First"},
		}); resp.StatusCode != http.StatusOK {
		t.Fatalf("first edit: %d", resp.StatusCode)
	}
	resp, _ := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata",
		manage, map[string]any{
			"revision": stale,
			"title":    map[string]any{"value": "Second"},
		})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale edit: %d, want 409", resp.StatusCode)
	}
	if f.metadata(t, bookID, manage)["title"].(map[string]any)["value"] != "First" {
		t.Fatal("the losing edit was applied anyway")
	}
	// Missing entirely is a bad request rather than a conflict: the
	// client never read the book at all.
	if resp, _ := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata",
		manage, map[string]any{
			"title": map[string]any{"value": "Third"},
		}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("edit with no revision: %d", resp.StatusCode)
	}
}

func TestMetadataEditRequiresTheManageScope(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "guarded", "Guarded", []byte("guarded"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	// Reading provenance needs only read: it is catalog data about the
	// catalog.
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/metadata", read); resp.StatusCode != http.StatusOK {
		t.Fatalf("read scope cannot read metadata: %d", resp.StatusCode)
	}
	resp, _ := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", read,
		map[string]any{"revision": 1, "title": map[string]any{"value": "No"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read scope edited metadata: %d", resp.StatusCode)
	}
	resp, _ = f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", "",
		map[string]any{"revision": 1, "title": map[string]any{"value": "No"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous edit: %d", resp.StatusCode)
	}
}

func TestEntityRoutesListRenameAndMerge(t *testing.T) {
	f := newUploadFixture(t)
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	one, _ := f.publishAs(t, "one", "Mort", []byte("mort bytes"))
	two, _ := f.publishAs(t, "two", "Sourcery", []byte("sourcery bytes"))

	tag := func(bookID, name string) {
		t.Helper()
		rev := f.metadata(t, bookID, manage)["revision"]
		resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata",
			manage, map[string]any{
				"revision": rev,
				"tags": map[string]any{
					"entries": []map[string]any{{"name": name}}},
			})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("tagging: %d %v", resp.StatusCode, out)
		}
	}
	tag(one, "Discworld")
	tag(two, "disc-world")

	base := "/v1/libraries/" + f.library + "/entities/tags"
	resp, raw := f.get(t, base, manage)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entities: %d %s", resp.StatusCode, raw)
	}
	page := map[string]any{}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	entities, _ := page["entities"].([]any)
	if len(entities) != 2 {
		t.Fatalf("entities: %v", page)
	}
	ids := map[string]string{}
	for _, e := range entities {
		row := e.(map[string]any)
		ids[row["name"].(string)] = row["id"].(string)
		if row["book_count"].(float64) != 1 {
			t.Fatalf("book count: %v", row)
		}
	}

	// A rename onto a name that is taken is refused, and the refusal
	// names the operation the caller actually wants.
	resp, out := f.send(t, http.MethodPatch, base+"/"+ids["disc-world"], manage,
		map[string]any{"name": "Discworld"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("colliding rename: %d %v", resp.StatusCode, out)
	}
	if msg, _ := out["error"].(string); msg == "" {
		t.Fatalf("conflict said nothing useful: %v", out)
	}

	resp, out = f.send(t, http.MethodPost, base+"/merge", manage,
		map[string]any{"from": ids["disc-world"], "into": ids["Discworld"]})
	if resp.StatusCode != http.StatusOK || out["moved"] != 1.0 {
		t.Fatalf("merge: %d %v", resp.StatusCode, out)
	}

	resp, raw = f.get(t, base+"/"+ids["Discworld"]+"/books", manage)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entity books: %d %s", resp.StatusCode, raw)
	}
	page = map[string]any{}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if books, _ := page["books"].([]any); len(books) != 2 {
		t.Fatalf("books after merge: %v", page)
	}

	// The losing entity is gone, not merely emptied.
	resp, raw = f.get(t, base, manage)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entities: %d %s", resp.StatusCode, raw)
	}
	page = map[string]any{}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if entities, _ := page["entities"].([]any); len(entities) != 1 {
		t.Fatalf("entities after merge: %s", raw)
	}
}

func TestEntityRoutesRefuseUnknownKindsAndReaders(t *testing.T) {
	f := newUploadFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	base := "/v1/libraries/" + f.library + "/entities"

	// The kind selects a table, so a kind outside the closed set must
	// never reach a query.
	for _, kind := range []string{"libraries", "books", "users"} {
		if resp, _ := f.get(t, base+"/"+kind, read); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("kind %q: %d", kind, resp.StatusCode)
		}
	}
	if resp, _ := f.get(t, base+"/tags", read); resp.StatusCode != http.StatusOK {
		t.Fatalf("a reader cannot browse tags: %d", resp.StatusCode)
	}
	// Reshaping the entities a whole library shares is a manage
	// capability, whatever the caller can read.
	resp, _ := f.send(t, http.MethodPost, base+"/tags/merge", read,
		map[string]any{"from": "a", "into": "b"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a read token merged entities: %d", resp.StatusCode)
	}
	resp, _ = f.send(t, http.MethodPatch, base+"/tags/whatever", read,
		map[string]any{"name": "new"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a read token renamed an entity: %d", resp.StatusCode)
	}
	// A library the caller cannot see is not found, never forbidden: the
	// answer must not tell them it exists.
	if resp, _ := f.get(t, "/v1/libraries/lib-someone-elses/entities/tags",
		read); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another library's entities: %d", resp.StatusCode)
	}
}
