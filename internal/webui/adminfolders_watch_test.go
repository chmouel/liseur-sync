package webui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// recordingWatcher stands in for the running folder watcher.
type recordingWatcher struct {
	mu          sync.Mutex
	added       []store.Folder
	removed     []string
	scanFolders []store.Folder
	scanResult  store.ReconcileResult
	scanErr     error
}

func (w *recordingWatcher) ScanFolders(_ context.Context, folders []store.Folder) (store.ReconcileResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.scanFolders = append(w.scanFolders, folders...)
	return w.scanResult, w.scanErr
}

func (w *recordingWatcher) Add(_ context.Context, folder store.Folder) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.added = append(w.added, folder)
}

func (w *recordingWatcher) Remove(folderID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed = append(w.removed, folderID)
}

// TestAddingAFolderTellsTheWatcher: the panel says "its books appear as
// the server reads them", and that sentence is only true if the running
// watcher hears about the folder now. Without this call the row exists
// but nothing reads it until the half-hourly safety pass, so an
// administrator adds a folder, is congratulated, and then watches an
// empty shelf for thirty minutes.
func TestAddingAFolderTellsTheWatcher(t *testing.T) {
	watcher := &recordingWatcher{}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.Watching = watcher
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	csrf := extractCSRF(t, body)

	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Books"}, "root": {root},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatalf("the folder was not added: %s", body)
	}
	watcher.mu.Lock()
	added := append([]store.Folder(nil), watcher.added...)
	watcher.mu.Unlock()
	if len(added) != 1 || added[0].RootPath != root {
		t.Fatalf("watcher was told %+v, want one folder at %s", added, root)
	}

	// A refused folder must not be announced: there is no row to watch,
	// and a watcher asked to follow a path that was rejected would put
	// an inotify watch on a directory this server was told to leave
	// alone.
	_, _ = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"  "}, "root": {root},
	})
	watcher.mu.Lock()
	count := len(watcher.added)
	watcher.mu.Unlock()
	if count != 1 {
		t.Fatalf("a refused folder was announced: %d calls", count)
	}

	folders, err := st.ListFolders(t.Context(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/"+folders[0].ID+"/delete", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "Stopped watching") {
		t.Fatalf("the folder was not removed: %s", body)
	}
	watcher.mu.Lock()
	removed := append([]string(nil), watcher.removed...)
	watcher.mu.Unlock()
	if len(removed) != 1 || removed[0] != folders[0].ID {
		t.Fatalf("watcher was told to drop %v, want [%s]", removed, folders[0].ID)
	}
}

// TestAFolderCanBeAddedWithoutAWatcher: Watching is nil in every test
// server but this one, and a server started without a watcher must
// still accept a folder rather than panic on the announcement.
func TestAFolderCanBeAddedWithoutAWatcher(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	csrf := extractCSRF(t, body)
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Books"}, "root": {t.TempDir()},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatalf("a server with no watcher refused a folder: %s", body)
	}
}

// TestScanNowRunsAPassAndReportsIt: the button exists because inotify
// sees nothing on NFS or SMB and drops events under pressure, so an
// operator whose catalog is wrong has no other recourse but to wait half
// an hour. If the press does not reach the watcher, the page tells a
// comfortable lie and the wait happens anyway — so this pins both that
// it ran and that the page says what it found.
func TestScanNowRunsAPassAndReportsIt(t *testing.T) {
	watcher := &recordingWatcher{scanResult: store.ReconcileResult{Added: 1}}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.Watching = watcher
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	csrf := extractCSRF(t, body)
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Books"}, "root": {t.TempDir()},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatalf("the folder was not added: %s", body)
	}
	folders, err := st.ListFolders(t.Context(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}

	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/"+folders[0].ID+"/scan", url.Values{"csrf": {csrf}})
	// The button waits, so the page says what the pass found rather
	// than that one was asked for.
	if !strings.Contains(body, "1 added") {
		t.Fatalf("the scan did not report what it changed: %s", body)
	}
	watcher.mu.Lock()
	scanned := append([]store.Folder(nil), watcher.scanFolders...)
	watcher.mu.Unlock()
	if len(scanned) != 1 || scanned[0].ID != folders[0].ID {
		t.Fatalf("watcher was asked to scan %v, want just %s", scanned, folders[0].ID)
	}

	// A folder that is not there cannot be scanned, and saying so beats
	// a notice about a pass that will never run.
	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/nope/scan", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "no such folder") {
		t.Fatalf("an unknown folder was accepted: %s", body)
	}
	watcher.mu.Lock()
	count := len(watcher.scanFolders)
	watcher.mu.Unlock()
	if count != 1 {
		t.Fatalf("an unknown folder reached the watcher: %d calls", count)
	}
}

// TestScanNowWithoutAWatcherSaysSo: a server started without a watcher
// has nothing to ask, and a notice claiming a pass would be a lie the
// operator acts on.
func TestScanNowWithoutAWatcherSaysSo(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	csrf := extractCSRF(t, body)
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Books"}, "root": {t.TempDir()},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatal("the folder was not added")
	}
	folders, err := st.ListFolders(t.Context(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/"+folders[0].ID+"/scan", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "without a folder watcher") {
		t.Fatalf("a server with no watcher claimed a pass: %s", body)
	}
}

// TestPanelFolderIsGrantedToItsCreator. Issue #13 was that a folder
// added here was catalogued and then invisible, because visibility is a
// row in user_folders (ADR-0027) and nothing wrote one. The grant goes
// to the administrator who submitted the form and to nobody else:
// administrator status still confers no reading access.
func TestPanelFolderIsGrantedToItsCreator(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(t.Context(), store.User{
		ID: "u2", Name: "bob", Argon2Hash: "x", Timezone: "UTC",
		IsAdmin: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"Books"}, "root": {root},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatalf("the folder was not added: %s", body)
	}

	granted, err := st.ListUserFolders(t.Context(), "u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 1 || granted[0].Name != "Books" {
		t.Fatalf("the creator sees %+v, want the folder they just added", granted)
	}
	if got, err := st.ListUserFolders(t.Context(), "u2", "", 10); err != nil ||
		len(got) != 0 {
		t.Fatalf("another administrator was granted %+v, %v", got, err)
	}
}

// TestFoldersPageMarksOneNobodyCanRead. A folder with no grant is
// scanned, catalogued and invisible, and the only place that is
// discoverable is this page. Marking it is what turns issue #13 from a
// mystery into a two-click fix.
func TestFoldersPageMarksOneNobodyCanRead(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateFolder(t.Context(), store.Folder{
		ID: "lonely", Name: "Nobody's Books", RootPath: t.TempDir(),
		Kind: store.FolderPlain,
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	if !strings.Contains(body, "No account can read this folder") {
		t.Fatalf("an unreadable folder was not marked:\n%s", body)
	}

	if err := st.AssignUserFolder(t.Context(), "u1", "lonely"); err != nil {
		t.Fatal(err)
	}
	_, body = page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	if strings.Contains(body, "No account can read this folder") {
		t.Fatalf("a granted folder is still marked unreadable:\n%s", body)
	}
}

// scanPost posts to a scan route the way a browser or htmx would, and
// hands back the raw response: what these tests are about is the
// redirect header, which the browser-shaped helpers follow and discard.
func scanPost(
	t *testing.T, ts *httptest.Server, cookie *http.Cookie,
	path string, form url.Values, htmx bool,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func adminWithFolder(t *testing.T, watcher *recordingWatcher) (*httptest.Server, store.Store, *http.Cookie, string) {
	t.Helper()
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.Watching = watcher
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=folders")
	csrf := extractCSRF(t, body)
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Books"}, "root": {t.TempDir()},
	})
	if !strings.Contains(body, "Watching Books") {
		t.Fatalf("the folder was not added: %s", body)
	}
	return ts, st, cookie, csrf
}

// TestLibraryScanRedirectsWhereTheReaderWas pins the one thing about
// this route that is easy to get wrong and invisible in a unit test that
// only greps for "notice".
//
// The same destination has to be spelled two different ways. A 303's
// Location is resolved by the browser against the request URL —
// /ui/library/scan — so it needs the hop back out; htmx assigns
// HX-Redirect to location.href, resolved against the page the reader is
// on — /ui/library — so it needs the target the page itself would write.
// Swapping them lands on /ui/library/library and /library respectively,
// and both are 404s.
func TestLibraryScanRedirectsWhereTheReaderWas(t *testing.T) {
	watcher := &recordingWatcher{scanResult: store.ReconcileResult{Added: 3}}
	ts, _, cookie, csrf := adminWithFolder(t, watcher)

	// "library?folder=f1" is what the page writes into the hidden field.
	form := url.Values{"csrf": {csrf}, "back": {"library?folder=f1"}}

	resp := scanPost(t, ts, cookie, "/ui/library/scan", form, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain POST status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "../library?folder=f1&notice=") {
		t.Fatalf("Location = %q, want a target relative to /ui/library/scan", loc)
	}
	// The browser's own resolution, which is the thing that matters.
	base, _ := url.Parse(ts.URL + "/ui/library/scan")
	landed, err := base.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Path != "/ui/library" {
		t.Fatalf("a browser following that lands on %q, want /ui/library", landed.Path)
	}

	resp = scanPost(t, ts, cookie, "/ui/library/scan", form, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("htmx POST status = %d, want 200", resp.StatusCode)
	}
	hx := resp.Header.Get("HX-Redirect")
	if !strings.HasPrefix(hx, "library?folder=f1&notice=") {
		t.Fatalf("HX-Redirect = %q, want a target relative to /ui/library", hx)
	}
	base, _ = url.Parse(ts.URL + "/ui/library")
	landed, err = base.Parse(hx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Path != "/ui/library" {
		t.Fatalf("htmx sets location.href to %q, want /ui/library", landed.Path)
	}

	watcher.mu.Lock()
	scanned := len(watcher.scanFolders)
	watcher.mu.Unlock()
	if scanned != 2 {
		t.Fatalf("ScanFolders saw %d folders over two requests, want 2", scanned)
	}
}

// TestLibraryScanRefusesAnOffsiteBack. back arrives in a form field, so
// it is somewhere to send a browser that a request chose. Every other
// place this UI takes one (prefs.go) runs it through safeUIPath first,
// and a scan button is not the place to stop.
func TestLibraryScanRefusesAnOffsiteBack(t *testing.T) {
	ts, _, cookie, csrf := adminWithFolder(t, &recordingWatcher{})
	for _, back := range []string{
		"//evil.example/", "https://evil.example/", "/ui/library",
	} {
		resp := scanPost(t, ts, cookie, "/ui/library/scan",
			url.Values{"csrf": {csrf}, "back": {back}}, true)
		hx := resp.Header.Get("HX-Redirect")
		if !strings.HasPrefix(hx, "library?") {
			t.Errorf("back=%q produced HX-Redirect %q, want the library page", back, hx)
		}
	}
}

// TestLibraryScanHidesTheRootPathFromAReader. A reconcile failure names
// the folder's root path (ErrRootUnavailable wraps it), and root_path is
// a filesystem oracle no non-administrator is ever shown. The
// administrator who can act on it still gets the error itself.
func TestLibraryScanHidesTheRootPathFromAReader(t *testing.T) {
	const secret = "/srv/private/books"
	watcher := &recordingWatcher{scanErr: errors.New("root unavailable: " + secret)}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.Watching = watcher
	})
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(t.Context(), store.User{
		ID: "u2", Name: "bob", Argon2Hash: hash, Timezone: "UTC",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateFolder(t.Context(), store.Folder{
		ID: "f1", Name: "Books", RootPath: secret, Kind: store.FolderPlain,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignUserFolder(t.Context(), "u2", "f1"); err != nil {
		t.Fatal(err)
	}

	reader := loginCookieAs(t, ts, "bob", "hunter2hunter")
	_, body := page(t, ts, reader, "/ui/library")
	resp := scanPost(t, ts, reader, "/ui/library/scan",
		url.Values{"csrf": {extractCSRF(t, body)}, "back": {"library"}}, true)
	hx := resp.Header.Get("HX-Redirect")
	if strings.Contains(hx, "srv") || strings.Contains(hx, url.QueryEscape(secret)) {
		t.Fatalf("a reader was told the root path: %q", hx)
	}
	if !strings.Contains(hx, "problem=") {
		t.Fatalf("a failed scan did not report a problem: %q", hx)
	}

	// The same failure, to an administrator, keeps the detail.
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	admin := loginCookie(t, ts)
	_, body = page(t, ts, admin, "/ui/library")
	resp = scanPost(t, ts, admin, "/ui/library/scan",
		url.Values{"csrf": {extractCSRF(t, body)}, "back": {"library"}}, true)
	if hx := resp.Header.Get("HX-Redirect"); !strings.Contains(hx, url.QueryEscape(secret)) {
		t.Fatalf("an administrator was not told why the scan failed: %q", hx)
	}
}

// TestAdminScanAllFoldersRedirectsToTheFoldersView. The admin views live
// at /ui/settings, so the page-relative spelling htmx needs is the bare
// href — not one computed from /ui/admin/folders/scan-all, which would
// send location.href three directories above /ui.
func TestAdminScanAllFoldersRedirectsToTheFoldersView(t *testing.T) {
	watcher := &recordingWatcher{scanResult: store.ReconcileResult{Added: 1, Updated: 2}}
	ts, _, cookie, csrf := adminWithFolder(t, watcher)
	_, _ = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"More"}, "root": {t.TempDir()},
	})

	resp := scanPost(t, ts, cookie, "/ui/admin/folders/scan-all",
		url.Values{"csrf": {csrf}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("htmx POST status = %d, want 200", resp.StatusCode)
	}
	hx := resp.Header.Get("HX-Redirect")
	base, _ := url.Parse(ts.URL + "/ui/settings?section=admin&view=folders")
	landed, err := base.Parse(hx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Path != "/ui/settings" {
		t.Fatalf("htmx sets location.href to %q (from %q), want /ui/settings",
			landed.Path, hx)
	}
	if landed.Query().Get("view") != settingsAdminFolders {
		t.Fatalf("HX-Redirect %q does not return to the folders view", hx)
	}
	if !strings.Contains(landed.Query().Get("notice"), "1 added") ||
		!strings.Contains(landed.Query().Get("notice"), "2 updated") {
		t.Fatalf("notice %q does not say what the pass changed", landed.Query().Get("notice"))
	}

	watcher.mu.Lock()
	scanned := len(watcher.scanFolders)
	watcher.mu.Unlock()
	if scanned != 2 {
		t.Fatalf("ScanFolders saw %d folders, want both", scanned)
	}
}

// TestAdminScanFolderAnswersHTMX. The per-folder button posts through
// htmx, and htmx swaps a 200 into the element that made the request. A
// handler that renders a whole settings page there puts a document
// inside a <form>; this route has to redirect instead.
func TestAdminScanFolderAnswersHTMX(t *testing.T) {
	watcher := &recordingWatcher{}
	ts, st, cookie, csrf := adminWithFolder(t, watcher)
	folders, err := st.ListFolders(t.Context(), "", "", 10)
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders = %+v, %v", folders, err)
	}
	resp := scanPost(t, ts, cookie,
		"/ui/admin/folders/"+folders[0].ID+"/scan", url.Values{"csrf": {csrf}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hx := resp.Header.Get("HX-Redirect"); hx == "" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("no HX-Redirect; htmx would swap this into the form:\n%s", body)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<html") {
		t.Fatalf("an htmx scan answered with a whole page:\n%s", body)
	}
}

// TestScanNoticeNamesEveryCounter. A pass that only purged or only
// rekeyed changed something, and a sentence that lists three of the
// seven counters reports it as nothing.
func TestScanNoticeNamesEveryCounter(t *testing.T) {
	if got := scanNotice(store.ReconcileResult{}, "up to date"); got != "Scan complete. up to date" {
		t.Fatalf("an unchanged pass said %q", got)
	}
	got := scanNotice(store.ReconcileResult{Purged: 2}, "up to date")
	if !strings.Contains(got, "2 purged") {
		t.Fatalf("a purge-only pass said %q", got)
	}
	got = scanNotice(store.ReconcileResult{Added: 1, Rekeyed: 4}, "up to date")
	if !strings.Contains(got, "1 added") || !strings.Contains(got, "4 rekeyed") {
		t.Fatalf("notice %q dropped a counter", got)
	}
}

// TestScanProblemDoesNotPromiseABackgroundScan. The budget cancels the
// pass; it does not detach it. A message saying the scan carries on
// would tell a reader to wait for something that is not running, and
// the honest answer is that it stopped and the periodic pass will
// finish. This pins the distinction, because it is easy to write the
// reassuring version by accident.
func TestScanProblemDoesNotPromiseABackgroundScan(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("test setup: context error is %v", ctx.Err())
	}

	for _, admin := range []bool{true, false} {
		got := scanProblem(ctx, errors.New("root unavailable: /srv/books"), admin)
		if strings.Contains(got, "background") || strings.Contains(got, "will finish") {
			t.Errorf("timeout message promises a background scan (admin=%v): %q", admin, got)
		}
		if !strings.Contains(got, "stopped") {
			t.Errorf("timeout message does not say it stopped (admin=%v): %q", admin, got)
		}
		// A timeout is reported before the error is, so the root path
		// never reaches the page by this route either.
		if strings.Contains(got, "/srv/books") {
			t.Errorf("timeout message leaked the root path (admin=%v): %q", admin, got)
		}
	}
}
