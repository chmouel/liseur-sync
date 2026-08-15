//go:build linux

package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// inPlaceFixture is the watched fixture's twin for a library the server
// reads but does not own: same store, same CAS, same root, and a library
// whose storage mode says the bytes stay where they are.
type inPlaceFixture struct {
	*watchedFixture
}

func newInPlaceFixture(t *testing.T) *inPlaceFixture {
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
	user := storetest.MkUser(t, st, "reader")
	root := filepath.Join(dir, "calibre")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	library := store.Library{
		ID: "lib-in-place", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, Name: "Somebody else's shelf",
		RootPath: &root, CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatalf("create library: %v", err)
	}
	return &inPlaceFixture{watchedFixture: &watchedFixture{
		t: t, ctx: ctx, store: st, cas: cas,
		root: root, library: library, now: now,
	}}
}

func (f *inPlaceFixture) sync() WatchedSyncReport {
	f.t.Helper()
	report, err := SyncScannedLibrary(f.ctx, f.store, f.cas, ScannedLibrary{
		ID:          f.library.ID,
		Storage:     store.LibraryStorageInPlace,
		RootPath:    f.root,
		ActorUserID: f.library.OwnerUserID,
	}, WatchedSyncOptions{
		MaxFileBytes: 1 << 20,
		Patterns:     NewLibraryPatterns(f.store),
	}, f.clock())
	if err != nil {
		f.t.Fatalf("sync: %v", err)
	}
	return report
}

// contentTreeEntries counts what the CAS holds, which for an in-place
// library must be nothing at all.
func (f *inPlaceFixture) contentTreeEntries() int {
	f.t.Helper()
	count := 0
	err := filepath.WalkDir(filepath.Join(f.cas.Root(), "sha256"),
		func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				count++
			}
			return nil
		})
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatalf("walk content tree: %v", err)
	}
	return count
}

// The whole promise of an in-place library: the catalog gains a book, the
// disk gains nothing, and the quota is not charged for bytes the server
// does not store.
func TestInPlaceSweepPublishesWithoutCopying(t *testing.T) {
	f := newInPlaceFixture(t)
	f.write("Ursula Le Guin/A Wizard of Earthsea.epub",
		variantEPUB(t, "A Wizard of Earthsea"))

	report := f.sync()
	if report.Ingested != 1 {
		t.Fatalf("ingested %d, want 1 (report %+v)", report.Ingested, report)
	}

	books := f.books()
	if len(books) != 1 {
		t.Fatalf("catalog holds %d books, want 1", len(books))
	}
	if books[0].Title != "A Wizard of Earthsea" {
		t.Errorf("title %q, want the publication's own", books[0].Title)
	}

	if n := f.contentTreeEntries(); n != 0 {
		t.Errorf("the content store holds %d files, want none", n)
	}
	counts, err := f.store.AdminCounts(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Blobs != 0 || counts.BlobBytes != 0 || counts.BlobsPending != 0 {
		t.Errorf("the server accounts for bytes it does not store: %+v", counts)
	}

	files, err := f.store.ListBookFiles(f.ctx, f.library.OwnerUserID,
		books[0].ID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	file := files[0]
	if file.Storage != store.LibraryStorageInPlace {
		t.Errorf("storage %q, want in_place", file.Storage)
	}
	if file.BlobSHA256 != "" {
		t.Errorf("an in-place file claims a CAS copy: %q", file.BlobSHA256)
	}
	if file.ContentSHA256 == "" || file.ContentSizeBytes == 0 ||
		file.SourceModifiedAt == nil {
		t.Errorf("the file is missing the snapshot that proves it: %+v", file)
	}
	if file.LibraryRoot != f.root {
		t.Errorf("library root %q, want %q", file.LibraryRoot, f.root)
	}

	// And it reads back, byte for byte, through the same chokepoint a
	// download uses.
	opened, size, err := f.cas.OpenBookFile(f.ctx, file)
	if err != nil {
		t.Fatalf("open in-place book: %v", err)
	}
	defer opened.Close()
	if size != file.ContentSizeBytes {
		t.Errorf("served %d bytes, catalogued %d", size, file.ContentSizeBytes)
	}
}

// A second sweep over an unchanged library is not a second catalog.
func TestInPlaceSweepIsIdempotent(t *testing.T) {
	f := newInPlaceFixture(t)
	f.write("book.epub", variantEPUB(t, "Only Once"))

	f.sync()
	second := f.sync()
	if second.Ingested != 0 || second.Unchanged != 1 {
		t.Fatalf("second sweep: %+v, want nothing ingested and one unchanged",
			second)
	}
	if books := f.books(); len(books) != 1 {
		t.Fatalf("catalog holds %d books after two sweeps, want 1", len(books))
	}
}

// Nothing under the root is written, renamed or removed — not by the
// sweep, and not by deleting the book it produced.
func TestInPlaceSweepNeverTouchesTheRoot(t *testing.T) {
	f := newInPlaceFixture(t)
	f.write("keep/me.epub", variantEPUB(t, "Untouched"))
	full := filepath.Join(f.root, "keep", "me.epub")
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	f.sync()

	after, err := os.Stat(full)
	if err != nil {
		t.Fatalf("the sweep lost the file: %v", err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the sweep modified the source file")
	}

	books := f.books()
	if _, err := f.store.TrashCatalogBook(f.ctx, f.library.OwnerUserID,
		books[0].ID, f.clock()(), f.now.Add(time.Hour)); err != nil {
		t.Fatalf("trash book: %v", err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("trashing the book disturbed somebody else's file: %v", err)
	}
}

// A file that is not a publication is refused, and refusing it says so
// rather than failing the whole library's sweep.
func TestInPlaceSweepQuarantinesAnUnreadableFile(t *testing.T) {
	f := newInPlaceFixture(t)
	f.write("broken.epub", []byte("this is not a zip archive"))

	report := f.sync()
	if report.Ingested != 0 || report.Refused != 1 {
		t.Fatalf("report %+v, want the path counted as refused, not ingested",
			report)
	}
	if books := f.books(); len(books) != 0 {
		t.Fatalf("catalog holds %d books, want none", len(books))
	}
	jobs, err := f.store.ListIngestJobs(
		f.ctx, f.library.OwnerUserID, f.library.ID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("library holds %d jobs, want 1", len(jobs))
	}
	if jobs[0].State != store.IngestQuarantined {
		t.Errorf("job state %q, want quarantined", jobs[0].State)
	}
	if jobs[0].Storage != store.LibraryStorageInPlace {
		t.Errorf("job storage %q, want in_place", jobs[0].Storage)
	}
	if jobs[0].StagingPath != nil {
		t.Errorf("an in-place job claims a staged artifact: %q",
			*jobs[0].StagingPath)
	}
}

// Availability for a book the server does not store is a statement about
// somebody else's directory: the file is gone when a completed sweep did
// not find it, and back when a later one did.
func TestInPlaceAvailabilityFollowsTheSourceFile(t *testing.T) {
	f := newInPlaceFixture(t)
	body := variantEPUB(t, "Here Then Gone")
	f.write("gone.epub", body)
	f.sync()

	books := f.books()
	if len(books) != 1 {
		t.Fatalf("catalog holds %d books, want 1", len(books))
	}
	bookID := books[0].ID

	if err := os.Remove(filepath.Join(f.root, "gone.epub")); err != nil {
		t.Fatal(err)
	}
	report := f.sync()
	if report.MarkedAbsent != 1 {
		t.Fatalf("sweep marked %d absent, want 1 (%+v)",
			report.MarkedAbsent, report)
	}
	f.reconcileAvailability()

	files, err := f.store.ListBookFiles(
		f.ctx, f.library.OwnerUserID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Availability != store.BookFileMissing {
		t.Errorf("availability %q, want missing", files[0].Availability)
	}
	if got := f.books()[0].Status; got != store.BookMissing {
		t.Errorf("book status %q, want missing", got)
	}

	// The same bytes back at the same path are the same book again, not a
	// second one.
	f.write("gone.epub", body)
	f.sync()
	f.reconcileAvailability()

	if got := f.books()[0].Status; got != store.BookActive {
		t.Errorf("book status %q after the file came back, want active", got)
	}
	if len(f.books()) != 1 {
		t.Errorf("the returning file made a second book")
	}
}
