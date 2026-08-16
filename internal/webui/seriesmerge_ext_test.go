package webui_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Merging and splitting a shelf from the web UI (ADR-0021). The store
// suite owns what moves; these are about the page: that only an admin is
// offered the card at all, that a rename refused for a name already
// taken turns into the merge it really was, that a split is offered only
// where two folders share a shelf, and that undoing a merge is one
// button.

// makeAdmin promotes the fixture's first reader, who is the one every
// helper here logs in as.
func makeAdmin(t *testing.T, f *booksFixture) *http.Cookie {
	t.Helper()
	if err := f.st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	return f.login(t, "alice")
}

// secondFolderSeries catalogues a book in another folder under a series
// name the first folder already uses, which is the fold ADR-0019 does
// automatically and the one a split undoes.
func secondFolderSeries(t *testing.T, f *booksFixture, name, series string) {
	t.Helper()
	folder := store.Folder{
		ID: "folder-two", Name: "Shared Shelf", RootPath: f.root + "-two",
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}
	if _, err := f.st.FolderByID(t.Context(), folder.ID); err != nil {
		if err := f.st.CreateFolder(t.Context(), folder); err != nil {
			t.Fatal(err)
		}
	}
	pos := 1.0
	if _, err := f.st.ReconcileFolder(t.Context(), folder.ID,
		[]store.ObservedBook{{
			RelativePath: name + ".epub", SizeBytes: 4096, MTime: time.Now().UTC(),
			ContentSHA256:    strings.Repeat(name[:1], 64),
			OriginalFilename: name + ".epub", MediaType: "application/epub+zip",
			Title:  "Title of " + name,
			Series: []store.ObservedSeries{{Name: series, Position: &pos}},
		}}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesReshapeIsAdminOnly(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")

	_, page := f.get(t, shelf, f.cookie)
	if strings.Contains(page, "Merge or split this shelf") {
		t.Errorf("a plain reader was offered the curator's card:\n%s", page)
	}
	resp, _ := f.postFragment(t, shelf+"/merge", f.cookie,
		url.Values{"csrf": {csrfFrom(t, page)}, "into_name": {"Anything"}})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "Only+an+administrator") {
		t.Fatalf("a plain reader merged a shelf: %q", loc)
	}

	admin := makeAdmin(t, f)
	if _, page = f.get(t, shelf, admin); !strings.Contains(page, "Merge or split this shelf") {
		t.Fatalf("an admin was not offered the card:\n%s", page)
	}
}

func TestSeriesMergeAndUndoFromTheShelf(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	seriesBook(t, f, "two", "Discworld", 1)
	admin := makeAdmin(t, f)
	absorbed := seriesIDFor(t, f, "Foundation")
	survivor := seriesIDFor(t, f, "Discworld")

	_, page := f.get(t, "/ui/entities/series/"+absorbed, admin)
	resp, _ := f.postFragment(t, "/ui/entities/series/"+absorbed+"/merge", admin,
		url.Values{"csrf": {csrfFrom(t, page)}, "into_name": {"Discworld"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("merging: %d, want 303", resp.StatusCode)
	}
	// The absorbed shelf has no page any more, so the reader is sent to
	// the one their books are on.
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, survivor) {
		t.Fatalf("a merge left the reader on the absorbed shelf: %q", loc)
	}
	if got, _ := f.get(t, "/ui/entities/series/"+absorbed, admin); got.StatusCode != http.StatusNotFound {
		t.Errorf("the absorbed shelf still has a page: %d", got.StatusCode)
	}

	_, page = f.get(t, "/ui/entities/series/"+survivor, admin)
	if !strings.Contains(page, "Title of one") || !strings.Contains(page, "Title of two") {
		t.Fatalf("the survivor is missing a volume:\n%s", page)
	}
	// The absorbed name is listed as leading here, which is what makes
	// the merge outlive a rescan and what offers the undo.
	if !strings.Contains(page, "Foundation — everywhere") {
		t.Fatalf("the absorbed name is not listed:\n%s", page)
	}

	binding := valueOfInput(t, page, "binding")
	resp, _ = f.postFragment(t, "/ui/entities/series/"+survivor+"/unbind", admin,
		url.Values{"csrf": {csrfFrom(t, page)}, "binding": {binding}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("undoing a merge: %d, want 303", resp.StatusCode)
	}
	_, page = f.get(t, "/ui/entities/series/"+survivor, admin)
	if strings.Contains(page, "Foundation — everywhere") {
		t.Errorf("the binding survived its own undo:\n%s", page)
	}
	// Undoing moves no book. The books stay until a scan of the folder
	// observes the freed name again.
	if !strings.Contains(page, "Title of one") {
		t.Errorf("undoing a binding moved a book by itself:\n%s", page)
	}
}

func TestSeriesRenameConflictOffersTheMerge(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	seriesBook(t, f, "two", "Discworld", 1)
	admin := makeAdmin(t, f)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")

	_, page := f.get(t, shelf, admin)
	resp, _ := f.postFragment(t, shelf+"/name", admin,
		url.Values{"csrf": {csrfFrom(t, page)}, "name": {"Discworld"}})
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "merge=") {
		t.Fatalf("a refused rename offered no merge: %q", loc)
	}
	// Following the redirect is what a browser does, and the offer has
	// to survive the trip.
	_, page = f.get(t, followShelf(t, loc), admin)
	if !strings.Contains(page, "Merge into Discworld") {
		t.Fatalf("the merge offer did not reach the page:\n%s", page)
	}
}

func TestSeriesSplitNeedsTwoFolders(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Essays", 1)
	admin := makeAdmin(t, f)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Essays")

	// One folder's shelf is a rename, so the split is not even offered.
	_, page := f.get(t, shelf, admin)
	if strings.Contains(page, "Give one folder a shelf of its own") {
		t.Errorf("a single-folder shelf was offered a split:\n%s", page)
	}

	secondFolderSeries(t, f, "two", "Essays")
	_, page = f.get(t, shelf, admin)
	if !strings.Contains(page, "Give one folder a shelf of its own") {
		t.Fatalf("a shelf spanning two folders was offered no split:\n%s", page)
	}
	if !strings.Contains(page, "Shared Shelf (1 book)") {
		t.Fatalf("the split does not say what would move:\n%s", page)
	}

	resp, _ := f.postFragment(t, shelf+"/split", admin, url.Values{
		"csrf": {csrfFrom(t, page)}, "folder_id": {"folder-two"},
		"name": {"Essays (Shared Shelf)"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("splitting: %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "problem=") {
		t.Fatalf("splitting was refused: %q", loc)
	}
	_, page = f.get(t, followShelf(t, loc), admin)
	if !strings.Contains(page, "Title of two") {
		t.Fatalf("the new shelf is empty:\n%s", page)
	}
	// The old shelf keeps the first folder's book and loses the other's.
	_, page = f.get(t, shelf, admin)
	if strings.Contains(page, "Title of two") || !strings.Contains(page, "Title of one") {
		t.Fatalf("the split moved the wrong books:\n%s", page)
	}
}

func TestSeriesReshapeNeedsCSRF(t *testing.T) {
	f := newBooksFixture(t)
	seriesBook(t, f, "one", "Foundation", 1)
	admin := makeAdmin(t, f)
	shelf := "/ui/entities/series/" + seriesIDFor(t, f, "Foundation")
	for _, path := range []string{"/merge", "/split", "/unbind"} {
		resp, _ := f.postFragment(t, shelf+path, admin,
			url.Values{"csrf": {"wrong"}, "into_name": {"Whatever"}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s without a CSRF token: %d, want 403", path, resp.StatusCode)
		}
	}
}

// followShelf resolves the relative Location a shelf redirect sends,
// which is what the browser would do with it.
func followShelf(t *testing.T, loc string) string {
	t.Helper()
	if !strings.HasPrefix(loc, "../") {
		t.Fatalf("a shelf redirect went somewhere unexpected: %q", loc)
	}
	return "/ui/entities/series/" + strings.TrimPrefix(loc, "../")
}

// valueOfInput pulls one hidden field's value out of the rendered page,
// which is how a test gets an id the store minted.
func valueOfInput(t *testing.T, page, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("no %q field in:\n%s", name, page)
	}
	rest := page[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated %q field", name)
	}
	return rest[:j]
}
