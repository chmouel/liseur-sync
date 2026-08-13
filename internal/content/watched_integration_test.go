//go:build linux

package content

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// watchedFixture is one server: a real store, a real CAS, and a real
// watched root. The unit-level fakes elsewhere can only be as strict as
// their author guessed, and every interesting rule here — that a changed
// path does not inherit a book, that an incomplete sweep changes nothing —
// is enforced by the backend and the filesystem rather than by this
// package.
type watchedFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *sqlite.Store
	cas     *CAS
	root    string
	library store.Library
	now     time.Time
}

func newWatchedFixture(t *testing.T) *watchedFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cas, err := Open(filepath.Join(dir, "content"))
	if err != nil {
		t.Fatalf("open cas: %v", err)
	}
	t.Cleanup(func() { cas.Close() })
	st, err := sqlite.Open(filepath.Join(dir, "liseur.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	user := storetest.MkUser(t, st, "watcher")
	root := filepath.Join(dir, "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	library := store.Library{
		ID: "lib-watched", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryWatched, Name: "Shelf",
		RootPath: &root, CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatalf("create library: %v", err)
	}
	return &watchedFixture{
		t: t, ctx: ctx, store: st, cas: cas,
		root: root, library: library, now: now,
	}
}

// clock advances a little each call, because every durable transition
// refuses a timestamp that moves backwards.
func (f *watchedFixture) clock() func() time.Time {
	return func() time.Time {
		f.now = f.now.Add(time.Second)
		return f.now
	}
}

func (f *watchedFixture) sync() WatchedSyncReport {
	f.t.Helper()
	report, err := SyncWatchedLibrary(f.ctx, f.store, f.cas, WatchedLibrary{
		ID:          f.library.ID,
		RootPath:    f.root,
		ActorUserID: f.library.OwnerUserID,
	}, WatchedSyncOptions{MaxFileBytes: 1 << 20}, f.clock())
	if err != nil {
		f.t.Fatalf("sync: %v", err)
	}
	return report
}

// drain runs the ingest workers to completion, exactly as the server's
// background goroutines do, so a test can assert on catalog books rather
// than on jobs.
func (f *watchedFixture) drain() {
	f.t.Helper()
	retention := time.Hour
	for i := 0; i < 20; i++ {
		validated, err := RunIngestValidationPass(f.ctx, f.store, f.cas,
			f.clock(), retention, epub.DefaultLimits(), 100)
		if err != nil {
			f.t.Fatalf("validation pass: %v", err)
		}
		extracted, err := RunIngestMetadataExtractionPass(f.ctx, f.store,
			f.cas, f.clock(), retention, epub.DefaultLimits(), 100)
		if err != nil {
			f.t.Fatalf("extraction pass: %v", err)
		}
		promoted, err := RunIngestPromotionPass(f.ctx, f.store, f.cas,
			NewLibraryPatterns(f.store), f.clock(), retention, 100)
		if err != nil {
			f.t.Fatalf("promotion pass: %v", err)
		}
		if validated.Validated == 0 && extracted.Extracted == 0 &&
			promoted.Promoted == 0 {
			return
		}
	}
	f.t.Fatal("ingest workers did not settle")
}

func (f *watchedFixture) reconcileAvailability() {
	f.t.Helper()
	if _, err := ReconcileCatalogAvailability(
		f.ctx, f.store, f.clock()(), 100); err != nil {
		f.t.Fatalf("reconcile availability: %v", err)
	}
}

func (f *watchedFixture) books() []store.CatalogBook {
	f.t.Helper()
	books, err := f.store.ListCatalogBooks(
		f.ctx, f.library.OwnerUserID, f.library.ID, nil, 100)
	if err != nil {
		f.t.Fatalf("list books: %v", err)
	}
	return books
}

func (f *watchedFixture) bookAt(rel string) store.WatchedFile {
	f.t.Helper()
	files, err := f.store.WatchedFilesByPath(f.ctx, f.library.ID, rel)
	if err != nil {
		f.t.Fatalf("watched files at %q: %v", rel, err)
	}
	if len(files) != 1 {
		f.t.Fatalf("expected exactly one file at %q, got %d", rel, len(files))
	}
	return files[0]
}

func (f *watchedFixture) write(rel string, body []byte) {
	f.t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		f.t.Fatalf("write %s: %v", rel, err)
	}
}

// variantEPUB produces a valid publication whose bytes differ per marker,
// so two files are genuinely different publications rather than copies.
// The marker goes inside the package document: appending to the archive
// instead would produce bytes the validator rightly refuses, and the tests
// here are about content changes, not corruption.
func variantEPUB(t *testing.T, marker string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	add := func(name, body string, method uint16) {
		t.Helper()
		target, err := writer.CreateHeader(
			&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml",
		`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
			`<rootfiles><rootfile full-path="OPS/book.opf"`+
			` media-type="application/oebps-package+xml"/>`+
			`</rootfiles></container>`,
		zip.Deflate)
	add("OPS/book.opf",
		`<package xmlns="http://www.idpf.org/2007/opf">`+
			`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`+
			`<dc:title>`+marker+`</dc:title></metadata>`+
			`<manifest/></package>`,
		zip.Deflate)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// A file dropped into a watched root becomes a book, through the same
// pipeline an upload uses. Nothing about the source is served: the
// download reads the CAS snapshot.
func TestWatchedSweepIngestsDiscoveredPublications(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("Ada Lovelace/Notes.epub", minimalEPUB(t))

	report := f.sync()
	if report.Ingested != 1 || !report.Scan.Complete {
		t.Fatalf("first sweep: %+v", report)
	}
	f.drain()

	books := f.books()
	if len(books) != 1 {
		t.Fatalf("expected one book, got %d", len(books))
	}
	if books[0].Status != store.BookActive {
		t.Fatalf("expected an active book, got %q", books[0].Status)
	}
	file := f.bookAt("Ada Lovelace/Notes.epub")
	if file.Availability != store.BookFileAvailable || file.SourceAbsent {
		t.Fatalf("unexpected file state: %+v", file)
	}
	if file.BlobSHA256 == "" {
		t.Fatal("a watched book must reference a CAS snapshot")
	}
}

// A sweep over an unchanged library must settle: no new job, no new book,
// and eventually no read of the file's bytes either.
//
// A file's row is created by promotion, which happens after the sweep that
// discovered it has already recorded its observations, so a newly promoted
// row carries no modification time. The following sweep reads it once to
// establish one. That is deliberate: the alternative is to assume a file
// whose size has not changed has not changed, which would miss a
// same-length replacement — exactly the case the review rule exists for.
func TestWatchedSweepSettlesAfterOneRehash(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("book.epub", minimalEPUB(t))
	f.sync()
	f.drain()

	first := f.sync()
	if first.Ingested != 0 || first.Review != 0 || first.Rehashed != 1 {
		t.Fatalf("expected exactly one establishing rehash: %+v", first)
	}
	second := f.sync()
	if second.Ingested != 0 || second.Unchanged != 1 ||
		second.Rehashed != 0 || second.Review != 0 ||
		second.MarkedAbsent != 0 {
		t.Fatalf("a settled sweep did work it did not need to: %+v", second)
	}
	if len(f.books()) != 1 {
		t.Fatalf("expected still one book, got %d", len(f.books()))
	}
}

// Replacing a watched path with an unrelated publication must not let the
// new content inherit the old book's identity. The existing snapshot is
// kept and the book goes to review.
func TestWatchedChangedPathNeverInheritsTheBook(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("book.epub", variantEPUB(t, "original"))
	f.sync()
	f.drain()

	original := f.bookAt("book.epub")

	f.write("book.epub", variantEPUB(t, "an entirely different publication"))
	report := f.sync()
	if report.Review != 1 {
		t.Fatalf("expected the change to be sent to review: %+v", report)
	}
	if report.Ingested != 0 {
		t.Fatal("a changed watched path must not be silently re-ingested")
	}

	after := f.bookAt("book.epub")
	if after.BookID != original.BookID {
		t.Fatal("the file row was rebound to a different book")
	}
	if after.BlobSHA256 != original.BlobSHA256 {
		t.Fatalf("the existing snapshot was replaced: %q became %q",
			original.BlobSHA256, after.BlobSHA256)
	}
	books, err := f.store.ListBooksInReview(
		f.ctx, f.library.OwnerUserID, f.library.ID, 10)
	if err != nil {
		t.Fatalf("list review: %v", err)
	}
	if len(books) != 1 || books[0].ID != original.BookID {
		t.Fatalf("expected the original book in review, got %+v", books)
	}
	if books[0].ReviewReason == "" {
		t.Fatal("a review item that does not say why is not a review item")
	}
}

// Touching a file — same bytes, new modification time — is not a catalog
// event. The sweep rehashes once to find that out and then leaves
// everything alone.
func TestWatchedTouchedFileIsNotAChange(t *testing.T) {
	f := newWatchedFixture(t)
	body := minimalEPUB(t)
	f.write("book.epub", body)
	f.sync()
	f.drain()
	before := f.bookAt("book.epub")

	later := time.Now().Add(2 * time.Hour)
	full := filepath.Join(f.root, "book.epub")
	if err := os.Chtimes(full, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	report := f.sync()
	if report.Rehashed != 1 || report.Review != 0 || report.Ingested != 0 {
		t.Fatalf("a touched file was treated as a change: %+v", report)
	}
	if after := f.bookAt("book.epub"); after.BookID != before.BookID ||
		after.BlobSHA256 != before.BlobSHA256 {
		t.Fatalf("a touched file changed the catalog: %+v -> %+v", before, after)
	}
	// The new modification time is recorded, so the next sweep is free.
	settled := f.sync()
	if settled.Rehashed != 0 || settled.Unchanged != 1 {
		t.Fatalf("the rehash was not remembered: %+v", settled)
	}
}

// A completed sweep that no longer finds a path marks its book missing.
// The snapshot stays in the CAS — it is still a GC root and still charged
// — but a book the library no longer contains is not offered.
func TestWatchedCompletedSweepMarksDeletedFilesMissing(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("gone.epub", variantEPUB(t, "gone"))
	f.write("stays.epub", variantEPUB(t, "stays"))
	f.sync()
	f.drain()

	if err := os.Remove(filepath.Join(f.root, "gone.epub")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	report := f.sync()
	if !report.Scan.Complete {
		t.Fatalf("expected a complete sweep: %+v", report)
	}
	if report.MarkedAbsent != 1 {
		t.Fatalf("expected one file marked absent, got %+v", report)
	}
	f.reconcileAvailability()

	byStatus := map[store.BookStatus]int{}
	for _, book := range f.books() {
		byStatus[book.Status]++
	}
	if byStatus[store.BookMissing] != 1 || byStatus[store.BookActive] != 1 {
		t.Fatalf("unexpected statuses: %v", byStatus)
	}
	if file := f.bookAt("gone.epub"); !file.SourceAbsent ||
		file.Availability != store.BookFileMissing {
		t.Fatalf("the removed path is still being offered: %+v", file)
	}
}

// The same file returning restores it. Availability is reversible, which
// is what makes an unmounted disk survivable.
func TestWatchedReturningFileRestoresAvailability(t *testing.T) {
	f := newWatchedFixture(t)
	body := variantEPUB(t, "comes back")
	f.write("book.epub", body)
	f.sync()
	f.drain()

	if err := os.Remove(filepath.Join(f.root, "book.epub")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	f.sync()
	f.reconcileAvailability()
	if f.books()[0].Status != store.BookMissing {
		t.Fatalf("expected the book to go missing first")
	}

	f.write("book.epub", body)
	report := f.sync()
	if report.MarkedAbsent != 0 {
		t.Fatalf("a returning file was marked absent: %+v", report)
	}
	f.reconcileAvailability()
	if status := f.books()[0].Status; status != store.BookActive {
		t.Fatalf("expected the book back, got %q", status)
	}
	if file := f.bookAt("book.epub"); file.SourceAbsent ||
		file.Availability != store.BookFileAvailable {
		t.Fatalf("the returned file is not servable: %+v", file)
	}
}

// The rule the whole design turns on: an incomplete traversal may not
// conclude anything is gone. A sweep bounded below the library's size
// leaves every book exactly as it found it.
func TestWatchedIncompleteSweepMarksNothingMissing(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("a.epub", variantEPUB(t, "a"))
	f.write("b.epub", variantEPUB(t, "b"))
	f.write("c.epub", variantEPUB(t, "c"))
	f.sync()
	f.drain()

	if err := os.Remove(filepath.Join(f.root, "c.epub")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	report, err := SyncWatchedLibrary(f.ctx, f.store, f.cas, WatchedLibrary{
		ID:          f.library.ID,
		RootPath:    f.root,
		ActorUserID: f.library.OwnerUserID,
	}, WatchedSyncOptions{
		MaxFileBytes: 1 << 20,
		Scan:         ScanLimits{MaxFiles: 1},
	}, f.clock())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.Scan.Complete {
		t.Fatal("a bounded sweep claimed to be complete")
	}
	if report.MarkedAbsent != 0 {
		t.Fatalf("an incomplete sweep marked %d files absent", report.MarkedAbsent)
	}
	f.reconcileAvailability()
	for _, book := range f.books() {
		if book.Status != store.BookActive {
			t.Fatalf("an incomplete sweep changed a book to %q", book.Status)
		}
	}
}

// A root that cannot be opened is not a sweep that found nothing. An
// unmounted volume must not empty a household's catalog.
func TestWatchedUnavailableRootChangesNothing(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("book.epub", minimalEPUB(t))
	f.sync()
	f.drain()

	moved := f.root + "-unmounted"
	if err := os.Rename(f.root, moved); err != nil {
		t.Fatalf("rename root: %v", err)
	}
	_, err := SyncWatchedLibrary(f.ctx, f.store, f.cas, WatchedLibrary{
		ID:          f.library.ID,
		RootPath:    f.root,
		ActorUserID: f.library.OwnerUserID,
	}, WatchedSyncOptions{MaxFileBytes: 1 << 20}, f.clock())
	if err == nil {
		t.Fatal("expected a missing root to be reported")
	}
	f.reconcileAvailability()
	if status := f.books()[0].Status; status != store.BookActive {
		t.Fatalf("an unavailable root took the catalog away: %q", status)
	}
	if file := f.bookAt("book.epub"); file.SourceAbsent {
		t.Fatal("an unavailable root marked a file absent")
	}
}

// Two identical files at two live paths are two catalog references that
// happen to deduplicate onto one blob. Hash equality is never a rename.
func TestWatchedIdenticalFilesAtTwoPathsStayDistinct(t *testing.T) {
	f := newWatchedFixture(t)
	body := minimalEPUB(t)
	f.write("shelf-a/twin.epub", body)
	f.write("shelf-b/twin.epub", body)

	report := f.sync()
	if report.Ingested != 2 {
		t.Fatalf("expected both paths queued, got %+v", report)
	}
	f.drain()

	if books := f.books(); len(books) != 2 {
		t.Fatalf("expected two catalog references, got %d", len(books))
	}
	left := f.bookAt("shelf-a/twin.epub")
	right := f.bookAt("shelf-b/twin.epub")
	if left.BookID == right.BookID {
		t.Fatal("two live paths were collapsed into one book")
	}
	if left.BlobSHA256 != right.BlobSHA256 {
		t.Fatal("identical bytes should deduplicate onto one blob")
	}
}

// A rename inside the tree is a path that went away and a path that
// arrived. The new one is a new book; the old one goes missing. Nothing
// pretends to know they are the same publication.
func TestWatchedRenameIsNotAnIdentityTransfer(t *testing.T) {
	f := newWatchedFixture(t)
	body := variantEPUB(t, "renamed")
	f.write("before.epub", body)
	f.sync()
	f.drain()
	before := f.bookAt("before.epub")

	if err := os.Rename(filepath.Join(f.root, "before.epub"),
		filepath.Join(f.root, "after.epub")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	report := f.sync()
	if report.Ingested != 1 || report.MarkedAbsent != 1 {
		t.Fatalf("expected one arrival and one departure: %+v", report)
	}
	f.drain()
	f.reconcileAvailability()

	after := f.bookAt("after.epub")
	if after.BookID == before.BookID {
		t.Fatal("a rename transferred a book id on hash equality alone")
	}
	if after.BlobSHA256 != before.BlobSHA256 {
		t.Fatal("the same bytes should still deduplicate")
	}
	if gone := f.bookAt("before.epub"); !gone.SourceAbsent {
		t.Fatal("the old path was not marked absent")
	}
}

// The sweep must never write below the root. This is the acceptance
// criterion, checked against a real ingest that copies bytes out of the
// tree.
func TestWatchedSweepNeverMutatesTheRoot(t *testing.T) {
	f := newWatchedFixture(t)
	f.write("a/book.epub", minimalEPUB(t))
	f.write("a/notes.txt", []byte("mine"))

	snapshot := func() map[string]string {
		seen := map[string]string{}
		err := filepath.Walk(f.root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(f.root, p)
			seen[rel] = info.Mode().String() + "|" +
				info.ModTime().UTC().String()
			return nil
		})
		if err != nil {
			t.Fatalf("walk: %v", err)
		}
		return seen
	}

	before := snapshot()
	f.sync()
	f.drain()
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("the sweep changed the tree: %v -> %v", before, after)
	}
	for name, was := range before {
		if after[name] != was {
			t.Fatalf("the sweep modified %q", name)
		}
	}
	// And the bytes it serves are the CAS snapshot, not the source.
	file := f.bookAt("a/book.epub")
	blob, size, err := f.cas.OpenBlob(f.ctx, file.BlobSHA256)
	if err != nil {
		t.Fatalf("open blob: %v", err)
	}
	defer blob.Close()
	body := make([]byte, size)
	if _, err := blob.Read(body); err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if !bytes.Equal(body, minimalEPUB(t)) {
		t.Fatal("the snapshot does not hold the discovered bytes")
	}
}

// A symlink inside a watched root is skipped, so it can never become a
// book — including one aimed at a file outside the root.
func TestWatchedSweepIgnoresSymlinkedContent(t *testing.T) {
	f := newWatchedFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.epub"),
		minimalEPUB(t), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "private.epub"),
		filepath.Join(f.root, "linked.epub")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	report := f.sync()
	if report.Ingested != 0 || report.Scan.Symlinks != 1 {
		t.Fatalf("a symlink was ingested: %+v", report)
	}
	f.drain()
	if books := f.books(); len(books) != 0 {
		t.Fatalf("expected no books, got %d", len(books))
	}
}
