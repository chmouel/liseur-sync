package webui_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// facetLink pulls the href of the facet named in the results page, which
// is what a person clicks to narrow. Reading it out of the HTML rather
// than building it is the point: the link the page offers is the thing
// under test.
func facetLink(t *testing.T, html, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(
		`href="([^"]*search\?[^"]*)"[^>]*>` + regexp.QuoteMeta(name) + `<`)
	m := pattern.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no facet link for %q in:\n%s", name, html)
	}
	return strings.ReplaceAll(m[1], "&amp;", "&")
}

func TestSearchPageFindsABookAndNarrowsByItsFacets(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "searchable")
	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	resp := saveMetadata(t, f, f.cookie, bookID, url.Values{
		"csrf":      {csrfFrom(t, html)},
		"title":     {"The Left Hand of Darkness"},
		"title_was": {""},
		"tags":      {"Fantasy"},
		"tags_was":  {""},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}

	base := "/ui/libraries/" + f.library + "/search"
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

	_, page := f.get(t, "/ui/libraries/"+f.library+"/search", f.cookie)
	// An empty box must not read as an empty library.
	if strings.Contains(page, "Nothing matched") {
		t.Fatalf("an unasked search reported a failure:\n%s", page)
	}
	if !strings.Contains(page, "Find a book") {
		t.Fatalf("no search box on the search page:\n%s", page)
	}

	_, none := f.get(t, "/ui/libraries/"+f.library+"/search?q=zzzzznothing", f.cookie)
	if !strings.Contains(none, "Nothing matched") {
		t.Fatalf("a failed search said nothing about it:\n%s", none)
	}
}

func TestSearchIsOfferedOnTheLibraryPageAndScopedToTheLibrary(t *testing.T) {
	f := newBooksFixture(t)
	bookWithMetadata(t, f, "offered")

	_, books := f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(books, "/search") {
		t.Fatalf("the library page offered no way to search it:\n%s", books)
	}

	// A reader may search; a library nobody granted them is not there.
	resp, _ := f.get(t, "/ui/libraries/"+f.library+"/search?q=offered", f.readerCookie(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a reader could not search: %d", resp.StatusCode)
	}
	resp, _ = f.get(t, "/ui/libraries/00000000-0000-0000-0000-000000000000/search?q=x", f.cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unreadable library answered %d, want 404", resp.StatusCode)
	}
}

func TestBooksPageAsksAboutBooksThatLookAlike(t *testing.T) {
	f := newBooksFixture(t)
	first := bookWithMetadata(t, f, "buildone")
	second := bookWithMetadata(t, f, "buildtwo")
	for _, id := range []string{first, second} {
		_, html := f.get(t, "/ui/books/"+id, f.cookie)
		resp := saveMetadata(t, f, f.cookie, id, url.Values{
			"csrf":             {csrfFrom(t, html)},
			"title":            {"Dune"},
			"title_was":        {""},
			"contributors":     {"Frank Herbert (author)"},
			"contributors_was": {""},
		})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("save %s: %d", id, resp.StatusCode)
		}
	}

	_, page := f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(page, "Possibly the same book") {
		t.Fatalf("the librarian was not told:\n%s", page)
	}
	// It is a guess, so it must link rather than assert: deciding needs
	// looking at the books.
	if !strings.Contains(page, "books/"+first) || !strings.Contains(page, "books/"+second) {
		t.Fatalf("the group did not link to both books:\n%s", page)
	}

	// A reader is shown neither report, for the same reason they are
	// shown no trash: it is a list of things only a librarian can act on.
	_, readerPage := f.get(t, "/ui/books?library="+f.library, f.readerCookie(t))
	if strings.Contains(readerPage, "Possibly the same book") {
		t.Fatalf("a reader was shown the duplicate report:\n%s", readerPage)
	}
}
