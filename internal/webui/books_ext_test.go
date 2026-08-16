//go:build linux

// Package webui_test exercises the books UI against the real content
// package rather than against fakes. It lives outside the package
// because the API server imports this one, and a page that claims a
// book can be downloaded is only worth anything if the bytes behind it
// came off a real folder the way a scan puts them there.
package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/api"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/webui"
)

type booksFixture struct {
	ts     *httptest.Server
	st     store.Store
	cache  *content.Cache
	rec    *content.Reconciler
	api    *api.Server
	ui     *webui.Server
	cfg    config.Config
	cookie *http.Cookie
	// folder is the id of the watched folder, and root is where it is on
	// disk. Every book in these tests is a file written under root and
	// then reconciled, because that is the only way a book gets into the
	// catalog now (ADR-0017).
	folder string
	root   string
}

func newBooksFixture(t *testing.T) *booksFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cache, err := content.OpenCache(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	hash, _ := auth.HashPassword("hunter2hunter")
	for _, u := range []store.User{
		{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()},
		{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()},
	} {
		if err := st.CreateUser(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}

	root := filepath.Join(dir, "books")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := store.Folder{
		ID: "folder-web", Name: "Alice's Books", RootPath: root,
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateFolder(t.Context(), folder); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.InsecureHTTP = true
	apiSrv := &api.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Files:        content.NewFiles(st), Covers: cache,
	}
	ui := &webui.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Downloads:    apiSrv, Covers: apiSrv,
	}
	mux := http.NewServeMux()
	ui.Mount(mux, func(h http.Handler) http.Handler { return h })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	f := &booksFixture{
		ts: ts, st: st, cache: cache, api: apiSrv, ui: ui, cfg: cfg,
		folder: folder.ID, root: root,
		rec: content.NewReconciler(st, content.ScanLimits{},
			cfg.EPUBLimits(), nil),
	}
	f.cookie = f.login(t, "alice")
	return f
}

// addBook writes a publication into the watched folder and runs the pass
// that puts it in the catalog, then returns the book id the store minted
// for it. This is the only way books arrive now: there is no upload.
func (f *booksFixture) addBook(t *testing.T, name string, body []byte) string {
	t.Helper()
	relative := name + ".epub"
	if err := os.WriteFile(filepath.Join(f.root, relative), body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	return f.bookAt(t, relative)
}

// observe states one book to the catalog without a file behind it, for
// the tests that care what a page does with metadata rather than with
// bytes. complete is false, which is the store's word for "this pass has
// no opinion about what it did not see" — so seeding one book does not
// declare every other one missing.
func (f *booksFixture) observe(t *testing.T, obs store.ObservedBook) string {
	t.Helper()
	if _, err := f.st.ReconcileFolder(t.Context(), f.folder,
		[]store.ObservedBook{obs}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return f.bookAt(t, obs.RelativePath)
}

// bookWithMetadata catalogues a book carrying the metadata a pass reads
// out of a file, without needing a file that really holds it.
func bookWithMetadata(t *testing.T, f *booksFixture, name string) string {
	t.Helper()
	return f.observe(t, store.ObservedBook{
		RelativePath: name + ".epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256:    strings.Repeat(name[:1], 64),
		OriginalFilename: name + ".epub", MediaType: "application/epub+zip",
		Title:         "Title of " + name,
		Description:   "A book about " + name + ".",
		Publisher:     "A Publisher",
		PublishedDate: "1979",
		Tags:          []string{"fiction"},
		Series:        []store.ObservedSeries{{Name: "A Series"}},
		Contributors: []store.ObservedContributor{
			{Name: "Ann Author", Role: store.ContributorRoleAuthor, Position: 1},
		},
	})
}

// reconcile runs one pass over the fixture's folder, exactly as the
// watcher does.
func (f *booksFixture) reconcile(t *testing.T) {
	t.Helper()
	folder, err := f.st.FolderByID(t.Context(), f.folder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rec.Reconcile(t.Context(), folder); err != nil {
		t.Fatal(err)
	}
}

// bookAt finds the catalogued book for one relative path. A path is the
// identity of a book in a plain folder, so this is a lookup rather than
// a guess.
func (f *booksFixture) bookAt(t *testing.T, relative string) string {
	t.Helper()
	books, err := f.st.ListCatalogBooks(t.Context(), f.folder, nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.RelativePath == relative {
			return b.ID
		}
	}
	t.Fatalf("no catalogued book at %q", relative)
	return ""
}

// uiWithoutCovers is the same UI on the same data with content storage
// switched off, which is a supported configuration rather than an error.
func (f *booksFixture) uiWithoutCovers() *webui.Server {
	return &webui.Server{
		St: f.st, Auth: auth.NewService(f.st), Cfg: f.cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
	}
}

func (f *booksFixture) login(t *testing.T, user string) *http.Cookie {
	t.Helper()
	return f.loginTo(t, f.ts, user)
}

// loginTo signs in against a specific server, so a test can exercise a
// second instance over the same database.
func (f *booksFixture) loginTo(
	t *testing.T, server *httptest.Server, user string,
) *http.Cookie {
	t.Helper()
	resp, err := noRedirectClient().PostForm(server.URL+"/ui/login", url.Values{
		"username": {user}, "password": {"hunter2hunter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if strings.Contains(c.Name, "session") {
			return c
		}
	}
	t.Fatalf("no session cookie for %s", user)
	return nil
}

// readerCookie signs in a second account. It needs no grant: every
// signed-in account reads every folder (ADR-0017).
func (f *booksFixture) readerCookie(t *testing.T) *http.Cookie {
	t.Helper()
	return f.login(t, "bob")
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (f *booksFixture) get(t *testing.T, path string, cookie *http.Cookie) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
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

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatal("no csrf field in page")
	}
	rest := html[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// postForm posts a urlencoded form the way the page's buttons do.
func (f *booksFixture) postForm(
	t *testing.T, path string, cookie *http.Cookie, form url.Values,
) *http.Response {
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
	resp.Body.Close()
	return resp
}

// TestBooksUIServesABook is the browser half of the MVP: a file appears
// in a watched folder, a pass catalogues it, and the page it gets is a
// page you can download the same bytes from.
func TestBooksUIServesABook(t *testing.T) {
	f := newBooksFixture(t)
	body := []byte(strings.Repeat("web-epub", 50))
	bookID := f.addBook(t, "moby", body)

	resp, html := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("library: %d", resp.StatusCode)
	}
	if !strings.Contains(html, bookID) {
		t.Fatalf("the book is not on the shelf:\n%s", html)
	}

	resp, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("book page: %d", resp.StatusCode)
	}
	if !strings.Contains(page, "moby.epub") {
		t.Errorf("the book page does not name its file:\n%s", page)
	}
	// Nothing on this page changes anything: the folder is somebody
	// else's and this server only reads it.
	for _, gone := range []string{"/metadata", "/delete", "/restore", "/accept"} {
		if strings.Contains(page, gone) {
			t.Errorf("book page still offers %q", gone)
		}
	}

	resp, download := f.get(t, "/ui/books/"+bookID+"/download", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d", resp.StatusCode)
	}
	if download != string(body) {
		t.Error("the download is not the bytes that were put in the folder")
	}
}

// TestBookPageIsANotFoundForAnUnknownID: an id that is not a book is a
// 404, not a blank page and not a 500.
func TestBookPageIsANotFoundForAnUnknownID(t *testing.T) {
	f := newBooksFixture(t)
	resp, _ := f.get(t, "/ui/books/no-such-book", f.cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown book: %d, want 404", resp.StatusCode)
	}
}

// TestBooksUIIsSharedByEverySignedInAccount pins ADR-0017's decision:
// the catalog is the server's, not an account's. There is no owner and
// no grant, so bob sees the same shelf alice does.
func TestBooksUIIsSharedByEverySignedInAccount(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "shared", []byte(strings.Repeat("shared-bytes", 40)))

	bob := f.login(t, "bob")
	resp, html := f.get(t, "/ui/library?folder="+f.folder, bob)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob's library: %d", resp.StatusCode)
	}
	if !strings.Contains(html, bookID) {
		t.Fatalf("bob cannot see the shared catalog:\n%s", html)
	}
	resp, _ = f.get(t, "/ui/books/"+bookID, bob)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob's book page: %d", resp.StatusCode)
	}
	resp, _ = f.get(t, "/ui/books/"+bookID+"/download", bob)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob's download: %d", resp.StatusCode)
	}
}

// TestBooksUIRequiresASession: every books route is behind the session,
// so an unauthenticated request is sent to the login page rather than
// being answered.
func TestBooksUIRequiresASession(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "private", []byte(strings.Repeat("bytes", 40)))
	for _, path := range []string{
		"/ui/library",
		"/ui/books/" + bookID,
		"/ui/books/" + bookID + "/download",
		"/ui/books/" + bookID + "/cover",
		"/ui/folders/" + f.folder + "/search",
		"/ui/folders/" + f.folder + "/series",
	} {
		resp, _ := f.get(t, path, nil)
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s: %d, want a redirect to login", path, resp.StatusCode)
		}
	}
}

// TestBooksUIDropsAMissingBooksDownload: a file that leaves the folder
// keeps its row — everybody's reading positions hang off it — but the
// page stops offering bytes it no longer has.
func TestBooksUIDropsAMissingBooksDownload(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "vanishing", []byte(strings.Repeat("gone-soon", 40)))
	// A second book that stays: a pass that sees nothing at all is
	// indistinguishable from an unmounted disk, so the store declines to
	// conclude anything from an empty folder.
	f.addBook(t, "staying", []byte(strings.Repeat("still-here", 40)))

	if err := os.Remove(filepath.Join(f.root, "vanishing.epub")); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)

	book, err := f.st.CatalogBookByID(t.Context(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	if book.Status != store.BookMissing {
		t.Fatalf("status = %q, want missing", book.Status)
	}
	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if strings.Contains(page, `books/`+bookID+`/download"`) {
		t.Errorf("a missing book still offers a download:\n%s", page)
	}
	if strings.Contains(page, `books/`+bookID+`/read"`) {
		t.Errorf("a missing book still offers a reader:\n%s", page)
	}
}

// TestBooksGridAndListViews covers the revamp's browse shapes (ADR-0011):
// a grid of covers by default, the old table on request, and the same
// books either way.
func TestBooksGridAndListViews(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))

	_, html := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	csrf := csrfFrom(t, html)
	if !strings.Contains(html, `class="grid"`) || !strings.Contains(html, `class="bookcard"`) {
		t.Fatal("default browse view is not a grid of cards")
	}
	if !strings.Contains(html, "/cover?size=thumbnail") {
		t.Error("grid does not ask for the cached thumbnail")
	}
	if !strings.Contains(html, bookID) {
		t.Error("the book is missing from the grid")
	}

	// Switching to the list is a form post like every other mutation,
	// and it comes back to the folder that was being looked at.
	resp := f.postForm(t, "/ui/preferences", f.cookie, url.Values{
		"csrf": {csrf}, "view": {"list"},
		"back": {"library?folder=" + f.folder},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("view toggle: want 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "library?folder="+f.folder {
		t.Errorf("view toggle returned to %q", loc)
	}
	var pref *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "liseur_ui" {
			pref = c
		}
	}
	if pref == nil {
		t.Fatal("view toggle set no preference cookie")
	}

	req, _ := http.NewRequest("GET", f.ts.URL+"/ui/library?folder="+f.folder, nil)
	req.AddCookie(f.cookie)
	req.AddCookie(pref)
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	list := string(body)
	if strings.Contains(list, `class="bookcard"`) {
		t.Error("list view still rendered cards")
	}
	if !strings.Contains(list, "<table>") || !strings.Contains(list, bookID) {
		t.Error("list view lost the table or the book")
	}
}

// TestBooksHTMXFragmentIsOnlyTheCards pins the endless-scroll contract:
// htmx gets cards and a sentinel, never a second copy of the shell.
func TestBooksHTMXFragmentIsOnlyTheCards(t *testing.T) {
	f := newBooksFixture(t)
	f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))

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
	if !strings.Contains(frag, `class="bookcard"`) {
		t.Fatal("fragment has no cards")
	}
	for _, shell := range []string{"<html", `class="rail"`, `class="topbar"`, "<table>"} {
		if strings.Contains(frag, shell) {
			t.Errorf("fragment contains %q, so htmx would append the whole page", shell)
		}
	}
}

// TestReadingCardOffersToCarryOnReading is the shelf's whole point: a
// book that is being read and that this server holds an EPUB of must be
// one click from where the reader left off. The cover is that click, the
// numbers behind it are a sublink away, and a work whose file this
// server does not hold offers no Read at all rather than a link that
// leads to an error page.
func TestReadingCardOffersToCarryOnReading(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	now := time.Now().UTC()
	if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", bookID,
		store.Work{ID: "w-read", UserID: "u1", Title: "novel", CreatedAt: now},
		nil, nil, true, now); err != nil {
		t.Fatal(err)
	}
	// A second work with no catalog book at all: progress synced from a
	// device holding a file this server never saw.
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "w-elsewhere", UserID: "u1", Title: "Elsewhere", CreatedAt: now},
		nil, []store.Identifier{{Kind: "sha256", Value: "beef"}}); err != nil {
		t.Fatal(err)
	}

	resp, page := f.get(t, "/ui/library", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading page: %d", resp.StatusCode)
	}
	for _, want := range []string{
		`href="books/` + bookID + `/read"`,
		`href="works/w-read"`,
		`href="works/w-read#sessions"`,
		`href="books/` + bookID + `"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("reading card is missing %s:\n%s", want, page)
		}
	}
	// The cover itself is the click that opens the book — and it says
	// which of the two things it will do, since a book that has been
	// started is resumed and one that has not is opened at the front.
	if !strings.Contains(page, `href="books/`+bookID+`/read" title="Read novel"><img`) {
		t.Errorf("the cover does not open the book:\n%s", page)
	}
	// The unmapped work keeps its own page as the only destination.
	if strings.Contains(page, `w-elsewhere/read`) {
		t.Error("a work with no file here was offered a reader")
	}

	// The work page is where the numbers live, and it says how to get
	// back into the book rather than only reporting on it.
	_, work := f.get(t, "/ui/works/w-read", f.cookie)
	for _, want := range []string{`id="sessions"`, `id="stats"`,
		`href="../books/` + bookID + `/read"`} {
		if !strings.Contains(work, want) {
			t.Errorf("work page is missing %s:\n%s", want, work)
		}
	}
}

// TestReadingCardHidesReadWithoutAFile pins the other half: a work
// mapped to a book whose file this server cannot open any more must not
// offer to open it.
func TestReadingCardHidesReadWithoutAFile(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "vanished", []byte(strings.Repeat("web-epub", 50)))
	f.addBook(t, "staying", []byte(strings.Repeat("still-here", 40)))
	now := time.Now().UTC()
	if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", bookID,
		store.Work{ID: "w-empty", UserID: "u1", Title: "Vanished", CreatedAt: now},
		nil, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, "vanished.epub")); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)

	_, page := f.get(t, "/ui/library", f.cookie)
	if strings.Contains(page, `books/`+bookID+`/read`) {
		t.Errorf("offered to read a book whose file is gone:\n%s", page)
	}
	if !strings.Contains(page, `href="works/w-empty"`) {
		t.Errorf("the work is not linked at all:\n%s", page)
	}
	// The cover still shows, and it goes to the numbers instead.
	if !strings.Contains(page, `books/`+bookID+`/cover?size=thumbnail`) {
		t.Errorf("cover missing:\n%s", page)
	}
}
