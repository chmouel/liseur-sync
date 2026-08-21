package webui_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// facetLink pulls the href of the facet named in the results page, which
// is what a person clicks to narrow. Reading it out of the HTML rather
// than building it is the point: the link the page offers is the thing
// under test.
func facetLink(t *testing.T, html, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(
		`href="([^"]*search\?[^"]*)"[^>]*>\s*` + regexp.QuoteMeta(name) + `\s*<`)
	m := pattern.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no facet link for %q in:\n%s", name, html)
	}
	return strings.ReplaceAll(m[1], "&amp;", "&")
}

func TestSearchPageFindsABookAndNarrowsByItsFacets(t *testing.T) {
	f := newBooksFixture(t)
	f.observe(t, store.ObservedBook{
		RelativePath: "left-hand.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256:    strings.Repeat("1", 64),
		OriginalFilename: "left-hand.epub", MediaType: "application/epub+zip",
		Title: "The Left Hand of Darkness",
		Tags:  []string{"Fantasy"},
	})

	base := "/ui/folders/" + f.folder + "/search"
	_, page := f.get(t, base+"?q=darkness", f.cookie)
	if !strings.Contains(page, "The Left Hand of Darkness") {
		t.Fatalf("search missed the book:\n%s", page)
	}

	// The facet link must carry the query along, or narrowing would
	// silently widen the search back to the whole library.
	link := facetLink(t, page, "Fantasy")
	if !strings.Contains(link, "q=darkness") {
		t.Fatalf("facet link dropped the query: %s", link)
	}
	// The page's links are relative, so resolve one the way a browser
	// would rather than assuming a shape for them.
	here, err := url.Parse(base + "?q=darkness")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	_, narrowed := f.get(t, here.ResolveReference(target).String(), f.cookie)
	if !strings.Contains(narrowed, "The Left Hand of Darkness") {
		t.Fatalf("narrowing lost the book:\n%s", narrowed)
	}
	if !strings.Contains(narrowed, "Narrowed to") {
		t.Fatalf("the page did not say what it had narrowed to:\n%s", narrowed)
	}
}

func TestSearchPageSaysNothingUntilItIsAsked(t *testing.T) {
	f := newBooksFixture(t)
	bookWithMetadata(t, f, "quiet")

	_, page := f.get(t, "/ui/folders/"+f.folder+"/search", f.cookie)
	// An empty box must not read as an empty library.
	if strings.Contains(page, "Nothing matched") {
		t.Fatalf("an unasked search reported a failure:\n%s", page)
	}
	if !strings.Contains(page, "Find a book") {
		t.Fatalf("no search box on the search page:\n%s", page)
	}

	_, none := f.get(t, "/ui/folders/"+f.folder+"/search?q=zzzzznothing", f.cookie)
	if !strings.Contains(none, "Nothing matched") {
		t.Fatalf("a failed search said nothing about it:\n%s", none)
	}
}

func TestSearchIsOfferedOnTheLibraryPageAndScopedToTheLibrary(t *testing.T) {
	f := newBooksFixture(t)
	bookWithMetadata(t, f, "offered")

	_, books := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if !strings.Contains(books, `action="folders/`+f.folder+`/search"`) {
		t.Fatalf("the library page offered no way to search it:\n%s", books)
	}
	if strings.Contains(books, `class="topsearch"`) {
		t.Fatalf("the library page still has a second, ambiguous top-bar search:\n%s", books)
	}

	// Every signed-in account may search every folder (ADR-0017); a
	// folder that does not exist is a 404 rather than an empty page.
	resp, _ := f.get(t, "/ui/folders/"+f.folder+"/search?q=offered", f.readerCookie(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a second account could not search: %d", resp.StatusCode)
	}
	resp, _ = f.get(t, "/ui/folders/00000000-0000-0000-0000-000000000000/search?q=x", f.cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a folder that is not there answered %d, want 404", resp.StatusCode)
	}
}
