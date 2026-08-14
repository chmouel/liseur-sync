package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// entityFixture builds a library with three books whose tags, series and
// contributors overlap in the ways a merge has to survive.
type entityFixture struct {
	s       store.Store
	ctx     context.Context
	owner   store.User
	reader  store.User
	strange store.User
	library string
}

func newEntityFixture(t *testing.T, s store.Store) *entityFixture {
	t.Helper()
	f := &entityFixture{
		s: s, ctx: context.Background(),
		owner:   MkUser(t, s, "entity-owner"),
		reader:  MkUser(t, s, "entity-reader"),
		strange: MkUser(t, s, "entity-outsider"),
		library: "lib-entities",
	}
	now := time.Now().UTC()
	if err := s.CreateLibrary(f.ctx, store.Library{
		ID: f.library, OwnerUserID: f.owner.ID, QuotaUserID: f.owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Entities", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(f.ctx, f.owner.ID, f.library,
		f.reader.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}
	return f
}

// book creates one active book and applies the metadata built by fill.
func (f *entityFixture) book(
	t *testing.T, id, title string, at time.Time,
	fill func(*store.BookMetadata),
) {
	t.Helper()
	if err := f.s.CreateCatalogBook(f.ctx, f.owner.ID, store.CatalogBook{
		ID: id, LibraryID: f.library, Status: store.BookActive,
		Title: title, TitleSource: store.MetadataEmbedded, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	meta, err := f.s.CatalogBookMetadata(
		f.ctx, f.owner.ID, id, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	fill(&meta)
	if _, err := f.s.ApplyCatalogBookMetadata(f.ctx, f.owner.ID,
		store.ApplyBookMetadataRequest{
			Metadata: meta, ExpectedRevision: meta.Book.Revision,
			UpdatedAt: at,
		}); err != nil {
		t.Fatal(err)
	}
}

func (f *entityFixture) metadata(t *testing.T, bookID string) store.BookMetadata {
	t.Helper()
	meta, err := f.s.CatalogBookMetadata(
		f.ctx, f.owner.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func testCatalogEntityListing(t *testing.T, open OpenFunc) {
	f := newEntityFixture(t, open(t))
	now := time.Now().UTC()
	pos1, pos2 := 1.0, 2.0

	f.book(t, "book-one", "Dune", now, func(m *store.BookMetadata) {
		m.Tags = []store.BookTaxon{
			{ID: "tag-sf", Name: "Science Fiction",
				NormalizedName: "science fiction", Source: store.MetadataEmbedded},
		}
		m.Series = []store.BookSeries{
			{SeriesID: "series-dune", Name: "Dune", NormalizedName: "dune",
				Position: &pos2, Source: store.MetadataEmbedded},
		}
	})
	f.book(t, "book-two", "Dune Messiah", now.Add(time.Second),
		func(m *store.BookMetadata) {
			m.Tags = []store.BookTaxon{
				{ID: "tag-sf", Name: "Science Fiction",
					NormalizedName: "science fiction", Source: store.MetadataEmbedded},
			}
			m.Series = []store.BookSeries{
				{SeriesID: "series-dune", Name: "Dune", NormalizedName: "dune",
					Position: &pos1, Source: store.MetadataEmbedded},
			}
		})
	// A third book carries a tag of its own and is then trashed, so the
	// counts can show that a trashed book stops counting.
	f.book(t, "book-three", "Notes", now.Add(2*time.Second),
		func(m *store.BookMetadata) {
			m.Tags = []store.BookTaxon{
				{ID: "tag-notes", Name: "Notes",
					NormalizedName: "notes", Source: store.MetadataEmbedded},
			}
		})

	tags, err := f.s.ListCatalogEntities(
		f.ctx, f.reader.ID, f.library, store.EntityTag, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags: %+v", tags)
	}
	// Ordered by normalized name, so "notes" precedes "science fiction".
	if tags[0].Name != "Notes" || tags[0].BookCount != 1 {
		t.Fatalf("first tag: %+v", tags[0])
	}
	if tags[1].Name != "Science Fiction" || tags[1].BookCount != 2 {
		t.Fatalf("second tag: %+v", tags[1])
	}

	trashAt := now.Add(time.Hour)
	if _, err := f.s.TrashCatalogBook(f.ctx, f.owner.ID, "book-three",
		trashAt, trashAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tags, err = f.s.ListCatalogEntities(
		f.ctx, f.reader.ID, f.library, store.EntityTag, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	// The entity survives — it is library-wide and the book can come back
	// — but it stops claiming a book nobody can browse to.
	if len(tags) != 2 || tags[0].BookCount != 0 {
		t.Fatalf("a trashed book still counts: %+v", tags)
	}

	// Paging resumes after a normalized name rather than at an offset, so
	// it stays stable while counts move underneath it.
	page, err := f.s.ListCatalogEntities(
		f.ctx, f.reader.ID, f.library, store.EntityTag, "notes", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Name != "Science Fiction" {
		t.Fatalf("second page: %+v", page)
	}

	// A series lists its books in reading order, not upload order.
	books, err := f.s.ListBooksByEntity(f.ctx, f.reader.ID, f.library,
		"series-dune", store.EntitySeries, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 || books[0].ID != "book-two" {
		t.Fatalf("series order ignored the position: %+v", books)
	}

	if _, err := f.s.ListCatalogEntities(
		f.ctx, f.strange.ID, f.library, store.EntityTag, "", 50,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outsider listed entities: %v", err)
	}
	if _, err := f.s.ListBooksByEntity(f.ctx, f.strange.ID, f.library,
		"series-dune", store.EntitySeries, nil, 50,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outsider listed a series: %v", err)
	}
}

func testCatalogEntityMerge(t *testing.T, open OpenFunc) {
	f := newEntityFixture(t, open(t))
	now := time.Now().UTC()
	pos3 := 3.0

	// Book one claims both spellings of the series, the target row
	// without a position and the losing row with one. Book two claims
	// only the losing spelling.
	f.book(t, "book-one", "Sourcery", now, func(m *store.BookMetadata) {
		m.Series = []store.BookSeries{
			{SeriesID: "series-good", Name: "Discworld",
				NormalizedName: "discworld", Source: store.MetadataEmbedded},
			{SeriesID: "series-bad", Name: "Disc World",
				NormalizedName: "disc world", Position: &pos3,
				Source: store.MetadataFilename, Locked: true},
		}
	})
	f.book(t, "book-two", "Mort", now.Add(time.Second),
		func(m *store.BookMetadata) {
			m.Series = []store.BookSeries{
				{SeriesID: "series-bad", Name: "Disc World",
					NormalizedName: "disc world", Source: store.MetadataFilename},
			}
		})

	before := f.metadata(t, "book-one").Book.Revision
	moved, err := f.s.MergeCatalogEntities(f.ctx, f.owner.ID, f.library,
		"series-bad", "series-good", store.EntitySeries, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Only book two's membership moved; book one's collided and folded.
	if moved != 1 {
		t.Fatalf("moved %d memberships, want 1", moved)
	}

	one := f.metadata(t, "book-one")
	if len(one.Series) != 1 || one.Series[0].SeriesID != "series-good" {
		t.Fatalf("book one series after merge: %+v", one.Series)
	}
	// The position and the lock were things a person asserted, so a merge
	// that dropped them would lose information nobody asked to lose.
	if one.Series[0].Position == nil || *one.Series[0].Position != pos3 {
		t.Fatalf("merge dropped the only position anyone asserted: %+v", one.Series[0])
	}
	if !one.Series[0].Locked {
		t.Fatal("merge dropped a manual lock")
	}
	if one.Book.Revision <= before {
		t.Fatal("a merged book kept its revision, so a stale writer could win")
	}

	two := f.metadata(t, "book-two")
	if len(two.Series) != 1 || two.Series[0].SeriesID != "series-good" {
		t.Fatalf("book two series after merge: %+v", two.Series)
	}

	remaining, err := f.s.ListCatalogEntities(
		f.ctx, f.owner.ID, f.library, store.EntitySeries, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "series-good" ||
		remaining[0].BookCount != 2 {
		t.Fatalf("series after merge: %+v", remaining)
	}
}

// testCatalogEntityMergeKeepsDistinctRoles pins the one kind whose
// membership is not keyed by entity alone. The same person credited as
// author under one spelling and translator under another has made two
// claims, and folding the spellings must not fold the claims.
func testCatalogEntityMergeKeepsDistinctRoles(t *testing.T, open OpenFunc) {
	f := newEntityFixture(t, open(t))
	now := time.Now().UTC()

	f.book(t, "book-one", "The Dispossessed", now, func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{
			{ContributorID: "c-good", Name: "Ursula K. Le Guin",
				NormalizedName: "ursula k. le guin", Role: "author",
				Source: store.MetadataEmbedded},
			{ContributorID: "c-bad", Name: "ursula k le guin",
				NormalizedName: "ursula k le guin", Role: "translator",
				Position: 1, Source: store.MetadataFilename},
		}
	})

	if _, err := f.s.MergeCatalogEntities(f.ctx, f.owner.ID, f.library,
		"c-bad", "c-good", store.EntityContributor, now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	one := f.metadata(t, "book-one")
	if len(one.Contributors) != 2 {
		t.Fatalf("merge folded two different roles into one: %+v", one.Contributors)
	}
	roles := map[string]bool{}
	for _, c := range one.Contributors {
		if c.ContributorID != "c-good" {
			t.Fatalf("a contributor row survived the merge: %+v", c)
		}
		roles[c.Role] = true
	}
	if !roles["author"] || !roles["translator"] {
		t.Fatalf("roles after merge: %+v", one.Contributors)
	}
}

func testCatalogEntityRename(t *testing.T, open OpenFunc) {
	f := newEntityFixture(t, open(t))
	now := time.Now().UTC()

	f.book(t, "book-one", "Sourcery", now, func(m *store.BookMetadata) {
		m.Tags = []store.BookTaxon{
			{ID: "tag-a", Name: "scifi", NormalizedName: "scifi",
				Source: store.MetadataEmbedded},
			{ID: "tag-b", Name: "Fantasy", NormalizedName: "fantasy",
				Source: store.MetadataEmbedded},
		}
	})

	renamed, err := f.s.RenameCatalogEntity(
		f.ctx, f.owner.ID, f.library, "tag-a", store.EntityTag, "  Science  Fiction ")
	if err != nil {
		t.Fatal(err)
	}
	// The display spelling is what was typed, trimmed; the normalized
	// name is what matching uses.
	if renamed.Name != "Science  Fiction" ||
		renamed.NormalizedName != "science fiction" {
		t.Fatalf("renamed: %+v", renamed)
	}
	if renamed.BookCount != 1 {
		t.Fatalf("rename lost the memberships: %+v", renamed)
	}

	// Renaming onto a name that is taken is a merge, and a merge is a
	// decision about identity that a rename must not make silently.
	if _, err := f.s.RenameCatalogEntity(f.ctx, f.owner.ID, f.library,
		"tag-b", store.EntityTag, "science fiction",
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("rename onto a taken name: %v", err)
	}
	// Case and spacing only: a name that normalizes to itself is a
	// respelling, not a conflict with itself.
	if _, err := f.s.RenameCatalogEntity(f.ctx, f.owner.ID, f.library,
		"tag-b", store.EntityTag, "FANTASY"); err != nil {
		t.Fatalf("respelling an entity as itself: %v", err)
	}
}

func testCatalogEntityRejectsBadInput(t *testing.T, open OpenFunc) {
	f := newEntityFixture(t, open(t))
	now := time.Now().UTC()
	f.book(t, "book-one", "Sourcery", now, func(m *store.BookMetadata) {
		m.Tags = []store.BookTaxon{
			{ID: "tag-a", Name: "scifi", NormalizedName: "scifi",
				Source: store.MetadataEmbedded},
		}
	})

	if _, err := f.s.ListCatalogEntities(
		f.ctx, f.owner.ID, f.library, store.EntityKind("libraries"), "", 50,
	); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatal("a kind outside the closed set reached a query")
	}
	for _, limit := range []int{0, -1, store.MaxEntityListLimit + 1} {
		if _, err := f.s.ListCatalogEntities(
			f.ctx, f.owner.ID, f.library, store.EntityTag, "", limit,
		); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
	// Merging an entity into itself would delete the entity it just moved
	// every membership to.
	if _, err := f.s.MergeCatalogEntities(f.ctx, f.owner.ID, f.library,
		"tag-a", "tag-a", store.EntityTag, now,
	); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("self-merge: %v", err)
	}
	if _, err := f.s.MergeCatalogEntities(f.ctx, f.owner.ID, f.library,
		"tag-a", "tag-missing", store.EntityTag, now,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("merge into an entity that does not exist: %v", err)
	}
	if _, err := f.s.RenameCatalogEntity(f.ctx, f.owner.ID, f.library,
		"tag-a", store.EntityTag, "   ",
	); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("blank rename: %v", err)
	}

	// A reader may browse entities and must not reshape them.
	if _, err := f.s.RenameCatalogEntity(f.ctx, f.reader.ID, f.library,
		"tag-a", store.EntityTag, "renamed",
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a reader renamed an entity: %v", err)
	}
	if _, err := f.s.MergeCatalogEntities(f.ctx, f.reader.ID, f.library,
		"tag-a", "tag-b", store.EntityTag, now,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a reader merged entities: %v", err)
	}
}
