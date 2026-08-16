package workident

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

type backfillFixture struct {
	st     store.Store
	user   store.User
	folder store.Folder
	now    time.Time
	seqNo  int
	// observed accumulates every book the fixture has recorded. A
	// reconcile pass is the whole folder, so adding one book means
	// replaying the ones before it — a pass that observed only the new
	// file would mark every earlier book missing.
	observed []store.ObservedBook
	// ids maps the name a test gave a book to the id the store minted
	// for it. Reconcile owns book ids — a catalog row is a consequence
	// of a file, not something a caller names.
	ids map[string]string
}

func newBackfillFixture(t *testing.T) *backfillFixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := store.User{
		ID: "u-backfill", Name: "backfill", Argon2Hash: "x",
		Timezone: "UTC", CreatedAt: now,
	}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	folder := store.Folder{
		ID: "f-backfill", Name: "Backfill", RootPath: "/srv/books",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	return &backfillFixture{
		st: st, user: user, folder: folder, now: now,
		ids: map[string]string{},
	}
}

// observe describes one file without recording it, so that a caller can
// choose between one pass per book and one pass for a whole shelf.
func (f *backfillFixture) observe(name, title, author string) store.ObservedBook {
	f.seqNo++
	at := f.now.Add(time.Duration(f.seqNo) * time.Millisecond)
	obs := store.ObservedBook{
		RelativePath:     name + ".epub",
		SizeBytes:        int64(1000 + f.seqNo),
		MTime:            at,
		ContentSHA256:    fmt.Sprintf("%064x", f.seqNo),
		OriginalFilename: name + ".epub",
		MediaType:        "application/epub+zip",
		Title:            title,
	}
	if title == "" {
		// A book with neither a title nor a digest has nothing another
		// device could recognise it by. It is the one shape the
		// backfill has to refuse, so the fixture spells it with an
		// empty title.
		obs.ContentSHA256 = ""
	}
	if author != "" {
		obs.Contributors = []store.ObservedContributor{
			{Name: author, Role: "author", Position: 0},
		}
	}
	f.observed = append(f.observed, obs)
	return obs
}

// book records one book in the folder, the only way a book gets into the
// catalog now: one reconcile pass per call, which is what a watcher does
// when a file lands.
func (f *backfillFixture) book(t *testing.T, name, title, author string) string {
	t.Helper()
	obs := f.observe(name, title, author)
	f.reconcile(t)
	return f.idOf(t, name, obs.RelativePath)
}

// shelf records a whole library in a single pass, which is what a first
// scan of an existing folder is. Adding books one at a time would
// re-observe the whole list on every call, and the cost of that is
// quadratic — 200 books is 20,000 observations, which under the race
// detector is minutes rather than seconds.
//
// It also seeds the harder case for a cursor: one pass stamps every book
// it inserts with a single created_at, so a walk can only advance if it
// breaks the tie on the book id.
func (f *backfillFixture) shelf(t *testing.T, count int, author string) {
	t.Helper()
	names := make([]string, 0, count)
	for i := range count {
		name := fmt.Sprintf("b%03d", i)
		f.observe(name, fmt.Sprintf("Title %03d", i), author)
		names = append(names, name)
	}
	f.reconcile(t)
	known, err := f.st.BooksInFolder(context.Background(), f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]string, len(known))
	for _, k := range known {
		byPath[k.RelativePath] = k.ID
	}
	for _, name := range names {
		id, ok := byPath[name+".epub"]
		if !ok {
			t.Fatalf("book %q was not recorded", name)
		}
		f.ids[name] = id
	}
}

func (f *backfillFixture) reconcile(t *testing.T) {
	t.Helper()
	at := f.now.Add(time.Duration(f.seqNo) * time.Millisecond)
	if _, err := f.st.ReconcileFolder(
		context.Background(), f.folder.ID, f.observed, true, at,
	); err != nil {
		t.Fatal(err)
	}
}

func (f *backfillFixture) idOf(t *testing.T, name, relativePath string) string {
	t.Helper()
	known, err := f.st.BooksInFolder(context.Background(), f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range known {
		if k.RelativePath == relativePath {
			f.ids[name] = k.ID
			return k.ID
		}
	}
	t.Fatalf("book %q was not recorded", name)
	return ""
}

// id is the store-minted id of a book a test named earlier.
func (f *backfillFixture) id(t *testing.T, name string) string {
	t.Helper()
	id, ok := f.ids[name]
	if !ok {
		t.Fatalf("no book named %q", name)
	}
	return id
}

func (f *backfillFixture) run(t *testing.T) Report {
	t.Helper()
	ids := 0
	report, err := Backfill(context.Background(), f.st, f.user.ID, func() (string, error) {
		ids++
		return fmt.Sprintf("work-%03d", ids), nil
	}, func() time.Time { return f.now })
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	return report
}

// TestBackfillMapsEveryBook: the mapping is created lazily on first
// resolve, so a folder that has just been scanned reports no statistics
// at all until every book has been opened. The backfill is what makes
// those books countable without a reader visiting each one.
func TestBackfillMapsEveryBook(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	report := f.run(t)
	if report.Books != 2 || report.Created != 2 {
		t.Fatalf("report = %+v, want 2 books and 2 creations", report)
	}
	for _, name := range []string{"b1", "b2"} {
		id := f.id(t, name)
		mapping, err := f.st.UserBookWork(context.Background(), f.user.ID, id)
		if err != nil {
			t.Fatalf("%s not mapped: %v", id, err)
		}
		if mapping.WorkID == "" {
			t.Fatalf("%s mapped to no work", id)
		}
	}
}

// TestBackfillIsIdempotent: an operator who runs it twice, or who adds a
// book and re-runs it, must not end up with a second work per book. The
// second pass links to what the first created.
func TestBackfillIsIdempotent(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	first := f.run(t)

	second := f.run(t)
	if second.Created != 0 || second.Linked != 1 {
		t.Fatalf("second run = %+v, want nothing created and one link", second)
	}
	works, err := f.st.ListWorks(context.Background(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 {
		t.Fatalf("second run left %d works, want 1 (first run: %+v)", len(works), first)
	}
}

// TestBackfillLeavesFuzzyMatchesUnmapped: two books that share a title and
// author may be the same edition or may be a reissue with different
// content. Only a reader can say. Mapping them together unasked would
// merge two reading histories on a guess, which is exactly what ADR-0003
// forbids, so the backfill counts them and moves on.
func TestBackfillLeavesFuzzyMatchesUnmapped(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "  dune  ", "Frank Herbert")

	report := f.run(t)
	if report.Created != 1 || report.Fuzzy != 1 {
		t.Fatalf("report = %+v, want one creation and one fuzzy match", report)
	}
	if report.Linked != 0 {
		t.Fatalf("a fuzzy match was counted as linked: %+v", report)
	}
	if _, err := f.st.UserBookWork(context.Background(), f.user.ID, f.id(t, "b2")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("fuzzy match was mapped anyway: %v", err)
	}
}

// TestBackfillSkipsBooksWithNoIdentifiers: a book with no digest, no
// embedded identifier and no title has nothing any other device could
// recognise it by. Resolving it would mint a work reachable from nowhere.
func TestBackfillSkipsBooksWithNoIdentifiers(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "", "")

	report := f.run(t)
	if report.Books != 1 || report.Skipped != 1 || report.Created != 0 {
		t.Fatalf("report = %+v, want the untitled book skipped", report)
	}
	works, err := f.st.ListWorks(context.Background(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("skipped book still created %d works", len(works))
	}
}

// TestBackfillPagesPastOneQuery: the walk reads a page at a time, so a
// library larger than one page has to advance its cursor. Getting that
// wrong either stops at the first page or loops on it forever.
func TestBackfillPagesPastOneQuery(t *testing.T) {
	f := newBackfillFixture(t)
	const total = backfillPage + 7
	f.shelf(t, total, "Ada Author")

	report := f.run(t)
	if report.Books != total || report.Created != total {
		t.Fatalf("report = %+v, want %d books", report, total)
	}
}

// TestBackfillMapsPerUser: the catalog is shared — every logged-in user
// sees every folder — but a work mapping is not. Two users backfilling
// the same folder each get their own works, which is what keeps a shared
// shelf from also sharing what its readers read.
func TestBackfillMapsPerUser(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	ctx := context.Background()

	reader := store.User{
		ID: "u-reader", Name: "reader", Argon2Hash: "x",
		Timezone: "UTC", CreatedAt: f.now,
	}
	if err := f.st.CreateUser(ctx, reader); err != nil {
		t.Fatal(err)
	}

	owner := f.run(t)
	if owner.Books != 1 || owner.Created != 1 {
		t.Fatalf("owner report = %+v, want the book mapped", owner)
	}
	report, err := Backfill(ctx, f.st, reader.ID,
		func() (string, error) { return "work-reader", nil },
		func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	if report.Books != 1 || report.Created != 1 {
		t.Fatalf("reader report = %+v, want the shared book mapped", report)
	}
	ownerWork, err := f.st.UserBookWork(ctx, f.user.ID, f.id(t, "b1"))
	if err != nil {
		t.Fatal(err)
	}
	readerWork, err := f.st.UserBookWork(ctx, reader.ID, f.id(t, "b1"))
	if err != nil {
		t.Fatal(err)
	}
	if ownerWork.WorkID == readerWork.WorkID {
		t.Fatal("two readers of one shelf were given the same work")
	}
}

// refusingStore fails the resolution itself. Both outcomes are about one
// book — it went away, or its identifiers cannot be reconciled — so
// neither is a reason to abandon the rest of the catalog.
type refusingStore struct {
	store.Store
	err error
}

func (rs refusingStore) ResolveCatalogBookWork(
	ctx context.Context, userID, bookID string, proposed store.Work,
	editions []store.Edition, ids []store.Identifier, confirmed bool, at time.Time,
) (store.WorkResolution, error) {
	return store.WorkResolution{}, rs.err
}

func TestBackfillContinuesPastAResolutionItCannotMake(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want func(Report) bool
	}{
		{"gone", store.ErrNotFound, func(r Report) bool { return r.Skipped == 2 }},
		{"conflict", store.ErrConflict, func(r Report) bool { return r.Conflicted == 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBackfillFixture(t)
			f.book(t, "b1", "Dune", "Frank Herbert")
			f.book(t, "b2", "Neuromancer", "William Gibson")

			report, err := Backfill(context.Background(),
				refusingStore{Store: f.st, err: tc.err}, f.user.ID,
				func() (string, error) { return "work-1", nil },
				func() time.Time { return f.now })
			if err != nil {
				t.Fatalf("run stopped: %v", err)
			}
			if report.Books != 2 || !tc.want(report) {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

// vanishingStore is a book whose file disappeared between being listed
// and being read. A backfill over a live server will hit this, and stopping the
// whole run because one book went away would be the wrong trade.
type vanishingStore struct {
	store.Store
	gone string
}

func (v vanishingStore) CatalogBookIdentifiers(
	ctx context.Context, bookID string,
) ([]store.BookIdentifier, error) {
	if bookID == v.gone {
		return nil, store.ErrNotFound
	}
	return v.Store.CatalogBookIdentifiers(ctx, bookID)
}

func TestBackfillContinuesPastABookThatVanished(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	ids := 0
	report, err := Backfill(context.Background(),
		vanishingStore{Store: f.st, gone: f.id(t, "b1")}, f.user.ID,
		func() (string, error) { ids++; return fmt.Sprintf("work-%d", ids), nil },
		func() time.Time { return f.now })
	if err != nil {
		t.Fatalf("one missing book stopped the run: %v", err)
	}
	if report.Books != 2 || report.Skipped != 1 || report.Created != 1 {
		t.Fatalf("report = %+v, want the vanished book skipped and the other mapped", report)
	}
}

// failingStore turns a real failure into a stop. A store that is refusing
// reads will refuse the next book too, so continuing would produce a
// report claiming a successful pass over a catalog it never read.
type failingStore struct {
	store.Store
	err error
}

func (fs failingStore) CatalogBookIdentifiers(
	ctx context.Context, bookID string,
) ([]store.BookIdentifier, error) {
	return nil, fs.err
}

func TestBackfillStopsOnAStoreFailure(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	boom := errors.New("disk on fire")
	report, err := Backfill(context.Background(),
		failingStore{Store: f.st, err: boom}, f.user.ID,
		func() (string, error) { return "work-1", nil },
		func() time.Time { return f.now })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store failure", err)
	}
	if report.Created != 0 {
		t.Fatalf("failed run still reported creations: %+v", report)
	}
}

// stuckStore is a store whose listing ignores the cursor it was given,
// which is what a paging bug looks like from here: every page is the
// first page.
type stuckStore struct {
	store.Store
}

func (ss stuckStore) ListCatalogBooks(
	ctx context.Context, folderID string, _ *store.CatalogBookCursor, limit int,
) ([]store.CatalogBook, error) {
	return ss.Store.ListCatalogBooks(ctx, folderID, nil, limit)
}

func (ss stuckStore) ListFolders(
	ctx context.Context, _ string, limit int,
) ([]store.Folder, error) {
	return ss.Store.ListFolders(ctx, "", limit)
}

// stuckFolderStore is the same bug one level up: a full page of folders
// that is always the same page. The book walk cannot reach this, because
// it only runs once per folder; the folder cursor has to catch it.
type stuckFolderStore struct {
	store.Store
}

func (ss stuckFolderStore) ListFolders(
	ctx context.Context, _ string, _ int,
) ([]store.Folder, error) {
	folders := make([]store.Folder, backfillPage)
	for i := range folders {
		folders[i] = store.Folder{
			ID: fmt.Sprintf("f%03d", i), Name: fmt.Sprintf("Folder %03d", i),
			Kind: store.FolderPlain,
		}
	}
	return folders, nil
}

func (stuckFolderStore) ListCatalogBooks(
	context.Context, string, *store.CatalogBookCursor, int,
) ([]store.CatalogBook, error) {
	return nil, nil
}

func TestBackfillStopsWhenTheFolderCursorDoesNot(t *testing.T) {
	f := newBackfillFixture(t)

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = Backfill(context.Background(),
			stuckFolderStore{Store: f.st}, f.user.ID,
			func() (string, error) { return "work-1", nil },
			func() time.Time { return f.now })
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the backfill is still walking a folder cursor that never moves")
	}
	if !errors.Is(err, ErrCursorStalled) {
		t.Fatalf("err = %v, want ErrCursorStalled", err)
	}
}

// TestBackfillStopsWhenACursorDoesNot: a walk whose cursor stops moving
// reads the same page forever. Nothing the job can do fixes that, and a
// background task that spins is worse than one that stops and says why,
// so it is an error rather than a loop.
func TestBackfillStopsWhenACursorDoesNot(t *testing.T) {
	f := newBackfillFixture(t)
	f.shelf(t, backfillPage+3, "Ada Author")

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = Backfill(context.Background(),
			stuckStore{Store: f.st}, f.user.ID,
			func() (string, error) { return "work-1", nil },
			func() time.Time { return f.now })
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the backfill is still walking a cursor that never moves")
	}
	if !errors.Is(err, ErrCursorStalled) {
		t.Fatalf("err = %v, want ErrCursorStalled", err)
	}
}

// conflictingStore stands in for identifiers that name two different
// works. The store refuses to guess which one is right; the backfill has
// to record that and keep going rather than abandon the catalog.
type conflictingStore struct {
	store.Store
}

func (conflictingStore) ResolveCatalogBookWork(
	ctx context.Context, userID, bookID string, proposed store.Work,
	editions []store.Edition, ids []store.Identifier, confirmed bool, at time.Time,
) (store.WorkResolution, error) {
	return store.WorkResolution{ConflictingWorkIDs: []string{"w1", "w2"}}, nil
}

func TestBackfillRecordsConflictsAndContinues(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	report, err := Backfill(context.Background(),
		conflictingStore{Store: f.st}, f.user.ID,
		func() (string, error) { return "work-1", nil },
		func() time.Time { return f.now })
	if err != nil {
		t.Fatalf("a conflict stopped the run: %v", err)
	}
	if report.Conflicted != 2 || report.Created != 0 {
		t.Fatalf("report = %+v, want both books conflicted", report)
	}
}

// TestBackfillReportsAFailureItCannotFinish: an id generator that fails
// cannot be worked around, and a partial report is still worth printing.
func TestBackfillFailsWhenIDsCannotBeMinted(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")

	boom := errors.New("no entropy")
	if _, err := Backfill(context.Background(), f.st, f.user.ID,
		func() (string, error) { return "", boom },
		func() time.Time { return f.now }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the id failure", err)
	}
}
