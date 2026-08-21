//go:build linux

package webui_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The series pages (ADR-0018). The store suite owns the layering; these
// tests are about what a browser can see and do: that a shelf reads in
// order, that a claim made in one account never appears in another's,
// that only an admin writes the shared layer, and that the mutations are
// CSRF-checked like every other web UI write.

// postFragment is postForm that keeps the body, which every one of these
// assertions is about: the assign dialog answers with itself.
func (f *booksFixture) postFragment(
	t *testing.T, path string, cookie *http.Cookie, form url.Values,
) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// seriesBook catalogues a book already placed in a series, the way a
// Calibre folder or a subdirectory would.
func seriesBook(t *testing.T, f *booksFixture, name, series string, pos float64) string {
	t.Helper()
	return f.observe(t, store.ObservedBook{
		RelativePath: name + ".epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256:    strings.Repeat(name[:1], 64),
		OriginalFilename: name + ".epub", MediaType: "application/epub+zip",
		Title:  "Title of " + name,
		Series: []store.ObservedSeries{{Name: series, Position: &pos}},
	})
}

// seriesIDFor finds the catalogued series by name, since the store mints
// the id.
func seriesIDFor(t *testing.T, f *booksFixture, name string) string {
	t.Helper()
	rows, err := f.st.ListCatalogEntities(t.Context(), "u1", store.EntitySeries, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == name {
			return row.ID
		}
	}
	t.Fatalf("no series named %q", name)
	return ""
}

// TestSeriesShelfReadsInOrder is the page's reason to exist: the run in
// reading order, with the gap that says a volume is missing.
func TestSeriesShelfReadsInOrder(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "third", "Foundation", 3)
	seriesBook(t, f, "first", "Foundation", 1)
	id := seriesIDFor(t, f, "Foundation")

	resp, html := f.get(t, "/ui/entities/series/"+id, f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shelf: %d", resp.StatusCode)
	}
	one := strings.Index(html, "Title of first")
	three := strings.Index(html, "Title of third")
	if one < 0 || three < 0 {
		t.Fatalf("shelf is missing a volume:\n%s", html)
	}
	if one > three {
		t.Error("the shelf is not in reading order")
	}
	if got := strings.Count(html, "/cover?size=thumbnail"); got != 2 {
		t.Errorf("reading order has %d thumbnail URLs, want one per volume", got)
	}
	if got := strings.Count(html, "Mark read"); got != 2 {
		t.Errorf("reading order has %d manual read controls, want one per volume", got)
	}
	// Book two is nowhere in the library, and the shelf says so rather
	// than renumbering around it.
	if !strings.Contains(html, "Book 2 is not in the library") {
		t.Errorf("no gap notice on a shelf missing book two:\n%s", html)
	}
	// The next-up call to action points at something unread.
	if !strings.Contains(html, "Next up") {
		t.Error("no next-up on a shelf nothing has been read from")
	}
}

// TestSeriesShelfIsPerReader is the tenant-isolation test for the web
// half. The catalog is shared; a claim over it is not.
func TestSeriesShelfIsPerReader(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "stray", "Foundation", 1)
	id := seriesIDFor(t, f, "Foundation")
	bob := f.login(t, "bob")

	// Alice takes the book out of the series, for herself.
	_, form := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	csrf := csrfFrom(t, form)
	resp, _ := f.postFragment(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{"csrf": {csrf}, "name": {""}, "position": {""}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clearing the series: %d", resp.StatusCode)
	}

	_, mine := f.get(t, "/ui/entities/series/"+id, f.cookie)
	if strings.Contains(mine, "Title of stray") {
		t.Error("the book is still on the shelf of the reader who removed it")
	}
	_, theirs := f.get(t, "/ui/entities/series/"+id, bob)
	if !strings.Contains(theirs, "Title of stray") {
		t.Error("one reader's claim emptied another reader's shelf")
	}

	// And the series index: alice's now holds nothing, bob's still does.
	_, mineIndex := f.get(t, "/ui/entities/series", f.cookie)
	if strings.Contains(mineIndex, "Foundation") {
		t.Error("a series nothing claims is still listed")
	}
	_, theirIndex := f.get(t, "/ui/entities/series", bob)
	if !strings.Contains(theirIndex, "Foundation") {
		t.Error("a stranger's claim removed a series from the index")
	}
}

// TestSeriesAssignAddsAndResets walks the dialog: name a series that
// does not exist, see it, then put the book back the way the folder had
// it.
func TestSeriesAssignAddsAndResets(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "one", "Foundation", 1)

	_, form := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	if !strings.Contains(form, "Foundation") {
		t.Fatalf("the dialog does not show what the folder said:\n%s", form)
	}
	if strings.Contains(form, "Back to what the folder says") {
		t.Error("a reset is offered when there is no claim to reset")
	}
	csrf := csrfFrom(t, form)

	resp, after := f.postFragment(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{
			"csrf": {csrf}, "name": {"The Robot Novels"}, "position": {"2"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assigning: %d", resp.StatusCode)
	}
	if !strings.Contains(after, "The Robot Novels") {
		t.Errorf("the claimed series is not in the dialog:\n%s", after)
	}
	if !strings.Contains(after, "Back to what the folder says") {
		t.Error("no reset offered after a claim")
	}
	if !strings.Contains(after, "The folder says: Foundation #1") {
		t.Errorf("the dialog does not say what a reset would restore:\n%s", after)
	}
	// The book page follows, because the claim is what the catalog now
	// resolves to for this reader.
	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(page, "The Robot Novels") {
		t.Error("the book page does not show the claimed series")
	}

	resp, reset := f.postFragment(t, "/ui/books/"+bookID+"/series/reset",
		f.cookie, url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resetting: %d", resp.StatusCode)
	}
	if strings.Contains(reset, "The Robot Novels") {
		t.Error("the claim survived its own reset")
	}
	if !strings.Contains(reset, "This is what the folder says") {
		t.Errorf("after a reset the dialog does not say so:\n%s", reset)
	}
}

// TestSeriesAssignRefusesBadPosition keeps the reader's typing on screen
// rather than replacing the page with an error.
func TestSeriesAssignRefusesBadPosition(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "one", "Foundation", 1)
	_, form := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	csrf := csrfFrom(t, form)

	resp, body := f.postFragment(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{"csrf": {csrf}, "name": {"Foundation"}, "position": {"soon"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a position that is not a number: %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "has to be a number") {
		t.Errorf("no complaint in the re-rendered dialog:\n%s", body)
	}
}

// TestSeriesSharedLayerNeedsAdmin: a reader states what they think, an
// administrator states what everybody sees.
func TestSeriesSharedLayerNeedsAdmin(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "one", "Foundation", 1)
	_, form := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	csrf := csrfFrom(t, form)
	// A non-admin is not even offered the choice.
	if strings.Contains(form, `name="scope" value="shared"`) {
		t.Error("a non-admin was offered the shared layer")
	}
	// And is refused when they ask for it anyway.
	resp, body := f.postFragment(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{
			"csrf": {csrf}, "scope": {"shared"},
			"name": {"Everyone's Series"}, "position": {"1"},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a non-admin writing the shared layer: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Only an administrator") {
		t.Errorf("no refusal in the dialog:\n%s", body)
	}

	// An admin writes it, and every other reader gets it.
	if err := f.st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	admin := f.login(t, "alice")
	_, form = f.get(t, "/ui/books/"+bookID+"/series", admin)
	if !strings.Contains(form, `name="scope" value="shared"`) {
		t.Fatalf("an admin was not offered the shared layer:\n%s", form)
	}
	csrf = csrfFrom(t, form)
	resp, _ = f.postFragment(t, "/ui/books/"+bookID+"/series", admin,
		url.Values{
			"csrf": {csrf}, "scope": {"shared"},
			"name": {"Everyone's Series"}, "position": {"1"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an admin writing the shared layer: %d", resp.StatusCode)
	}
	bob := f.login(t, "bob")
	_, page := f.get(t, "/ui/books/"+bookID, bob)
	if !strings.Contains(page, "Everyone&#39;s Series") &&
		!strings.Contains(page, "Everyone's Series") {
		t.Errorf("the shared claim did not reach another reader:\n%s", page)
	}
}

// TestSeriesMutationsNeedCSRF: every web UI write is CSRF-checked, and
// these are writes.
func TestSeriesMutationsNeedCSRF(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "one", "Foundation", 1)
	for _, path := range []string{
		"/ui/books/" + bookID + "/series",
		"/ui/books/" + bookID + "/series/reset",
	} {
		resp, _ := f.postFragment(t, path, f.cookie,
			url.Values{"csrf": {"wrong"}, "name": {"Nope"}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s without a CSRF token: %d, want 403", path, resp.StatusCode)
		}
	}
}

// TestSeriesSuggestOffersExistingNames keeps a reader joining a series
// rather than founding a near-identical second one.
func TestSeriesSuggestOffersExistingNames(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	seriesBook(t, f, "two", "Discworld", 1)

	_, html := f.get(t,
		"/ui/entities/series/suggest?q=found", f.cookie)
	if !strings.Contains(html, "Foundation") {
		t.Errorf("no suggestion for a matching prefix:\n%s", html)
	}
	if strings.Contains(html, "Discworld") {
		t.Error("a non-matching series was suggested")
	}
	// htmx sends the field's own name rather than q.
	_, html = f.get(t,
		"/ui/entities/series/suggest?name=disc", f.cookie)
	if !strings.Contains(html, "Discworld") {
		t.Errorf("the htmx spelling of the query was ignored:\n%s", html)
	}
	// An empty query suggests nothing rather than everything.
	_, html = f.get(t, "/ui/entities/series/suggest", f.cookie)
	if strings.Contains(html, "Foundation") {
		t.Error("an empty query listed the whole folder")
	}
}

// TestSeriesPagesNeedASession: the shelf and the dialog are catalog
// pages, and every catalog page is behind the session.
func TestSeriesPagesNeedASession(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "one", "Foundation", 1)
	id := seriesIDFor(t, f, "Foundation")
	for _, path := range []string{
		"/ui/entities/series/" + id,
		"/ui/books/" + bookID + "/series",
		"/ui/entities/series/suggest?q=f",
	} {
		resp, _ := f.get(t, path, nil)
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s without a session: %d, want a redirect to the login page",
				path, resp.StatusCode)
		}
	}
}

// The rename form (ADR-0020). The store suite owns the layering; these
// are about the page: that a reader can rename a shelf and put it back,
// that renaming for everybody takes an admin, that a collision is
// explained rather than swallowed, and that the write is CSRF-checked.

func TestSeriesRenameAndRevert(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	id := seriesIDFor(t, f, "Foundation")
	shelf := "/ui/entities/series/" + id

	_, page := f.get(t, shelf, f.cookie)
	csrf := csrfFrom(t, page)
	// A shelf nobody has renamed offers no way to put it back.
	if strings.Contains(page, `name="reset"`) {
		t.Error("an unrenamed shelf offered a revert")
	}

	resp, _ := f.postFragment(t, shelf+"/name", f.cookie,
		url.Values{"csrf": {csrf}, "name": {"The Foundation Saga"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename: %d, want 303", resp.StatusCode)
	}
	_, page = f.get(t, shelf, f.cookie)
	if !strings.Contains(page, "The Foundation Saga") {
		t.Fatalf("the shelf kept the scanned name:\n%s", page)
	}
	// The scanned name is still on the page, because that is what the
	// revert button promises to restore.
	if !strings.Contains(page, "Back to Foundation") {
		t.Fatalf("no revert to the scanned name:\n%s", page)
	}
	// Nobody else sees it: a rename with no scope is personal.
	bob := f.login(t, "bob")
	_, other := f.get(t, shelf, bob)
	if strings.Contains(other, "The Foundation Saga") {
		t.Errorf("a personal rename leaked to another reader:\n%s", other)
	}

	resp, _ = f.postFragment(t, shelf+"/name", f.cookie,
		url.Values{"csrf": {csrf}, "reset": {"1"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revert: %d, want 303", resp.StatusCode)
	}
	_, page = f.get(t, shelf, f.cookie)
	if strings.Contains(page, "The Foundation Saga") {
		t.Errorf("the rename survived its own revert:\n%s", page)
	}
}

func TestSeriesRenameRefusesAnOccupiedName(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	seriesBook(t, f, "two", "Discworld", 1)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")

	_, page := f.get(t, shelf, f.cookie)
	resp, _ := f.postFragment(t, shelf+"/name", f.cookie,
		url.Values{"csrf": {csrfFrom(t, page)}, "name": {"Discworld"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a colliding rename: %d, want 303", resp.StatusCode)
	}
	// The complaint travels on the redirect, so the reader lands back on
	// the shelf being told why nothing changed.
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "problem=") {
		t.Fatalf("a collision was swallowed: %q", loc)
	}
	_, page = f.get(t, shelf+"?problem=x", f.cookie)
	if strings.Contains(page, "Discworld") {
		t.Errorf("the colliding rename was applied anyway:\n%s", page)
	}
}

func TestSeriesRenameForEverybodyNeedsAdmin(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")

	_, page := f.get(t, shelf, f.cookie)
	if strings.Contains(page, `name="scope" value="shared"`) {
		t.Error("a non-admin was offered the shared name")
	}
	resp, _ := f.postFragment(t, shelf+"/name", f.cookie,
		url.Values{
			"csrf": {csrfFrom(t, page)}, "scope": {"shared"},
			"name": {"Everyone's Name"},
		})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "Only+an+administrator") {
		t.Fatalf("a non-admin renamed for everybody: %q", loc)
	}

	if err := f.st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	admin := f.login(t, "alice")
	_, page = f.get(t, shelf, admin)
	if !strings.Contains(page, `name="scope" value="shared"`) {
		t.Fatalf("an admin was not offered the shared name:\n%s", page)
	}
	resp, _ = f.postFragment(t, shelf+"/name", admin,
		url.Values{
			"csrf": {csrfFrom(t, page)}, "scope": {"shared"},
			"name": {"Everyone's Name"},
		})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("an admin renaming for everybody: %d", resp.StatusCode)
	}
	bob := f.login(t, "bob")
	_, other := f.get(t, shelf, bob)
	if !strings.Contains(other, "Everyone&#39;s Name") &&
		!strings.Contains(other, "Everyone's Name") {
		t.Errorf("the shared rename did not reach another reader:\n%s", other)
	}
}

func TestSeriesRenameNeedsCSRF(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")
	resp, _ := f.postFragment(t, shelf+"/name", f.cookie,
		url.Values{"csrf": {"wrong"}, "name": {"Nope"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("renaming without a CSRF token: %d, want 403", resp.StatusCode)
	}
}
