//go:build linux

package webui_test

import (
	"bytes"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func observeSeriesBook(
	t *testing.T, f *booksFixture, path, title, series string,
	position float64, at time.Time,
) string {
	t.Helper()
	if _, err := f.st.ReconcileFolder(t.Context(), f.folder, []store.ObservedBook{{
		RelativePath: path, SizeBytes: 4096, MTime: at,
		ContentSHA256: "series-" + path, OriginalFilename: path,
		MediaType: "application/epub+zip", Title: title,
		Series: []store.ObservedSeries{{Name: series, Position: &position}},
		Contributors: []store.ObservedContributor{{
			Name: "Series Author", Role: store.ContributorRoleAuthor, Position: 1,
		}},
	}}, false, at); err != nil {
		t.Fatal(err)
	}
	return f.bookAt(t, path)
}

func observeStandaloneBook(
	t *testing.T, f *booksFixture, path, title string, at time.Time,
) string {
	t.Helper()
	if _, err := f.st.ReconcileFolder(t.Context(), f.folder, []store.ObservedBook{{
		RelativePath: path, SizeBytes: 4096, MTime: at,
		ContentSHA256: "standalone-" + path, OriginalFilename: path,
		MediaType: "application/epub+zip", Title: title,
	}}, false, at); err != nil {
		t.Fatal(err)
	}
	return f.bookAt(t, path)
}

func setSeriesGrouping(t *testing.T, f *booksFixture, grouped bool) *http.Cookie {
	t.Helper()
	form := url.Values{
		"csrf": {csrfOnLibrary(t, f)}, "set": {"series"},
		"back": {"library"},
	}
	if grouped {
		form.Set("group_series", "1")
	}
	req, _ := http.NewRequest("POST", f.ts.URL+"/ui/preferences",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(f.cookie)
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("preference POST: got %d", resp.StatusCode)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "liseur_ui" {
			return cookie
		}
	}
	t.Fatal("preference POST set no UI cookie")
	return nil
}

func libraryWithPreference(
	t *testing.T, f *booksFixture, path string, preference *http.Cookie,
) string {
	t.Helper()
	req, _ := http.NewRequest("GET", f.ts.URL+path, nil)
	req.AddCookie(f.cookie)
	req.AddCookie(preference)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// progressOn puts a work at a given progression, the way a device does:
// through the op log, so the page reads exactly what a real sync would
// have left behind.
func progressOn(t *testing.T, f *booksFixture, workID, opID string, at float64, when time.Time) {
	t.Helper()
	if _, err := f.st.AppendOps(t.Context(), "u1", "d-test", []store.Op{{
		OpID: opID, WorkID: workID, ClientTS: when, Progression: at,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
}

// libraryFixture is the shelf every test below looks at: one book read
// halfway, one book read to the end, one book never opened, and one work
// this server holds no file for.
func libraryFixture(t *testing.T) (*booksFixture, map[string]string) {
	t.Helper()
	f := newBooksFixture(t)
	ids := map[string]string{}

	for _, name := range []string{"midway", "done", "fresh"} {
		ids[name] = f.addBook(t, name, bytes.Repeat([]byte(name+"-epub"), 50))
	}

	now := time.Now().UTC()
	for _, m := range []struct {
		book, work string
		at         float64
	}{
		{ids["midway"], "w-midway", 0.5},
		{ids["done"], "w-done", 1},
	} {
		if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", m.book,
			store.Work{ID: m.work, UserID: "u1", Title: m.work, CreatedAt: now},
			nil, nil, true, now); err != nil {
			t.Fatal(err)
		}
		progressOn(t, f, m.work, "018e6f1a-0000-7000-8000-0000000000"+m.work[2:4], m.at, now)
	}
	// Progress from a device holding a file this server never saw.
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "w-elsewhere", UserID: "u1", Title: "Elsewhere", CreatedAt: now},
		nil, []store.Identifier{{Kind: "sha256", Value: "beefbeef"}}); err != nil {
		t.Fatal(err)
	}
	progressOn(t, f, "w-elsewhere", "018e6f1a-0000-7000-8000-000000000099", 0.2, now)
	return f, ids
}

// grid is the shelf without the continue-reading hero above it. The
// hero deliberately ignores the filter — it is a shortcut back to what
// you were reading, not a view of the list — so an assertion about what
// a chip shows has to look below it.
func grid(page string) string {
	i := strings.Index(page, `class="toolbar filters"`)
	if i < 0 {
		return page
	}
	return page[i:]
}

// TestTheLandingViewIsWhatIsOnThisServer pins which chip an address
// with no filter means. Orphan works — a reader's position for a book
// this server holds no file for — are worth keeping and poor to open
// on, being coverless text tiles among covers. So they moved one chip
// away rather than being hidden.
func TestTheLandingViewIsWhatIsOnThisServer(t *testing.T) {
	for name, tc := range map[string]struct {
		query string
		want  bool
	}{
		"no filter at all":          {"", false},
		"asking for all":            {"&filter=all", true},
		"asking for here":           {"&filter=here", false},
		"a chip that is not a chip": {"&filter=nonsense", true},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := libraryFixture(t)
			_, page := f.get(t, "/ui/library?folder="+f.folder+tc.query, f.cookie)
			if got := strings.Contains(page, "works/w-elsewhere"); got != tc.want {
				t.Errorf("orphan work shown = %v, want %v:\n%s", got, tc.want, page)
			}
		})
	}
}

// TestLibraryPageIsTheUnionOfBothOldPages is the whole reason this page
// exists. Before it there were two — a catalog and a reading history —
// and a book that was in both appeared twice while a book that was in
// neither list's source appeared nowhere. Every row below is a case that
// only one of the two old pages could produce.
func TestLibraryPageIsTheUnionOfBothOldPages(t *testing.T) {
	f, ids := libraryFixture(t)

	// Orphan works live under "all"; the landing view is what is here.
	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter=all", f.cookie)
	for what, want := range map[string]string{
		"a book that has been read":       `books/` + ids["midway"],
		"a book that has never been read": `books/` + ids["fresh"],
		"a work with no file here":        `works/w-elsewhere`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("%s is missing from the library (%s):\n%s", what, want, page)
		}
	}
	// Once each: a book with a work is one row, not one per source.
	if n := strings.Count(grid(page), `<article class="bookcard">`); n != 4 {
		t.Errorf("the shelf has %d cards; three books and one orphan work make four", n)
	}
	// The work behind a book is reachable without being a second card.
	if !strings.Contains(page, `works/w-midway`) {
		t.Error("a read book does not link to its own statistics")
	}
}

// TestLibraryChipsMeanWhatTheySay checks each filter against the same
// shelf. A chip that quietly includes the wrong rows is worse than no
// chip: it is a wrong answer with a confident label on it.
func TestLibraryChipsMeanWhatTheySay(t *testing.T) {
	f, ids := libraryFixture(t)

	for _, tc := range []struct {
		filter string
		want   []string
		absent []string
	}{
		{"reading", []string{ids["midway"], "w-elsewhere"}, []string{ids["fresh"], ids["done"]}},
		{"finished", []string{ids["done"]}, []string{ids["midway"], ids["fresh"]}},
		{"unread", []string{ids["fresh"]}, []string{ids["midway"], ids["done"]}},
		{"here", []string{ids["midway"], ids["fresh"], ids["done"]}, []string{"w-elsewhere"}},
	} {
		_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter="+tc.filter, f.cookie)
		body := grid(page)
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("filter=%s is missing %s:\n%s", tc.filter, want, body)
			}
		}
		for _, absent := range tc.absent {
			if strings.Contains(body, absent) {
				t.Errorf("filter=%s shows %s, which it should not", tc.filter, absent)
			}
		}
		// The filter has to survive in the URL, or the chips and the
		// "load more" sentinel would both fall back to the default.
		if !strings.Contains(page, `filter=`+tc.filter) {
			t.Errorf("filter=%s is not kept in the page's own links", tc.filter)
		}
	}
}

// TestLibrarySortCanBeReversed pins two things at once: the default
// "Recently added" sort is newest first, matching its label, and
// dir=asc reverses it. It would have caught the bug where this page
// paged the oldest-first store method while its chip claimed to show
// what was recent.
func TestLibrarySortCanBeReversed(t *testing.T) {
	f := newBooksFixture(t)
	first := f.addBook(t, "first", []byte(strings.Repeat("first-epub", 40)))
	second := f.addBook(t, "second", []byte(strings.Repeat("second-epub", 40)))

	_, page := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	body := grid(page)
	if i, j := strings.Index(body, first), strings.Index(body, second); i < 0 || j < 0 || i < j {
		t.Errorf("default sort is not newest-first: %q at %d, %q at %d\n%s", first, i, second, j, body)
	}

	_, asc := f.get(t, "/ui/library?folder="+f.folder+"&dir=asc", f.cookie)
	body = grid(asc)
	if i, j := strings.Index(body, first), strings.Index(body, second); i < 0 || j < 0 || i > j {
		t.Errorf("dir=asc is not oldest-first: %q at %d, %q at %d\n%s", first, i, second, j, body)
	}
	// dir survives in the page's own links, the same way filter does.
	if !strings.Contains(asc, `dir=asc`) {
		t.Errorf("dir=asc is not kept in the page's own links:\n%s", asc)
	}
}

// TestLibraryHeroResumesTheLastBook pins the shortcut at the top of the
// page: the newest thing started and not finished, and nothing at all
// when there is nothing to go back to.
func TestLibraryHeroResumesTheLastBook(t *testing.T) {
	f, ids := libraryFixture(t)

	_, page := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	hero := page[strings.Index(page, `class="card resume"`):]
	hero = hero[:strings.Index(hero, "</section>")]
	if !strings.Contains(hero, `books/`+ids["midway"]+`/read`) {
		t.Errorf("the hero does not resume the half-read book:\n%s", hero)
	}
	if strings.Contains(hero, ids["done"]) || strings.Contains(hero, ids["fresh"]) {
		t.Errorf("the hero picked a book that is not in progress:\n%s", hero)
	}

	// A shelf with nothing started has no hero — it is a shortcut, not
	// a section, so an empty one would be furniture.
	bare := newBooksFixture(t)
	_, page = bare.get(t, "/ui/library", bare.cookie)
	if strings.Contains(page, `class="card resume"`) {
		t.Errorf("a shelf with nothing in progress still drew a hero:\n%s", page)
	}
}

// TestLibraryCardOpensTheBookRatherThanAMenu records the decision the ⋮
// menu lost: the action affordance on a cover is a link to the page that
// holds the actions, because a popup anchored inside an overflow-hidden
// cover is clipped by the picture it hangs off, and hover and long-press
// are both unavailable to a web page on a phone.
func TestLibraryCardOpensTheBookRatherThanAMenu(t *testing.T) {
	f, ids := libraryFixture(t)

	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter=all", f.cookie)
	if !strings.Contains(page, `class="cardopen" href="books/`+ids["fresh"]+`"`) {
		t.Errorf("a card has no way to its book's page:\n%s", page)
	}
	if strings.Contains(page, "cardmenu") || strings.Contains(page, "<details class=\"cardmenu\"") {
		t.Error("the clipped popup menu came back")
	}
	// A work with no book here opens its own page instead.
	if !strings.Contains(page, `class="cardopen" href="works/w-elsewhere"`) {
		t.Errorf("a work with no file here has no way to its statistics:\n%s", page)
	}
}

// TestLibraryFragmentIsCardsOnly keeps the htmx continuation from
// appending a second copy of the whole document into the grid.
func TestLibraryFragmentIsCardsOnly(t *testing.T) {
	f, _ := libraryFixture(t)

	req, _ := http.NewRequest("GET", f.ts.URL+"/ui/library?folder="+f.folder, nil)
	req.AddCookie(f.cookie)
	req.Header.Set("HX-Request", "true")
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	frag := string(body)
	for _, shell := range []string{"<html", `class="rail"`, `class="topbar"`, `class="card resume"`} {
		if strings.Contains(frag, shell) {
			t.Errorf("the htmx fragment contains %q, so it would append the page to itself", shell)
		}
	}
	if !strings.Contains(frag, `class="bookcard"`) {
		t.Errorf("the htmx fragment has no cards:\n%s", frag)
	}
}

// TestLibraryShowsTheSharedShelfButNotAnotherAccountsReading draws the
// line ADR-0017 moved. The catalog is the server's, so bob sees the same
// books alice does — but what alice has read is hers, and none of her
// works reach his page.
func TestLibraryShowsTheSharedShelfButNotAnotherAccountsReading(t *testing.T) {
	f, ids := libraryFixture(t)

	bob := f.login(t, "bob")
	_, page := f.get(t, "/ui/library?folder="+f.folder, bob)
	for _, shared := range []string{ids["midway"], ids["fresh"]} {
		if !strings.Contains(page, shared) {
			t.Errorf("the shared catalog hid %s from a second account:\n%s", shared, page)
		}
	}
	for _, hers := range []string{"w-elsewhere", "w-midway", "w-done"} {
		if strings.Contains(page, hers) {
			t.Errorf("another account's reading leaked %s:\n%s", hers, page)
		}
	}
}

// TestLibraryCardsNameTheAuthor is the card's half of the batched
// credit read. The store answers with names; what this pins is that the
// page asks for them, prints the author rather than whoever else worked
// on the book, and does not let a crowded title page push the title off
// the card.
func TestLibraryCardsNameTheAuthor(t *testing.T) {
	f := newBooksFixture(t)
	// Contributors come off the file the pass read, so they are seeded
	// the same way a pass would report them.
	now := time.Now().UTC()
	if _, err := f.st.ReconcileFolder(t.Context(), f.folder, []store.ObservedBook{{
		RelativePath: "credited.epub", SizeBytes: 400, MTime: now,
		ContentSHA256:    strings.Repeat("c", 64),
		OriginalFilename: "credited.epub", MediaType: "application/epub+zip",
		Title: "Solaris",
		Contributors: []store.ObservedContributor{
			{Name: "Stanisław Lem", Role: store.ContributorRoleAuthor, Position: 1},
			{Name: "Joanna Kilmartin", Role: "translator", Position: 2},
		},
	}}, true, now); err != nil {
		t.Fatal(err)
	}

	_, page := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if !strings.Contains(page, "Stanisław Lem") {
		t.Error("the card does not say who wrote the book")
	}
	if strings.Contains(page, "Joanna Kilmartin") {
		t.Error("a translator was printed where the author goes")
	}
}

func TestLibraryGroupsSeriesLikeAndroidByDefault(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	one := observeSeriesBook(t, f, "one.epub", "Volume One", "The Run", 1, now)
	two := observeSeriesBook(t, f, "two.epub", "Volume Two", "The Run", 2, now.Add(time.Second))
	single := observeSeriesBook(t, f, "single.epub", "Only Volume", "A Solo", 1, now.Add(2*time.Second))
	standalone := f.observe(t, store.ObservedBook{
		RelativePath: "standalone.epub", SizeBytes: 4096, MTime: now,
		ContentSHA256: "series-standalone", OriginalFilename: "standalone.epub",
		MediaType: "application/epub+zip", Title: "Standalone",
	})
	// The entity is library-wide, but the pile is made only from the
	// selected folder. A third volume elsewhere belongs on the detail
	// shelf, not in this folder's count.
	other := store.Folder{
		ID: "folder-web-other", Name: "Other Books", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: now,
	}
	if err := f.st.CreateFolder(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileFolder(t.Context(), other.ID, []store.ObservedBook{{
		RelativePath: "three.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "series-third-folder", MediaType: "application/epub+zip",
		Title:  "Volume Three",
		Series: []store.ObservedSeries{{Name: "The Run", Position: float64Ptr(3)}},
	}}, true, now); err != nil {
		t.Fatal(err)
	}

	_, page := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	body := grid(page)
	if strings.Count(body, `class="bookcard seriescard"`) != 1 {
		t.Fatalf("default shelf did not contain one series pile:\n%s", body)
	}
	if !strings.Contains(body, "The Run") || !strings.Contains(body, "2 books") ||
		!strings.Contains(body, "Series Author") {
		t.Fatalf("pile is missing its Android summary:\n%s", body)
	}
	if strings.Contains(body, "3 books") {
		t.Fatal("a volume from another folder changed the selected folder's pile")
	}
	for _, volume := range []string{one, two} {
		if strings.Contains(body, `href="books/`+volume+`/read"`) {
			t.Errorf("grouped volume %s remained an individual card", volume)
		}
	}
	for _, individual := range []string{single, standalone} {
		if !strings.Contains(body, `href="books/`+individual+`/read"`) {
			t.Errorf("standalone %s disappeared into a pile", individual)
		}
	}
	if !strings.Contains(page, `id="group-series-toggle"`) ||
		!strings.Contains(page, `group-series-toggle" type="checkbox" name="group_series" value="1" checked`) {
		t.Fatal("grouping checkbox is not checked by default")
	}

	ungrouped := libraryWithPreference(t, f,
		"/ui/library?folder="+f.folder, setSeriesGrouping(t, f, false))
	body = grid(ungrouped)
	if strings.Contains(body, `class="bookcard seriescard"`) {
		t.Fatalf("unchecked shelf still contains a pile:\n%s", body)
	}
	for _, volume := range []string{one, two, single, standalone} {
		if !strings.Contains(body, `href="books/`+volume+`/read"`) {
			t.Errorf("ungrouped volume %s has no individual card", volume)
		}
	}
}

func TestLibrarySeriesGroupingResolvesForEachReader(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	one := observeSeriesBook(t, f, "scope-one.epub", "One", "Folder Shelf", 1, now)
	two := observeSeriesBook(t, f, "scope-two.epub", "Two", "Folder Shelf", 2, now)
	for _, bookID := range []string{one, two} {
		if _, err := f.st.SetBookSeriesOverride(t.Context(), "u1", bookID,
			store.SeriesSourcePersonal,
			[]store.SeriesClaimItem{{Name: "Alice Shelf"}},
			store.SeriesClaimMutation{At: now.Add(time.Second)},
		); err != nil {
			t.Fatal(err)
		}
	}

	_, alice := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if !strings.Contains(grid(alice), "Alice Shelf") ||
		strings.Contains(grid(alice), "Folder Shelf") {
		t.Fatalf("Alice's personal shelf was not resolved:\n%s", grid(alice))
	}
	bob := f.login(t, "bob")
	_, bobPage := f.get(t, "/ui/library?folder="+f.folder, bob)
	if !strings.Contains(grid(bobPage), "Folder Shelf") ||
		strings.Contains(grid(bobPage), "Alice Shelf") {
		t.Fatalf("Alice's personal shelf leaked to Bob:\n%s", grid(bobPage))
	}
}

func float64Ptr(value float64) *float64 { return &value }

func TestLibrarySeriesPileUsesNextUpAndSurvivesFilters(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	one := observeSeriesBook(t, f, "first.epub", "First", "Trilogy", 1, now)
	two := observeSeriesBook(t, f, "second.epub", "Second", "Trilogy", 2, now.Add(time.Second))
	_ = observeSeriesBook(t, f, "third.epub", "Third", "Trilogy", 3, now.Add(2*time.Second))

	for _, state := range []struct {
		book, work, op string
		progress       float64
	}{
		{one, "w-series-one", "018e6f1a-0000-7000-8000-000000000071", 1},
		{two, "w-series-two", "018e6f1a-0000-7000-8000-000000000072", .4},
	} {
		if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", state.book,
			store.Work{ID: state.work, UserID: "u1", Title: state.work, CreatedAt: now},
			nil, nil, true, now); err != nil {
			t.Fatal(err)
		}
		progressOn(t, f, state.work, state.op, state.progress, now)
	}

	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter=reading", f.cookie)
	body := grid(page)
	if !strings.Contains(body, `class="bookcard seriescard"`) ||
		!strings.Contains(body, "3 books") || !strings.Contains(body, "1 read") ||
		!strings.Contains(body, "1 in progress") {
		t.Fatalf("reading filter did not retain the complete pile:\n%s", body)
	}
	front := body[strings.Index(body, `class="stackfront"`):]
	front = front[:min(len(front), 500)]
	if !strings.Contains(front, `books/`+two+`/cover`) {
		t.Fatalf("in-progress volume is not the front cover:\n%s", front)
	}
	if !strings.Contains(body, `class="done"`) || !strings.Contains(body, `class="started"`) {
		t.Fatal("pile progress rail does not distinguish read and started volumes")
	}

	_, unread := f.get(t, "/ui/library?folder="+f.folder+"&filter=unread", f.cookie)
	if !strings.Contains(grid(unread), `class="bookcard seriescard"`) {
		t.Fatal("an unread volume did not keep its whole series pile visible")
	}

	listPref := &http.Cookie{Name: "liseur_ui", Value: "dark.list.series-grouped"}
	list := libraryWithPreference(t, f, "/ui/library?folder="+f.folder, listPref)
	if strings.Contains(list, `class="bookcard seriescard"`) ||
		strings.Count(list, `class="cover-thumb"`) != 3 {
		t.Fatalf("list view did not remain one row per book:\n%s", list)
	}
}

func TestLibrarySeriesPileDoesNotRepeatAcrossCursorPages(t *testing.T) {
	f := newBooksFixture(t)
	base := time.Date(2026, time.August, 21, 8, 0, 0, 0, time.UTC)
	_ = observeSeriesBook(t, f, "old.epub", "Old", "Long Run", 1, base)
	for i := 1; i <= 24; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		observeStandaloneBook(t, f, "filler-"+i2(i)+".epub", "Filler "+i2(i), at)
	}
	_ = observeSeriesBook(t, f, "new.epub", "New", "Long Run", 2, base.Add(25*time.Minute))

	_, descending := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if strings.Count(grid(descending), `class="bookcard seriescard"`) != 1 {
		t.Fatalf("newest-first page did not emit the pile once:\n%s", grid(descending))
	}
	descNext := libraryNextURL(t, descending)
	if strings.Contains(libraryFragmentAt(t, f, descNext), `class="bookcard seriescard"`) {
		t.Fatal("series pile repeated on the second newest-first page")
	}

	_, ascending := f.get(t, "/ui/library?folder="+f.folder+"&dir=asc", f.cookie)
	if strings.Contains(grid(ascending), `class="bookcard seriescard"`) {
		t.Fatal("oldest volume emitted a pile before its aggregate sort position")
	}
	ascNext := libraryNextURL(t, ascending)
	if strings.Count(libraryFragmentAt(t, f, ascNext), `class="bookcard seriescard"`) != 1 {
		t.Fatal("series pile was not emitted at its newest volume on the next page")
	}
}

func i2(n int) string { return strconv.Itoa(n) }

var nextLibraryLink = regexp.MustCompile(`href="([^"]*cursor[^"]*)"`)

func libraryNextURL(t *testing.T, page string) string {
	t.Helper()
	match := nextLibraryLink.FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("library page has no continuation:\n%s", page)
	}
	return html.UnescapeString(match[1])
}

func libraryFragmentAt(t *testing.T, f *booksFixture, relative string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", f.ts.URL+"/ui/"+relative, nil)
	req.AddCookie(f.cookie)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestAnEmptyLibraryPointsAdminsAtTheAdminPanel: an administrator with
// no libraries yet is one click from creating one; anybody else is only
// told to ask an administrator, since the page they would land on is
// closed to them.
func TestAnEmptyLibraryPointsAdminsAtTheAdminPanel(t *testing.T) {
	f := newBooksFixture(t)
	if err := f.st.DeleteFolder(t.Context(), f.folder); err != nil {
		t.Fatal(err)
	}
	bob := f.login(t, "bob")

	_, html := f.get(t, "/ui/library", bob)
	if !strings.Contains(html, "watches no folders yet") {
		t.Fatalf("empty shelf not shown: %s", html)
	}
	if strings.Contains(html, `href="settings?section=admin`) {
		t.Fatal("a plain reader was offered the admin Settings page")
	}

	if err := f.st.SetUserAdmin(t.Context(), "u2", true); err != nil {
		t.Fatal(err)
	}
	_, html = f.get(t, "/ui/library", bob)
	if !strings.Contains(html, `href="settings?section=admin&amp;view=folders"`) {
		t.Fatalf("an administrator was not offered the admin Settings folders view: %s", html)
	}
}
