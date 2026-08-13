package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// searchFixture is a small library whose books overlap enough that every
// question below has a wrong answer available.
type searchFixture struct {
	s        store.Store
	ctx      context.Context
	owner    store.User
	reader   store.User
	outsider store.User
	library  string
	at       time.Time
}

func newSearchFixture(t *testing.T, s store.Store) *searchFixture {
	t.Helper()
	f := &searchFixture{
		s: s, ctx: context.Background(),
		owner:    MkUser(t, s, "search-owner"),
		reader:   MkUser(t, s, "search-reader"),
		outsider: MkUser(t, s, "search-outsider"),
		library:  "lib-search",
		at:       time.Now().UTC().Truncate(time.Second),
	}
	if err := s.CreateLibrary(f.ctx, store.Library{
		ID: f.library, OwnerUserID: f.owner.ID, QuotaUserID: f.owner.ID,
		Kind: store.LibraryManaged, Name: "Search", CreatedAt: f.at,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(f.ctx, f.owner.ID, f.library,
		f.reader.ID, store.LibraryRoleRead, f.at); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *searchFixture) book(
	t *testing.T, id, title string, fill func(*store.BookMetadata),
) {
	t.Helper()
	if err := f.s.CreateCatalogBook(f.ctx, f.owner.ID, store.CatalogBook{
		ID: id, LibraryID: f.library, Status: store.BookActive,
		Title: title, TitleSource: store.MetadataEmbedded, CreatedAt: f.at,
	}); err != nil {
		t.Fatal(err)
	}
	if fill == nil {
		return
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
			UpdatedAt: f.at,
		}); err != nil {
		t.Fatal(err)
	}
}

// search runs a query as the owner and returns the ids it found, in the
// order it found them.
func (f *searchFixture) search(
	t *testing.T, text string, entities ...string,
) ([]string, store.SearchResult) {
	t.Helper()
	result, err := f.s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
		LibraryID: f.library, Text: text, Entities: entities, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.Books))
	for _, b := range result.Books {
		ids = append(ids, b.ID)
	}
	return ids, result
}

func seedSearchBooks(t *testing.T, f *searchFixture) {
	t.Helper()
	f.book(t, "s-dune", "Dune", func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{{
			ContributorID: "sc-herbert", Name: "Frank Herbert",
			NormalizedName: "frank herbert", Role: "author",
			Source: store.MetadataEmbedded,
		}}
		m.Tags = []store.BookTaxon{{
			ID: "st-space", Name: "Space", NormalizedName: "space",
			Source: store.MetadataEmbedded,
		}}
		m.Series = []store.BookSeries{{
			SeriesID: "ss-dune", Name: "Dune Chronicles",
			NormalizedName: "dune chronicles", Source: store.MetadataEmbedded,
		}}
	})
	f.book(t, "s-messiah", "Dune Messiah", func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{{
			ContributorID: "sc-herbert", Name: "Frank Herbert",
			NormalizedName: "frank herbert", Role: "author",
			Source: store.MetadataEmbedded,
		}}
		m.Series = []store.BookSeries{{
			SeriesID: "ss-dune", Name: "Dune Chronicles",
			NormalizedName: "dune chronicles", Source: store.MetadataEmbedded,
		}}
	})
	// This book names Dune only in its description, so it must be found
	// by the word and must not outrank the two that are called it.
	f.book(t, "s-essay", "Sandworms Considered", func(m *store.BookMetadata) {
		m.Book.Description = "An essay about Dune and its imitators."
		m.Book.DescriptionSource = store.MetadataEmbedded
		m.Tags = []store.BookTaxon{{
			ID: "st-space", Name: "Space", NormalizedName: "space",
			Source: store.MetadataEmbedded,
		}}
	})
	f.book(t, "s-unrelated", "Émile", nil)
}

func testSearchFindsBooksByEverythingTheySay(t *testing.T, open OpenFunc) {
	f := newSearchFixture(t, open(t))
	seedSearchBooks(t, f)

	ids, _ := f.search(t, "dune")
	if len(ids) != 3 {
		t.Fatalf("searching for dune found %v", ids)
	}
	// A book called Dune must beat a book that merely mentions it.
	// Anything else makes the search box feel broken on the one query
	// everybody tries first.
	if ids[len(ids)-1] != "s-essay" {
		t.Fatalf("the essay did not rank last: %v", ids)
	}

	// An author is as good a way to find a book as its title.
	if ids, _ := f.search(t, "herbert"); len(ids) != 2 {
		t.Fatalf("searching by author found %v", ids)
	}
	// So is what the book belongs to.
	if ids, _ := f.search(t, "chronicles"); len(ids) != 2 {
		t.Fatalf("searching by series found %v", ids)
	}
	// And what it is about.
	if ids, _ := f.search(t, "space"); len(ids) != 2 {
		t.Fatalf("searching by tag found %v", ids)
	}

	// Prefixes match, so a search box can answer before somebody has
	// finished typing.
	if ids, _ := f.search(t, "herb"); len(ids) != 2 {
		t.Fatalf("a prefix found %v", ids)
	}
	// Every word must match, because adding a word is asking to narrow.
	if ids, _ := f.search(t, "dune messiah"); len(ids) != 1 ||
		ids[0] != "s-messiah" {
		t.Fatalf("two words found %v", ids)
	}
	// Diacritics are folded, so a library catalogued with accents is
	// searchable by somebody who cannot type them.
	if ids, _ := f.search(t, "emile"); len(ids) != 1 || ids[0] != "s-unrelated" {
		t.Fatalf("searching without the accent found %v", ids)
	}
}

func testSearchFollowsTheCatalogItIndexes(t *testing.T, open OpenFunc) {
	f := newSearchFixture(t, open(t))
	seedSearchBooks(t, f)

	// An edit is findable immediately. A search that lags behind the
	// catalog lies about it.
	meta, err := f.s.CatalogBookMetadata(
		f.ctx, f.owner.ID, "s-unrelated", store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	meta.Book.Title = "Zamyatin's We"
	meta.Book.TitleSource = store.MetadataManual
	if _, err := f.s.ApplyCatalogBookMetadata(f.ctx, f.owner.ID,
		store.ApplyBookMetadataRequest{
			Metadata: meta, ExpectedRevision: meta.Book.Revision,
			UpdatedAt: f.at,
		}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := f.search(t, "zamyatin"); len(ids) != 1 || ids[0] != "s-unrelated" {
		t.Fatalf("a new title was not searchable: %v", ids)
	}
	if ids, _ := f.search(t, "emile"); len(ids) != 0 {
		t.Fatalf("the old title still matched: %v", ids)
	}

	// A rename changes what every book claiming it is findable by, and
	// the book itself never moved.
	if _, err := f.s.RenameCatalogEntity(f.ctx, f.owner.ID, f.library,
		"sc-herbert", store.EntityContributor, "Herbert, Frank"); err != nil {
		t.Fatal(err)
	}
	if ids, _ := f.search(t, "herbert"); len(ids) != 2 {
		t.Fatalf("a renamed author was not searchable: %v", ids)
	}

	// A trashed book is not in the catalog, so it is not in the answer.
	if _, err := f.s.TrashCatalogBook(
		f.ctx, f.owner.ID, "s-messiah", f.at, f.at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ids, _ := f.search(t, "messiah"); len(ids) != 0 {
		t.Fatalf("a trashed book was still findable: %v", ids)
	}
	if _, err := f.s.RestoreCatalogBook(f.ctx, f.owner.ID, "s-messiah", f.at); err != nil {
		t.Fatal(err)
	}
	if ids, _ := f.search(t, "messiah"); len(ids) != 1 {
		t.Fatalf("a restored book was not findable again: %v", ids)
	}
}

func testSearchFacetsDescribeTheAnswer(t *testing.T, open OpenFunc) {
	f := newSearchFixture(t, open(t))
	seedSearchBooks(t, f)

	_, result := f.search(t, "dune")
	counts := map[string]int{}
	kinds := map[string]store.EntityKind{}
	for _, facet := range result.Facets {
		counts[facet.ID] = facet.BookCount
		kinds[facet.ID] = facet.Kind
	}
	// Counts are over the matched books, not the library: a facet's job
	// is to describe the answer, not the shelf it came from.
	if counts["sc-herbert"] != 2 || counts["ss-dune"] != 2 {
		t.Fatalf("facet counts over the match: %v", counts)
	}
	if counts["st-space"] != 2 {
		t.Fatalf("the tag facet counted %d, want both matching books", counts["st-space"])
	}
	if kinds["sc-herbert"] != store.EntityContributor ||
		kinds["st-space"] != store.EntityTag {
		t.Fatalf("facets did not say what kind they are: %v", kinds)
	}

	// A facet is a suggestion, so sending one back must narrow.
	ids, _ := f.search(t, "dune", "sc-herbert")
	if len(ids) != 2 {
		t.Fatalf("filtering by author found %v", ids)
	}
	// Two filters mean both, because that is what picking two says.
	if ids, _ := f.search(t, "dune", "sc-herbert", "st-space"); len(ids) != 1 ||
		ids[0] != "s-dune" {
		t.Fatalf("filtering by two found %v", ids)
	}
	// A filter with no words is still a question worth answering: it is
	// how somebody browses everything by one author.
	if ids, _ := f.search(t, "", "sc-herbert"); len(ids) != 2 {
		t.Fatalf("filtering with no words found %v", ids)
	}
}

func testSearchIsScopedAndBounded(t *testing.T, open OpenFunc) {
	s := open(t)
	f := newSearchFixture(t, s)
	seedSearchBooks(t, f)

	// A reader can search; that is what reading a library means.
	result, err := s.SearchCatalogBooks(f.ctx, f.reader.ID, store.SearchQuery{
		LibraryID: f.library, Text: "dune", Limit: 20,
	})
	if err != nil || len(result.Books) != 3 {
		t.Fatalf("reader search: %v %d", err, len(result.Books))
	}
	// Somebody with no access gets not-found rather than an empty answer:
	// an empty answer is still an answer about somebody else's library.
	if _, err := s.SearchCatalogBooks(f.ctx, f.outsider.ID, store.SearchQuery{
		LibraryID: f.library, Text: "dune", Limit: 20,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outsider search: %v", err)
	}

	// An empty box is not a request for the whole library.
	empty, err := s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
		LibraryID: f.library, Text: "   ", Limit: 20,
	})
	if err != nil || len(empty.Books) != 0 || len(empty.Facets) != 0 {
		t.Fatalf("empty query: %v %+v", err, empty)
	}
	// Punctuation on its own is the same thing, and must not reach the
	// index as syntax.
	for _, text := range []string{`"`, `*`, `AND`, `) OR (`, `^`} {
		if _, err := s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
			LibraryID: f.library, Text: text, Limit: 20,
		}); err != nil {
			t.Fatalf("query %q errored: %v", text, err)
		}
	}

	// The limit is enforced by the store, not trusted from the caller.
	for _, limit := range []int{0, -1, store.MaxSearchLimit + 1} {
		if _, err := s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
			LibraryID: f.library, Text: "dune", Limit: limit,
		}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	// A cut answer says so, so a caller can ask for a narrower search
	// rather than implying it found everything.
	cut, err := s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
		LibraryID: f.library, Text: "dune", Limit: 1,
	})
	if err != nil || len(cut.Books) != 1 || !cut.Truncated {
		t.Fatalf("truncation: %v %+v", err, cut)
	}
	full, err := s.SearchCatalogBooks(f.ctx, f.owner.ID, store.SearchQuery{
		LibraryID: f.library, Text: "messiah", Limit: 20,
	})
	if err != nil || full.Truncated {
		t.Fatalf("a complete answer claimed to be cut: %v %+v", err, full)
	}
}

// A merge must leave the surviving books findable by the name that
// survived, and must not leave them findable by the one that did not.
func testSearchFollowsAMerge(t *testing.T, open OpenFunc) {
	f := newSearchFixture(t, open(t))
	seedSearchBooks(t, f)
	f.book(t, "s-other", "The Dosadi Experiment", func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{{
			ContributorID: "sc-herbert-alt", Name: "Herbert, Frank",
			NormalizedName: "herbert, frank", Role: "author",
			Source: store.MetadataEmbedded,
		}}
	})
	if _, err := f.s.MergeCatalogEntities(f.ctx, f.owner.ID, f.library,
		"sc-herbert-alt", "sc-herbert", store.EntityContributor,
		f.at); err != nil {
		t.Fatal(err)
	}
	ids, _ := f.search(t, "frank herbert")
	if len(ids) != 3 {
		t.Fatalf("after the merge, the author found %v", ids)
	}
	_, result := f.search(t, "herbert")
	for _, facet := range result.Facets {
		if facet.ID == "sc-herbert-alt" {
			t.Fatalf("a merged-away entity is still a facet: %+v", result.Facets)
		}
	}
}
