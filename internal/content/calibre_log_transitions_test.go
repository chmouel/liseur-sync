//go:build linux

package content

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TestACalibreMissingFileIsLoggedOnTransitionsOnly. The interesting
// moments in a book's absence are its edges: the pass that watched an
// active book vanish, and the pass that found it back. Every pass in
// between is the same fact restated, and a reminder every half hour is
// noise an operator learns to ignore — which is worse than silence,
// because the line that matters then scrolls past unread. A book that
// has never been servable never gets past debug either: the store keeps
// no row for it, so there is no state an INFO-once could hang off.
func TestACalibreMissingFileIsLoggedOnTransitionsOnly(t *testing.T) {
	root := writeCalibreLibrary(t)
	catalog := newFakeCatalog()
	folder := store.Folder{ID: "f-calibre", RootPath: root, Kind: store.FolderCalibre}

	var buf bytes.Buffer
	r := NewReconciler(catalog, ScanLimits{}, epub.DefaultLimits(),
		slog.New(slog.NewJSONHandler(&buf, nil)))

	pass := func() {
		t.Helper()
		if _, err := r.Reconcile(context.Background(), folder); err != nil {
			t.Fatal(err)
		}
	}
	logged := func(msg string) int {
		return strings.Count(buf.String(), msg)
	}
	goneID := int64(2)
	markActive := func() {
		// What the catalog holds after 'Gone' had a servable file: the
		// real store would carry the row as active from that pass.
		catalog.mu.Lock()
		catalog.known[folder.ID] = []store.KnownBook{
			{ID: "b-gone", CalibreID: &goneID, Status: store.BookActive},
		}
		catalog.mu.Unlock()
	}

	// A book metadata.db listed before the server ever saw a file for
	// it is not a transition: there is no row to remember it by, so an
	// INFO here would repeat on every pass, forever.
	pass()
	if n := logged("no file on disk"); n != 0 {
		t.Fatalf("a never-servable book was reported at INFO:\n%s", buf.String())
	}

	// The disappearance of a book the catalog held as active is news,
	// said once.
	markActive()
	buf.Reset()
	pass()
	if n := logged("calibre book has no file on disk"); n != 1 {
		t.Fatalf("an active book vanishing should be reported once, got %d:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), `"title":"Gone"`) {
		t.Fatalf("the report should name the book:\n%s", buf.String())
	}

	// The next pass over the same absence: the fake, like the store,
	// now carries the row as missing, and the pass says nothing at INFO.
	buf.Reset()
	pass()
	if n := logged("no file on disk"); n != 0 {
		t.Fatalf("a book already carried as missing was reported again:\n%s", buf.String())
	}

	// The file comes back: the return is news, said once.
	writeBook(t, root, filepath.Join("A Writer/Gone (2)", "Gone.epub"), "Gone")
	buf.Reset()
	pass()
	if n := logged("calibre book has a file on disk again"); n != 1 {
		t.Fatalf("the return should be reported once, got %d:\n%s", n, buf.String())
	}

	// A settled library says nothing at all.
	buf.Reset()
	pass()
	if buf.Len() != 0 {
		t.Fatalf("a pass over a settled library should be silent:\n%s", buf.String())
	}
}

// TestACalibreTransitionIsNotLoggedWhenTheStoreRejectsThePass. A
// transition line is a claim about the catalog, so it may only be
// written once the store has committed it: logged during the scan it
// would announce a change a failed reconcile never made, and then
// announce it again on the retry that did.
func TestACalibreTransitionIsNotLoggedWhenTheStoreRejectsThePass(t *testing.T) {
	root := writeCalibreLibrary(t)
	catalog := newFakeCatalog()
	folder := store.Folder{ID: "f-calibre", RootPath: root, Kind: store.FolderCalibre}
	goneID := int64(2)
	catalog.mu.Lock()
	catalog.known[folder.ID] = []store.KnownBook{
		{ID: "b-gone", CalibreID: &goneID, Status: store.BookActive},
	}
	catalog.fail = true
	catalog.mu.Unlock()

	var buf bytes.Buffer
	r := NewReconciler(catalog, ScanLimits{}, epub.DefaultLimits(),
		slog.New(slog.NewJSONHandler(&buf, nil)))

	if _, err := r.Reconcile(context.Background(), folder); err == nil {
		t.Fatal("the fake was told to fail and did not")
	}
	if strings.Contains(buf.String(), "no file on disk") {
		t.Fatalf("a transition was announced for a pass the store rejected:\n%s", buf.String())
	}
}
