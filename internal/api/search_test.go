//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// searchPath builds a search URL from a query the way a client would,
// so a test never has to think about escaping.
func (f *folderFixture) searchPath(values url.Values) string {
	return "/v1/folders/" + f.folder.ID + "/search?" + values.Encode()
}

// makeEPUBWithSubject is makeEPUB plus a dc:subject, which the reconciler
// reads straight into a book's tags (pass_linux.go: obs.Tags =
// m.Subjects). It exists because the metadata-edit route that used to
// attach a tag after the fact is gone; a tag now only ever comes from the
// publication itself.
func makeEPUBWithSubject(t *testing.T, title, subject string, extra []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string, method uint16) {
		t.Helper()
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	add("OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">`+
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`+
		`<dc:title>`+title+`</dc:title><dc:subject>`+subject+`</dc:subject>`+
		`</metadata><manifest><item href="nav.xhtml"`+
		` media-type="application/xhtml+xml" properties="nav"/></manifest></package>`,
		zip.Deflate)
	add("OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml">`+
		`<body><nav/><!-- `+string(extra)+` --></body></html>`, zip.Deflate)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSearchFindsABookByWordsFromItsTitle(t *testing.T) {
	f := newFolderFixture(t)
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

// TestSearchFacetsNarrowToTheBooksTheyDescribe pins the facet contract
// end to end: a tag the search reports back must itself be a working
// filter, without the client having to know it was a tag rather than a
// series or a contributor.
func TestSearchFacetsNarrowToTheBooksTheyDescribe(t *testing.T) {
	f := newFolderFixture(t)
	f.writeBook(t, "tagged.epub", makeEPUBWithSubject(t, "Tehanu", "Fantasy", []byte("tehanu bytes")))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, page := getJSON(t, f.ts.URL+f.searchPath(url.Values{
		"q": {"tehanu"},
	}), read)
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
	}), read)
	if code != http.StatusOK {
		t.Fatalf("filtered: %d %v", code, filtered)
	}
	books, _ := filtered["books"].([]any)
	if len(books) != 1 {
		t.Fatalf("filtered books = %v, want the tagged one", filtered["books"])
	}
}

func TestSearchRefusesNonsenseWithoutFailing(t *testing.T) {
	f := newFolderFixture(t)
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
