package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testSearchFindsBooksByEverythingTheySay covers the breadth of the
// search index: title, contributor and series/tag text are all
// searchable, and a query with neither words nor a filter answers
// nothing rather than the whole folder.
func testSearchFindsBooksByEverythingTheySay(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "search-breadth", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{
			RelativePath: "neuromancer.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-neuromancer",
			Title: "Neuromancer",
			Contributors: []store.ObservedContributor{
				{Name: "William Gibson", Role: store.ContributorRoleAuthor, Position: 1},
			},
		},
		{
			RelativePath: "left-hand.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-left-hand",
			Title:  "The Left Hand of Darkness",
			Series: []store.ObservedSeries{{Name: "Hainish Cycle"}},
		},
		{
			RelativePath: "unrelated.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-unrelated",
			Title: "Something Else Entirely",
		},
	}, true, now)

	byTitle, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "neuromancer", Limit: 10})
	if err != nil || len(byTitle.Books) != 1 || byTitle.Books[0].Title != "Neuromancer" {
		t.Fatalf("search by title: %+v %v", byTitle, err)
	}
	byAuthor, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "gibson", Limit: 10})
	if err != nil || len(byAuthor.Books) != 1 || byAuthor.Books[0].Title != "Neuromancer" {
		t.Fatalf("search by author: %+v %v", byAuthor, err)
	}
	bySeries, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "hainish", Limit: 10})
	if err != nil || len(bySeries.Books) != 1 || bySeries.Books[0].Title != "The Left Hand of Darkness" {
		t.Fatalf("search by series: %+v %v", bySeries, err)
	}

	empty, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Books) != 0 {
		t.Fatalf("no words and no filter answered a whole folder: %+v", empty)
	}
}

// testSearchFollowsTheCatalogItIndexes covers that the index tracks
// what ReconcileFolder writes: a book that goes missing is no longer
// found by its old title once replaced, and a replaced book's new
// content is what search now sees.
func testSearchFollowsTheCatalogItIndexes(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "search-follows", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 11, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-original",
		Title: "Original Title",
	}}, true, now)

	result, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "original", Limit: 10})
	if err != nil || len(result.Books) != 1 {
		t.Fatalf("search before replacement: %+v %v", result, err)
	}

	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "book.epub", SizeBytes: 2, MTime: now.Add(time.Hour), ContentSHA256: "sha-replaced",
		Title: "Replaced Title", Replaces: true,
	}}, true, now.Add(time.Hour))

	stale, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "original", Limit: 10})
	if err != nil || len(stale.Books) != 0 {
		t.Fatalf("replaced book still found by its old title: %+v %v", stale, err)
	}
	fresh, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "replaced", Limit: 10})
	if err != nil || len(fresh.Books) != 1 {
		t.Fatalf("search after replacement: %+v %v", fresh, err)
	}
}

// testSearchFacetsDescribeTheAnswer covers that facets describe the
// matched set, not the whole folder: a tag only one non-matching book
// carries must not appear.
func testSearchFacetsDescribeTheAnswer(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "search-facets", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-one",
			Title: "Space Opera One", Tags: []string{"Sci-Fi"}},
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-two",
			Title: "Space Opera Two", Tags: []string{"Sci-Fi", "Adventure"}},
		{RelativePath: "unrelated.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-unrelated",
			Title: "Cooking Guide", Tags: []string{"Nonfiction"}},
	}, true, now)

	result, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "space opera", Limit: 10})
	if err != nil || len(result.Books) != 2 {
		t.Fatalf("search: %+v %v", result, err)
	}
	facets := map[string]int{}
	for _, f := range result.Facets {
		facets[f.Name] = f.BookCount
	}
	if facets["Sci-Fi"] != 2 || facets["Adventure"] != 1 {
		t.Fatalf("facets: %+v", facets)
	}
	if _, ok := facets["Nonfiction"]; ok {
		t.Fatalf("a tag from an unmatched book appeared in the facets: %+v", facets)
	}
}

// testSearchIsScopedAndBounded covers folder isolation, the limit
// bounds, and truncation: a result cut at the limit says so rather than
// implying it found everything.
func testSearchIsScopedAndBounded(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "search-scope", store.FolderPlain)
	other := MkFolder(t, s, "search-scope-other", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 13, 0, 0, 0, time.UTC)

	var observed []store.ObservedBook
	for i := 0; i < 5; i++ {
		observed = append(observed, store.ObservedBook{
			RelativePath: fmt.Sprintf("match-%d.epub", i), SizeBytes: 1, MTime: now,
			ContentSHA256: fmt.Sprintf("sha-match-%d", i), Title: "Matching Book",
		})
	}
	doReconcile(t, s, folder.ID, observed, true, now)
	doReconcile(t, s, other.ID, []store.ObservedBook{{
		RelativePath: "match.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-other-match",
		Title: "Matching Book",
	}}, true, now)

	result, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "matching", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 3 || !result.Truncated {
		t.Fatalf("truncation: %+v %v", result, err)
	}
	for _, b := range result.Books {
		if b.FolderID != folder.ID {
			t.Fatalf("search leaked a book from another folder: %+v", b)
		}
	}

	full, err := s.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: folder.ID, Text: "matching", Limit: 10})
	if err != nil || len(full.Books) != 5 || full.Truncated {
		t.Fatalf("untruncated result: %+v %v", full, err)
	}

	for _, limit := range []int{0, -1, store.MaxSearchLimit + 1} {
		if _, err := s.SearchCatalogBooks(ctx,
			store.SearchQuery{FolderID: folder.ID, Text: "matching", Limit: limit}); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatalf("search limit %d: want ErrInvalidInput, got %v", limit, err)
		}
	}
}
