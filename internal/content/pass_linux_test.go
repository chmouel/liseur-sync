//go:build linux

package content

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// fakeCatalog records what a pass concluded without a database in the
// way. The rules being tested here are about what the reconciler *says*,
// not about what a store does with it — those live in
// internal/store/storetest.
type fakeCatalog struct {
	mu       sync.Mutex
	known    map[string][]store.KnownBook
	folders  []store.Folder
	calls    int
	observed []store.ObservedBook
	complete bool
	fail     bool
	changed  chan struct{}
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		known:   map[string][]store.KnownBook{},
		changed: make(chan struct{}, 64),
	}
}

func (c *fakeCatalog) addFolder(f store.Folder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.folders = append(c.folders, f)
}

func (c *fakeCatalog) BooksInFolder(
	_ context.Context, folderID string,
) ([]store.KnownBook, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.known[folderID], nil
}

func (c *fakeCatalog) ReconcileFolder(
	_ context.Context, folderID string, observed []store.ObservedBook,
	complete bool, at time.Time,
) (store.ReconcileResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.observed = observed
	c.complete = complete
	if c.fail {
		return store.ReconcileResult{}, errors.New("the catalog was told to reject this pass")
	}
	// Only a pass allowed to conclude anything gets to rewrite what is
	// known, which is the same rule the real store enforces — including
	// its treatment of an unservable observation: an existing row is
	// marked missing and kept, a book that has never been servable gets
	// no row at all.
	if complete && len(observed) > 0 {
		prior := map[int64]store.KnownBook{}
		for _, b := range c.known[folderID] {
			if b.CalibreID != nil {
				prior[*b.CalibreID] = b
			}
		}
		books := make([]store.KnownBook, 0, len(observed))
		for _, o := range observed {
			if o.Unservable {
				if o.CalibreID != nil {
					if b, ok := prior[*o.CalibreID]; ok {
						b.Status = store.BookMissing
						books = append(books, b)
					}
				}
				continue
			}
			books = append(books, store.KnownBook{
				ID:            folderID + "-" + o.RelativePath,
				RelativePath:  o.RelativePath,
				SizeBytes:     o.SizeBytes,
				MTime:         o.MTime,
				ContentSHA256: o.ContentSHA256,
				CalibreID:     o.CalibreID,
				Status:        store.BookActive,
			})
		}
		c.known[folderID] = books
	}
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return store.ReconcileResult{}, nil
}

func (c *fakeCatalog) snapshot() (int, []store.ObservedBook, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.observed, c.complete
}

// ListFolders and FolderByID make the fake a FolderSource too, so a
// watcher test does not need a second double.
func (c *fakeCatalog) ListFolders(
	_ context.Context, _ string, after string, limit int,
) ([]store.Folder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []store.Folder{}
	for _, f := range c.folders {
		if f.ID > after {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *fakeCatalog) FolderByID(
	_ context.Context, _ string, folderID string,
) (store.Folder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.folders {
		if f.ID == folderID {
			return f, nil
		}
	}
	return store.Folder{}, store.ErrNotFound
}

func testReconciler(catalog Catalog) *Reconciler {
	return NewReconciler(catalog, ScanLimits{}, epub.DefaultLimits(),
		slog.New(slog.DiscardHandler))
}

// makeTestEPUB builds the smallest publication epub.Validate accepts, so
// a pass reads real metadata rather than falling back to the filename.
func makeTestEPUB(t *testing.T, title string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := []struct {
		name, body string
		method     uint16
	}{
		{"mimetype", "application/epub+zip", zip.Store},
		{"META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate},
		{"OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">` +
			`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` +
			`<dc:title>` + title + `</dc:title></metadata>` +
			`<manifest><item href="nav.xhtml" media-type="application/xhtml+xml"` +
			` properties="nav"/></manifest></package>`, zip.Deflate},
		{"OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml">` +
			`<body><nav/></body></html>`, zip.Deflate},
	}
	for _, e := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeBook puts a publication at a path below root, creating whatever
// directories it needs. This is the test's own writing, not the
// server's: nothing under a watched root is ever written by the code
// being tested, which is what TestAPassNeverWritesUnderAWatchedFolder
// asserts.
func writeBook(t *testing.T, root, relative, title string) {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, makeTestEPUB(t, title), 0o644); err != nil {
		t.Fatal(err)
	}
}

func plainFolder(t *testing.T) store.Folder {
	t.Helper()
	return store.Folder{
		ID: "f1", Name: "Books", RootPath: t.TempDir(),
		Kind: store.FolderPlain,
	}
}

func observedPaths(observed []store.ObservedBook) []string {
	out := make([]string, 0, len(observed))
	for _, o := range observed {
		out = append(out, o.RelativePath)
	}
	sort.Strings(out)
	return out
}

// TestPassReadsWhatIsOnDiskAndIsIdempotent. Running a pass twice is
// running it once: the second sees the same files, recognises them by
// their stat, and does not re-read them.
func TestPassReadsWhatIsOnDiskAndIsIdempotent(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	writeBook(t, folder.RootPath, "two.epub", "Two")
	catalog := newFakeCatalog()
	r := testReconciler(catalog)

	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, complete := catalog.snapshot()
	if !complete {
		t.Fatal("a pass that read the whole tree reported itself incomplete")
	}
	if got := observedPaths(observed); len(got) != 2 {
		t.Fatalf("observed %v", got)
	}
	for _, o := range observed {
		if o.Unchanged {
			t.Fatalf("a first sighting was reported as unchanged: %+v", o)
		}
		if o.Title == "" || o.ContentSHA256 == "" {
			t.Fatalf("a new book carried no metadata: %+v", o)
		}
	}

	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, complete = catalog.snapshot()
	if !complete || len(observed) != 2 {
		t.Fatalf("second pass: complete=%v observed=%d", complete, len(observed))
	}
	for _, o := range observed {
		if !o.Unchanged {
			t.Fatalf("an unchanged file was re-read: %+v", o)
		}
	}
}

// TestChangedBytesAtAPathAreANewBook is rule 4: content change is not
// identity transfer.
func TestChangedBytesAtAPathAreANewBook(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	catalog := newFakeCatalog()
	r := testReconciler(catalog)
	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}

	// A different publication copied over the same path.
	writeBook(t, folder.RootPath, "one.epub", "Something Else Entirely")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(folder.RootPath, "one.epub"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, _ := catalog.snapshot()
	if len(observed) != 1 {
		t.Fatalf("observed %d books", len(observed))
	}
	if !observed[0].Replaces {
		t.Fatal("a file whose bytes changed was offered as the same book")
	}
	if observed[0].Title != "Something Else Entirely" {
		t.Fatalf("replacement metadata: %+v", observed[0])
	}
}

// TestASubdirectoryIsASeries. The directory tree is the organisation, so
// a folder of volumes becomes a shelf without anybody configuring one.
func TestASubdirectoryIsASeries(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "Discworld/01 - The Colour of Magic.epub", "The Colour of Magic")
	writeBook(t, folder.RootPath, "Discworld/03 - Equal Rites.epub", "Equal Rites")
	writeBook(t, folder.RootPath, "loose.epub", "Loose")
	catalog := newFakeCatalog()
	if _, err := testReconciler(catalog).Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, _ := catalog.snapshot()

	positions := map[string]float64{}
	for _, o := range observed {
		switch filepath.Dir(o.RelativePath) {
		case ".":
			if len(o.Series) != 0 {
				t.Fatalf("a file at the root was shelved in %v", o.Series)
			}
		default:
			if len(o.Series) != 1 || o.Series[0].Name != "Discworld" {
				t.Fatalf("series of %q: %+v", o.RelativePath, o.Series)
			}
			if o.Series[0].Position == nil {
				t.Fatalf("no position for %q", o.RelativePath)
			}
			positions[filepath.Base(o.RelativePath)] = *o.Series[0].Position
		}
	}
	// The number in the filename wins over sorted order: Equal Rites is
	// volume three even though it is the second file in the directory.
	if positions["01 - The Colour of Magic.epub"] != 1 ||
		positions["03 - Equal Rites.epub"] != 3 {
		t.Fatalf("series positions: %v", positions)
	}
}

// TestAVanishedRootConcludesNothing is rule 1 at its bluntest: a pass
// that could not look does not get an opinion.
func TestAVanishedRootConcludesNothing(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	catalog := newFakeCatalog()
	r := testReconciler(catalog)
	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	callsBefore, _, _ := catalog.snapshot()

	if err := os.RemoveAll(folder.RootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), folder); err == nil {
		t.Fatal("a pass over a root that is not there succeeded")
	}
	calls, _, _ := catalog.snapshot()
	if calls != callsBefore {
		t.Fatal("a pass over a missing root still wrote to the catalog")
	}
}

// TestAnEmptiedButReadableRootMarksNothingMissing is rule 2. An
// unmounted mount point is usually still readable and empty, and reading
// that as "every book was deleted" hides a whole catalog.
func TestAnEmptiedButReadableRootMarksNothingMissing(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	catalog := newFakeCatalog()
	r := testReconciler(catalog)
	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(folder.RootPath, "one.epub")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, complete := catalog.snapshot()
	if len(observed) != 0 {
		t.Fatalf("observed %v", observedPaths(observed))
	}
	// The reconciler reports honestly; refusing to act on it is the
	// store's half of the rule, covered in storetest.
	if !complete {
		t.Fatal("an emptied but readable root was reported as unread")
	}
}

// TestAnUnreadableBookDoesNotMakeThePassComplete is rule 1 for a single
// file: one book nobody can open is not a reason to forget the rest, but
// it does cost the pass its right to conclude anything is gone.
func TestAnUnreadableBookDoesNotMakeThePassComplete(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "good.epub", "Good")
	bad := filepath.Join(folder.RootPath, "bad.epub")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 file")
	}
	catalog := newFakeCatalog()
	if _, err := testReconciler(catalog).Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	_, observed, complete := catalog.snapshot()
	if complete {
		t.Fatal("a pass that could not read a file called itself complete")
	}
	if got := observedPaths(observed); len(got) != 1 || got[0] != "good.epub" {
		t.Fatalf("observed %v, want only the readable book", got)
	}
}

// TestWatcherReconcilesAFolderAddedAtRuntime. "Add a folder and the
// books show up" has to be true without a restart, which is the only
// reason the watcher has an Add method at all.
func TestWatcherReconcilesAFolderAddedAtRuntime(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	catalog := newFakeCatalog()
	w := NewWatcher(catalog, testReconciler(catalog), slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	catalog.addFolder(folder)
	w.Add(ctx, folder)
	waitForReconcile(t, catalog)

	_, observed, _ := catalog.snapshot()
	if got := observedPaths(observed); len(got) != 1 || got[0] != "one.epub" {
		t.Fatalf("observed %v after adding a folder", got)
	}

	// A file appearing under a watched root shows up without waiting for
	// the safety timer.
	writeBook(t, folder.RootPath, "two.epub", "Two")
	waitForObserved(t, catalog, 2)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestQueuedFolderRunsOnePassPerBurst covers the debounced queue: the
// channel every inotify notification lands in, and the ticker that turns
// a burst of them into one pass.
//
// It drives the queue directly, with notifications deliberately unhooked
// — which is also what a network mount looks like from here, since
// inotify reports nothing at all on NFS or SMB. What matters is the
// collapsing: copying a book in is many events, and a pass per event
// would read the same half-written file over and over.
func TestQueuedFolderRunsOnePassPerBurst(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	catalog := newFakeCatalog()
	catalog.addFolder(folder)
	w := NewWatcher(catalog, testReconciler(catalog), slog.New(slog.DiscardHandler))
	if w.notify != nil {
		_ = w.notify.Close()
		w.notify = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitForReconcile(t, catalog)

	// Nothing here can tell the server this happened.
	writeBook(t, folder.RootPath, "two.epub", "Two")
	w.events <- folder.ID
	waitForObserved(t, catalog, 2)

	// A burst collapses: a pass is idempotent, so three notifications
	// about one folder are one more pass, not a queue of them.
	w.events <- folder.ID
	w.events <- folder.ID
	waitForReconcile(t, catalog)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestAPassNeverWritesUnderAWatchedFolder is rule 3, enforced
// mechanically rather than by review. The tree is somebody else's
// books; the server reads it and leaves it exactly as it found it.
func TestAPassNeverWritesUnderAWatchedFolder(t *testing.T) {
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "one.epub", "One")
	writeBook(t, folder.RootPath, "Series/02 - Two.epub", "Two")
	if err := os.WriteFile(
		filepath.Join(folder.RootPath, "notes.txt"), []byte("mine"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, folder.RootPath)

	catalog := newFakeCatalog()
	r := testReconciler(catalog)
	ctx := context.Background()
	for range 2 {
		if _, err := r.Reconcile(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}

	// Everything that reaches a book's bytes, not just the pass: the
	// download path and the cover path open the same files.
	files := NewFiles(catalog)
	catalog.addFolder(folder)
	book := store.CatalogBook{
		ID: "b1", FolderID: folder.ID, RelativePath: "one.epub",
	}
	f, _, err := files.OpenBook(ctx, book)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := files.OpenBookCover(ctx, book); err == nil {
		t.Fatal("a book with no recorded cover offered one")
	}

	if after := snapshotTree(t, folder.RootPath); after != before {
		t.Fatalf("the watched tree changed:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
}

// snapshotTree renders everything about a tree that a write would
// disturb: the names, sizes and modification times of every entry, and
// of the root itself. Comparing two of these is how rule 3 is checked.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, "./ "+rootInfo.ModTime().UTC().Format(time.RFC3339Nano))
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		size := "dir"
		if !info.IsDir() {
			size = strconv.FormatInt(info.Size(), 10)
		}
		lines = append(lines, rel+" "+info.Mode().String()+" "+size+" "+
			info.ModTime().UTC().Format(time.RFC3339Nano))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func waitForReconcile(t *testing.T, catalog *fakeCatalog) {
	t.Helper()
	select {
	case <-catalog.changed:
	case <-time.After(10 * time.Second):
		t.Fatal("no reconcile within 10s")
	}
}

func waitForObserved(t *testing.T, catalog *fakeCatalog, want int) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		_, observed, _ := catalog.snapshot()
		if len(observed) >= want {
			return
		}
		select {
		case <-catalog.changed:
		case <-deadline:
			t.Fatalf("only %d books observed, want %d", len(observed), want)
		}
	}
}
