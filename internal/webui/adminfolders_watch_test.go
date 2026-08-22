package webui

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// recordingWatcher stands in for the running folder watcher.
type recordingWatcher struct {
	mu      sync.Mutex
	added   []store.Folder
	removed []string
	scanned []string
}

func (w *recordingWatcher) Scan(folderID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.scanned = append(w.scanned, folderID)
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

// TestScanNowAsksTheWatcher: the button exists because inotify sees
// nothing on NFS or SMB and drops events under pressure, so an operator
// whose catalog is wrong has no other recourse but to wait half an hour.
// If the press does not reach the watcher, the page tells a comfortable
// lie and the wait happens anyway.
func TestScanNowAsksTheWatcher(t *testing.T) {
	watcher := &recordingWatcher{}
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
	if !strings.Contains(body, "Reading Books again") {
		t.Fatalf("the scan was not acknowledged: %s", body)
	}
	watcher.mu.Lock()
	scanned := append([]string(nil), watcher.scanned...)
	watcher.mu.Unlock()
	if len(scanned) != 1 || scanned[0] != folders[0].ID {
		t.Fatalf("watcher was asked to scan %v, want [%s]", scanned, folders[0].ID)
	}

	// A folder that is not there cannot be scanned, and saying so beats
	// a notice about a pass that will never run.
	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/nope/scan", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "no such folder") {
		t.Fatalf("an unknown folder was accepted: %s", body)
	}
	watcher.mu.Lock()
	count := len(watcher.scanned)
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
