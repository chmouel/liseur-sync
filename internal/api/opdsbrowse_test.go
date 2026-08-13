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
	f := newUploadFixture(t)
	f.publishAs(t, "found", "The Left Hand of Darkness", []byte("left hand bytes"))
	f.publishAs(t, "other", "A Wizard of Earthsea", []byte("earthsea bytes"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, raw := f.opds(t, "/opds/v1.2", "token", read)
	libHref := linkHref(t, parseFeed(t, raw).Entries[0].Links, "subsection")
	_, raw = f.opds(t, libHref, "token", read)
	descHref := linkHref(t, parseFeed(t, raw).Links, "search")
	if descHref == "" {
		t.Fatalf("the library feed advertised no search:\n%s", raw)
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

func TestOPDSBrowsesBySeriesAndContributor(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "browsed", "Dune", []byte("dune bytes"))
	manage := f.mintScopes(t, f.user.ID, "editor",
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	before := f.metadata(t, bookID, manage)
	resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", manage,
		map[string]any{
			"revision": before["revision"],
			"contributors": map[string]any{
				"entries": []map[string]any{{"name": "Frank Herbert", "role": "author"}},
			},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, raw := f.opds(t, "/opds/v1.2", "token", read)
	libHref := linkHref(t, parseFeed(t, raw).Entries[0].Links, "subsection")
	_, raw = f.opds(t, libHref, "token", read)
	facet := ""
	for _, l := range parseFeed(t, raw).Links {
		if l.Rel == "http://opds-spec.org/facet" && strings.HasSuffix(l.Href, "/contributors") {
			facet = l.Href
		}
	}
	if facet == "" {
		t.Fatalf("the library feed offered no way to browse:\n%s", raw)
	}

	resp, raw = f.opds(t, facet, "token", read)
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

func TestOPDSBrowsingRefusesWhatTheCredentialMayNotSee(t *testing.T) {
	f := newUploadFixture(t)
	f.publishAs(t, "hidden", "Dune", []byte("dune bytes"))
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	base := "/opds/v1.2/libraries/" + f.library

	for _, path := range []string{
		base + "/search.xml",
		base + "/search?q=dune",
		base + "/tags",
	} {
		resp, raw := f.opds(t, path, "token", stranger)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404: %s", path, resp.StatusCode, raw)
		}
	}

	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	// An unknown kind names no resource, so it is a 404 rather than a
	// path segment reaching the store.
	resp, _ := f.opds(t, base+"/publishers", "token", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind: %d, want 404", resp.StatusCode)
	}
}
