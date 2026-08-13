package workident

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

type backfillFixture struct {
	st    store.Store
	user  store.User
	lib   store.Library
	now   time.Time
	seqNo int
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
	lib := store.Library{
		ID: "lib-backfill", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Backfill", CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	return &backfillFixture{st: st, user: user, lib: lib, now: now}
}

// book creates a catalog book. Timestamps step forward so that the paging
// cursor has a strict order to walk.
func (f *backfillFixture) book(t *testing.T, id, title, author string) store.CatalogBook {
	t.Helper()
	f.seqNo++
	book := store.CatalogBook{
		ID: id, LibraryID: f.lib.ID, Status: store.BookActive,
		Title: title, TitleSource: store.MetadataEmbedded,
		CreatedAt: f.now.Add(time.Duration(f.seqNo) * time.Millisecond),
	}
	if err := f.st.CreateCatalogBook(context.Background(), f.user.ID, book); err != nil {
		t.Fatal(err)
	}
	if author != "" {
		f.contributor(t, id, author)
	}
	return book
}

func (f *backfillFixture) contributor(t *testing.T, bookID, name string) {
	t.Helper()
	ctx := context.Background()
	meta, err := f.st.CatalogBookMetadata(ctx, f.user.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	meta.Contributors = []store.BookContributor{{
		ContributorID:  "c-" + metadata.NormalizeName(name),
		Name:           name,
		NormalizedName: metadata.NormalizeName(name),
		Role:           "author",
		Source:         store.MetadataEmbedded,
	}}
	if _, err := f.st.ApplyCatalogBookMetadata(ctx, f.user.ID, store.ApplyBookMetadataRequest{
		Metadata: meta, ExpectedRevision: meta.Book.Revision, UpdatedAt: f.now,
	}); err != nil {
		t.Fatal(err)
	}
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
// resolve, so a freshly uploaded library reports no statistics at all
// until every book has been opened. The backfill is what makes those
// books countable without a reader visiting each one.
func TestBackfillMapsEveryBook(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	report := f.run(t)
	if report.Books != 2 || report.Created != 2 {
		t.Fatalf("report = %+v, want 2 books and 2 creations", report)
	}
	for _, id := range []string{"b1", "b2"} {
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
	if _, err := f.st.UserBookWork(context.Background(), f.user.ID, "b2"); !errors.Is(err, store.ErrNotFound) {
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
	for i := 0; i < total; i++ {
		f.book(t, fmt.Sprintf("b%03d", i), fmt.Sprintf("Title %03d", i), "Ada Author")
	}

	report := f.run(t)
	if report.Books != total || report.Created != total {
		t.Fatalf("report = %+v, want %d books", report, total)
	}
}

// TestBackfillSkipsLibrariesTheUserCannotRead: the walk goes through the
// same ACL-checked calls the API uses, so a library shared with somebody
// else is invisible to it.
func TestBackfillSkipsLibrariesTheUserCannotRead(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")

	ctx := context.Background()
	other := store.User{
		ID: "u-other", Name: "other", Argon2Hash: "x",
		Timezone: "UTC", CreatedAt: f.now,
	}
	if err := f.st.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	report, err := Backfill(ctx, f.st, other.ID,
		func() (string, error) { return "work-x", nil },
		func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	if report.Books != 0 {
		t.Fatalf("outsider saw %d books", report.Books)
	}
	if _, err := f.st.UserBookWork(ctx, other.ID, "b1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outsider was mapped to another user's book: %v", err)
	}
}

// TestBackfillMapsASharedLibraryPerUser: a library shared read-only is
// part of the reader's catalog, so their backfill has to cover it. The
// mapping it creates is theirs alone — reading history is per user, which
// is what keeps a shared library from also sharing what its readers read.
func TestBackfillMapsASharedLibraryPerUser(t *testing.T) {
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
	if err := f.st.GrantLibraryAccess(ctx, f.user.ID, f.lib.ID, reader.ID,
		store.LibraryRoleRead, f.now); err != nil {
		t.Fatal(err)
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
	if _, err := f.st.UserBookWork(ctx, f.user.ID, "b1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the owner was mapped by the reader's backfill: %v", err)
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

// vanishingStore is a book that is trashed between being listed and being
// read. A backfill over a live server will hit this, and stopping the
// whole run because one book went away would be the wrong trade.
type vanishingStore struct {
	store.Store
	gone string
}

func (v vanishingStore) CatalogBookMetadata(
	ctx context.Context, userID, bookID string, required store.LibraryRole,
) (store.BookMetadata, error) {
	if bookID == v.gone {
		return store.BookMetadata{}, store.ErrNotFound
	}
	return v.Store.CatalogBookMetadata(ctx, userID, bookID, required)
}

func TestBackfillContinuesPastABookThatVanished(t *testing.T) {
	f := newBackfillFixture(t)
	f.book(t, "b1", "Dune", "Frank Herbert")
	f.book(t, "b2", "Neuromancer", "William Gibson")

	ids := 0
	report, err := Backfill(context.Background(),
		vanishingStore{Store: f.st, gone: "b1"}, f.user.ID,
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

func (fs failingStore) ListBookFiles(
	ctx context.Context, userID, bookID string, required store.LibraryRole,
) ([]store.BookFile, error) {
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
