//go:build linux

package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// searchPath builds a search URL from a query the way a client would,
// so a test never has to think about escaping.
func (f *uploadFixture) searchPath(values url.Values) string {
	return "/v1/libraries/" + f.library + "/search?" + values.Encode()
}

func TestSearchFindsABookByWordsFromItsTitle(t *testing.T) {
	f := newUploadFixture(t)
	wanted, _ := f.publishAs(t, "one", "The Left Hand of Darkness",
		[]byte("left hand bytes"))
	f.publishAs(t, "two", "A Wizard of Earthsea", []byte("earthsea bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, page := getJSON(t, f.ts.URL+f.searchPath(url.Values{
		"q": {"darkness"},
	}), read)
	if code != http.StatusOK {
		t.Fatalf("code = %d: %v", code, page)
	}
	books, _ := page["books"].([]any)
	if len(books) != 1 {
		t.Fatalf("books = %v, want exactly the matching one", page["books"])
	}
	first, _ := books[0].(map[string]any)
	if first["book_id"] != wanted {
		t.Fatalf("book id = %v, want %s", first["book_id"], wanted)
	}
	if page["truncated"] != false {
		t.Fatalf("truncated = %v, want false", page["truncated"])
	}
}

func TestSearchFacetsNarrowToTheBooksTheyDescribe(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "tagged", "Tehanu", []byte("tehanu bytes"))
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	before := f.metadata(t, bookID, manage)
	resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", manage,
		map[string]any{
			"revision": before["revision"],
			"tags": map[string]any{
				"entries": []map[string]any{{"name": "Fantasy"}},
			},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}

	code, page := getJSON(t, f.ts.URL+f.searchPath(url.Values{
		"q": {"tehanu"},
	}), manage)
	if code != http.StatusOK {
		t.Fatalf("code = %d: %v", code, page)
	}
	facets, _ := page["facets"].([]any)
	var tagID string
	for _, raw := range facets {
		facet, _ := raw.(map[string]any)
		if facet["kind"] == "tag" && facet["name"] == "Fantasy" {
			tagID, _ = facet["id"].(string)
		}
	}
	if tagID == "" {
		t.Fatalf("facets = %v, want the tag the book claims", page["facets"])
	}

	// The id a facet hands back must be usable as a filter without the
	// client having to say what kind of thing it is.
	code, filtered := getJSON(t, f.ts.URL+f.searchPath(url.Values{
		"entity": {tagID},
	}), manage)
	if code != http.StatusOK {
		t.Fatalf("filtered: %d %v", code, filtered)
	}
	books, _ := filtered["books"].([]any)
	if len(books) != 1 {
		t.Fatalf("filtered books = %v, want the tagged one", filtered["books"])
	}
}

func TestSearchRefusesNonsenseWithoutFailing(t *testing.T) {
	f := newUploadFixture(t)
	f.publishAs(t, "one", "Dune", []byte("dune bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	// Punctuation is words to match, not index syntax, so a query made
	// only of it finds nothing and says so rather than erroring.
	code, page := getJSON(t, f.ts.URL+f.searchPath(url.Values{
		"q": {`" OR *:* --`},
	}), read)
	if code != http.StatusOK {
		t.Fatalf("code = %d: %v", code, page)
	}
	if books, _ := page["books"].([]any); len(books) != 0 {
		t.Fatalf("books = %v, want none", page["books"])
	}

	for _, limit := range []string{"0", "-3", "abc", "1000"} {
		code, out := getJSON(t, f.ts.URL+f.searchPath(url.Values{
			"q": {"dune"}, "limit": {limit},
		}), read)
		if code != http.StatusBadRequest {
			t.Fatalf("limit %q: code = %d, want 400: %v", limit, code, out)
		}
	}
}

func TestSearchIsScopedToWhatTheCallerMayRead(t *testing.T) {
	f := newUploadFixture(t)
	f.publishAs(t, "one", "Dune", []byte("dune bytes"))
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	// A library the caller cannot read is a library that does not exist,
	// so search must not confirm its contents by any other status.
	code, out := getJSON(t, f.ts.URL+f.searchPath(url.Values{"q": {"dune"}}), stranger)
	if code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %v", code, out)
	}

	if code, _ := getJSON(t, f.ts.URL+f.searchPath(url.Values{"q": {"dune"}}), ""); code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

// TestDuplicatesReportsTheWeakerKindToo covers ADR-0010 phase 2 over the
// wire: the guess rides along with the certainty, on one route, because
// a librarian arrived with one question.
func TestDuplicatesReportsTheWeakerKindToo(t *testing.T) {
	f := newUploadFixture(t)
	firstID, _ := f.publishAs(t, "build-one", "Dune", []byte("one build of dune"))
	secondID, _ := f.publishAs(t, "build-two", "DUNE!", []byte("another build entirely"))
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	for _, id := range []string{firstID, secondID} {
		before := f.metadata(t, id, manage)
		resp, out := f.send(t, http.MethodPut, "/v1/books/"+id+"/metadata", manage,
			map[string]any{
				"revision": before["revision"],
				"contributors": map[string]any{
					"entries": []map[string]any{{"name": "Frank Herbert", "role": "author"}},
				},
			})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("edit %s: %d %v", id, resp.StatusCode, out)
		}
	}

	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+f.library+"/duplicates", manage)
	if code != http.StatusOK {
		t.Fatalf("code = %d: %v", code, page)
	}
	// Different bytes, so the certain report must stay silent about them.
	if groups, _ := page["duplicates"].([]any); len(groups) != 0 {
		t.Fatalf("exact duplicates = %v, want none", page["duplicates"])
	}
	similar, _ := page["similar"].([]any)
	if len(similar) != 1 {
		t.Fatalf("similar = %v, want one group", page["similar"])
	}
	group, _ := similar[0].(map[string]any)
	if group["normalized_title"] != "dune" {
		t.Fatalf("normalized_title = %v", group["normalized_title"])
	}
	books, _ := group["books"].([]any)
	if len(books) != 2 {
		t.Fatalf("group books = %v", group["books"])
	}
}
