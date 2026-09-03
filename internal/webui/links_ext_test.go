//go:build linux

package webui_test

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Every link in this UI is assembled by hand from a prefix and a path,
// and until this test nothing checked that the result was a route. It
// stopped being theoretical when entities became library-wide
// (ADR-0019): the pages moved from /ui/folders/{folder}/{kind}/{entity}
// to /ui/entities/{kind}/{entity}, the templates followed and the
// builders behind the book page's chips did not, so every author and
// series chip led to a 404 that nothing failed on.
//
// So this walks the pages a reader passes through, resolves every link,
// form target and "back where you came from" value the way a browser
// would, and asks whether anything answers it. The question goes to the
// router rather than to a POST, because a form target must not be
// submitted to find out that it exists; where the router does say a GET
// leads somewhere, the page is fetched too, since a route that answers
// 404 for this reader is as dead an end as no route at all.
//
// dashboard is the one pattern that proves nothing: /ui/ matches every
// unclaimed path under it and the handler 404s all but its own.
const dashboardPattern = "GET /ui/"

var (
	uiLinkAttr = regexp.MustCompile(
		`(?:href|action|hx-get|hx-post)="([^"]*)"`)
	uiBackValue = regexp.MustCompile(
		`name="back" value="([^"]*)"`)
)

// pageLinks is every internal /ui target a page points at, absolute and
// deduplicated. Relative targets resolve against the page's own URL, as
// the browser resolves them; a "back" value resolves against the UI root,
// which is where the forms carrying one post and where their redirects
// are resolved from.
func pageLinks(t *testing.T, pagePath, body string) []string {
	t.Helper()
	base, err := url.Parse(pagePath)
	if err != nil {
		t.Fatalf("page path %q: %v", pagePath, err)
	}
	root, _ := url.Parse("/ui/")

	seen := map[string]bool{}
	add := func(against *url.URL, raw string) {
		raw = strings.TrimSpace(html.UnescapeString(raw))
		if raw == "" || strings.HasPrefix(raw, "#") {
			return
		}
		ref, err := url.Parse(raw)
		if err != nil || ref.IsAbs() || ref.Host != "" {
			return
		}
		target := against.ResolveReference(ref)
		if !strings.HasPrefix(target.Path, "/ui/") ||
			strings.HasPrefix(target.Path, "/ui/static/") {
			return
		}
		target.Fragment = ""
		seen[target.String()] = true
	}
	for _, m := range uiLinkAttr.FindAllStringSubmatch(body, -1) {
		add(base, m[1])
	}
	for _, m := range uiBackValue.FindAllStringSubmatch(body, -1) {
		add(root, m[1])
	}

	out := make([]string, 0, len(seen))
	for link := range seen {
		out = append(out, link)
	}
	sort.Strings(out)
	return out
}

func TestEveryLinkAPageOffersLeadsSomewhere(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	bookID := observeSeriesBook(t, f, "dune.epub", "Dune", "Chronicles", 1, now)
	seriesID := seriesIDFor(t, f, "Chronicles")
	contributorID := entityIDFor(t, f, store.EntityContributor, "Series Author")

	// The same routing table the server runs, asked rather than driven.
	routes := http.NewServeMux()
	f.ui.Mount(routes, func(h http.Handler) http.Handler { return h })

	pages := []string{
		"/ui/",
		"/ui/library",
		"/ui/library?filter=reading",
		"/ui/books/" + bookID,
		"/ui/entities/series",
		"/ui/entities/contributors",
		"/ui/entities/tags",
		"/ui/entities/series/" + seriesID,
		"/ui/entities/contributors/" + contributorID,
		"/ui/folders/" + f.folder + "/search?q=dune",
		"/ui/settings",
	}
	for _, page := range pages {
		resp, body := f.get(t, page, f.cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", page, resp.StatusCode)
		}
		for _, link := range pageLinks(t, page, body) {
			get, post := patternsFor(t, routes, link)
			switch {
			case get != "" && get != dashboardPattern || link == "/ui/":
				if resp, _ := f.get(t, link, f.cookie); resp.StatusCode == http.StatusNotFound {
					t.Errorf("%s offers %s, which answers 404", page, link)
				}
			case post != "":
				// A form target: routed, and not ours to submit.
			default:
				t.Errorf("%s offers %s, which no route answers", page, link)
			}
		}
	}
}

// patternsFor asks the router which patterns claim a target, for a GET
// and for a POST. An empty string means nothing does.
func patternsFor(
	t *testing.T, routes *http.ServeMux, target string,
) (getPattern, postPattern string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("link %q: %v", target, err)
	}
	ask := func(method string) string {
		_, pattern := routes.Handler(&http.Request{
			Method: method, URL: u, Host: "liseur.test",
		})
		return pattern
	}
	return ask(http.MethodGet), ask(http.MethodPost)
}

// entityIDFor is seriesIDFor for the other kinds, since the store mints
// every entity id.
func entityIDFor(
	t *testing.T, f *booksFixture, kind store.EntityKind, name string,
) string {
	t.Helper()
	rows, err := f.st.ListCatalogEntities(t.Context(), "u1", kind, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == name {
			return row.ID
		}
	}
	t.Fatalf("no %s named %q", kind, name)
	return ""
}

// TestPaginationLinksLeadToTheNextPage covers what the walk above cannot
// reach with a handful of books: the "Next page" links, which only
// appear once a page is full. They were the other half of the same bug
// — a next-page link built for a route that had moved, or rendered
// without the prefix its page needed — so they are exercised with
// enough books to make them appear.
func TestPaginationLinksLeadToTheNextPage(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	// One more than the shelf holds (seriesShelfSize), so it pages.
	const volumes = 201
	observed := make([]store.ObservedBook, 0, volumes)
	for i := range volumes {
		position := float64(i + 1)
		name := fmt.Sprintf("vol-%03d", i)
		observed = append(observed, store.ObservedBook{
			RelativePath: name + ".epub", SizeBytes: 4096, MTime: now,
			ContentSHA256: name, OriginalFilename: name + ".epub",
			MediaType: "application/epub+zip",
			Title:     "Volume " + name,
			Series: []store.ObservedSeries{
				{Name: "Long Run", Position: &position},
			},
			// One author on every volume, so that shelf pages, plus one
			// of their own, so the contributors listing pages too.
			Contributors: []store.ObservedContributor{
				{Name: "Prolific Author", Role: store.ContributorRoleAuthor, Position: 1},
				{Name: "Editor " + name, Role: "edt", Position: 2},
			},
		})
	}
	if _, err := f.st.ReconcileFolder(
		t.Context(), f.folder, observed, false, now,
	); err != nil {
		t.Fatal(err)
	}

	routes := http.NewServeMux()
	f.ui.Mount(routes, func(h http.Handler) http.Handler { return h })

	for _, page := range []string{
		"/ui/entities/series/" + seriesIDFor(t, f, "Long Run"),
		"/ui/entities/contributors",
		"/ui/entities/contributors/" +
			entityIDFor(t, f, store.EntityContributor, "Prolific Author"),
	} {
		resp, body := f.get(t, page, f.cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", page, resp.StatusCode)
		}
		next := nextPageLink(t, page, body)
		if get, _ := patternsFor(t, routes, next); get == "" || get == dashboardPattern {
			t.Errorf("%s pages to %s, which no route answers", page, next)
			continue
		}
		if resp, _ := f.get(t, next, f.cookie); resp.StatusCode != http.StatusOK {
			t.Errorf("%s pages to %s: got %d, want 200",
				page, next, resp.StatusCode)
		}
	}
}

var nextPageHref = regexp.MustCompile(`href="([^"]*)">Next page<`)

// nextPageLink is the page's own "Next page" target, resolved the way
// the browser on that page would resolve it.
func nextPageLink(t *testing.T, pagePath, body string) string {
	t.Helper()
	m := nextPageHref.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s: no next-page link, so nothing paged", pagePath)
	}
	base, err := url.Parse(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	return base.ResolveReference(ref).String()
}
