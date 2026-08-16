//go:build linux

package api

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// parsedSearchDescription is the loose view of the OpenSearch document, for
// the same reason parsedFeed is loose: what matters is what a reader can
// read out of it.
type parsedSearchDescription struct {
	XMLName   xml.Name `xml:"OpenSearchDescription"`
	ShortName string   `xml:"ShortName"`
	URLs      []struct {
		Type     string `xml:"type,attr"`
		Template string `xml:"template,attr"`
	} `xml:"Url"`
}

// TestOPDSReaderDiscoversSearchAndUsesIt walks the way a reader does: it
// is told nothing but the root, and has to find the search from there.
func TestOPDSReaderDiscoversSearchAndUsesIt(t *testing.T) {
	f := newFolderFixture(t)
	f.publishAs(t, "found", "The Left Hand of Darkness", []byte("left hand bytes"))
	f.publishAs(t, "other", "A Wizard of Earthsea", []byte("earthsea bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, raw := f.opds(t, "/opds/v1.2", "token", read)
	folderHref := linkHref(t, parseFeed(t, raw).Entries[0].Links, "subsection")
	_, raw = f.opds(t, folderHref, "token", read)
	descHref := linkHref(t, parseFeed(t, raw).Links, "search")
	if descHref == "" {
		t.Fatalf("the folder feed advertised no search:\n%s", raw)
	}

	resp, raw := f.opds(t, descHref, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("description: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "opensearchdescription") {
		t.Fatalf("description content-type = %q", ct)
	}
	var desc parsedSearchDescription
	if err := xml.Unmarshal(raw, &desc); err != nil {
		t.Fatalf("description is not valid XML: %v\n%s", err, raw)
	}
	if len(desc.URLs) != 1 || !strings.Contains(desc.URLs[0].Template, "{searchTerms}") {
		t.Fatalf("no usable template: %+v", desc.URLs)
	}

	// A reader substitutes the term and asks; nothing else is offered to
	// substitute, which is how reading state stays out of this surface.
	href := strings.ReplaceAll(desc.URLs[0].Template, "{searchTerms}",
		url.QueryEscape("darkness"))
	resp, raw = f.opds(t, href, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d %s", resp.StatusCode, raw)
	}
	results := parseFeed(t, raw)
	if len(results.Entries) != 1 {
		t.Fatalf("results = %+v", results.Entries)
	}
	if results.Entries[0].Title != "The Left Hand of Darkness" {
		t.Fatalf("found %q", results.Entries[0].Title)
	}
	// Every result must still be downloadable: a search feed a reader
	// cannot acquire from is a list, not a catalog.
	if linkHref(t, results.Entries[0].Links, opdsAcquisitionRel) == "" {
		t.Fatalf("no acquisition link: %+v", results.Entries[0])
	}
}

// TestOPDSBrowsesBySeriesAndContributor walks the facet links from the
// folder feed to a contributor and on to their books. The contributor
// comes from a real dc:creator, since there is no metadata-edit route
// left to attach one after the fact (ADR-0017 deleted it).
func TestOPDSBrowsesBySeriesAndContributor(t *testing.T) {
	f := newFolderFixture(t)
	f.publishWithAuthor(t, "browsed", "Dune", "Frank Herbert", []byte("dune bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, raw := f.opds(t, "/opds/v1.2", "token", read)
	folderHref := linkHref(t, parseFeed(t, raw).Entries[0].Links, "subsection")
	_, raw = f.opds(t, folderHref, "token", read)
	facet := ""
	for _, l := range parseFeed(t, raw).Links {
		if l.Rel == "http://opds-spec.org/facet" && strings.HasSuffix(l.Href, "/contributors") {
			facet = l.Href
		}
	}
	if facet == "" {
		t.Fatalf("the folder feed offered no way to browse:\n%s", raw)
	}

	resp, raw := f.opds(t, facet, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("contributors: %d %s", resp.StatusCode, raw)
	}
	people := parseFeed(t, raw)
	if len(people.Entries) != 1 || people.Entries[0].Title != "Frank Herbert" {
		t.Fatalf("contributors = %+v", people.Entries)
	}
	booksHref := linkHref(t, people.Entries[0].Links, "subsection")
	if booksHref == "" {
		t.Fatalf("no link to the contributor's books: %+v", people.Entries[0])
	}

	resp, raw = f.opds(t, booksHref, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("books: %d %s", resp.StatusCode, raw)
	}
	books := parseFeed(t, raw)
	if len(books.Entries) != 1 || books.Entries[0].Title != "Dune" {
		t.Fatalf("books = %+v", books.Entries)
	}
	if linkHref(t, books.Entries[0].Links, opdsAcquisitionRel) == "" {
		t.Fatalf("no acquisition link: %+v", books.Entries[0])
	}
}

// TestOPDSEntitiesRefuseAnUnknownKind covers the one input-validation edge
// left in this route now that there is no per-folder ACL to test: a kind
// segment that names no table is a 404, not a query that reaches SQL with
// a caller's string spliced into a table name.
func TestOPDSEntitiesRefuseAnUnknownKind(t *testing.T) {
	f := newFolderFixture(t)
	f.publishAs(t, "present", "Dune", []byte("dune bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, _ := f.opds(t, "/opds/v1.2/folders/"+f.folder.ID+"/publishers", "token", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind: %d, want 404", resp.StatusCode)
	}
}

func TestRecentlyAddedIsTheCatalogFromTheOtherEnd(t *testing.T) {
	f := newFolderFixture(t)
	f.publishAs(t, "first", "The Older One", []byte("older bytes"))
	f.publishAs(t, "second", "The Newer One", []byte("newer bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	path := "/v1/folders/" + f.folder.ID + "/books?order=recent"
	code, page := getJSON(t, f.ts.URL+path, read)
	if code != http.StatusOK {
		t.Fatalf("code = %d: %v", code, page)
	}
	books, _ := page["books"].([]any)
	if len(books) != 2 {
		t.Fatalf("books = %v", page["books"])
	}
	first, _ := books[0].(map[string]any)
	if first["title"] != "The Newer One" {
		t.Fatalf("newest first put %v first", first["title"])
	}

	// A typo must not silently read the catalog backwards.
	code, _ = getJSON(t, f.ts.URL+"/v1/folders/"+f.folder.ID+"/books?order=newest", read)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown order: %d, want 400", code)
	}

	// The same order is a feed a reader finds from the folder's own.
	_, raw := f.opds(t, "/opds/v1.2", "token", read)
	folderHref := linkHref(t, parseFeed(t, raw).Entries[0].Links, "subsection")
	_, raw = f.opds(t, folderHref, "token", read)
	recent := linkHref(t, parseFeed(t, raw).Links, "http://opds-spec.org/sort/new")
	if recent == "" {
		t.Fatalf("no recently-added link:\n%s", raw)
	}
	resp, raw := f.opds(t, recent, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recent: %d %s", resp.StatusCode, raw)
	}
	feed := parseFeed(t, raw)
	if len(feed.Entries) != 2 || feed.Entries[0].Title != "The Newer One" {
		t.Fatalf("recent feed = %+v", feed.Entries)
	}
}
