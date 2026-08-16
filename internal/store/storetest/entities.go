package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCatalogEntityListing covers paging one folder's entities by
// normalized name, with counts over active books only: an entity whose
// books are all currently missing must read as empty rather than as a
// populated entity whose page turns out blank.
func testCatalogEntityListing(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "entity-listing", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "zeta.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-zeta",
			Title: "Zeta", Tags: []string{"Sci-Fi"}},
		{RelativePath: "alpha.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-alpha",
			Title: "Alpha", Tags: []string{"Fantasy"}},
		{RelativePath: "mid.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-mid",
			Title: "Mid", Tags: []string{"Fantasy", "Sci-Fi"}},
		{RelativePath: "away.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-away",
			Title: "Away", Tags: []string{"Horror"}},
	}, true, now)
	// "Horror"'s only book goes missing: the tag must still exist but
	// count zero active books.
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "zeta.epub", SizeBytes: 1, MTime: now, Unchanged: true},
		{RelativePath: "alpha.epub", SizeBytes: 1, MTime: now, Unchanged: true},
		{RelativePath: "mid.epub", SizeBytes: 1, MTime: now, Unchanged: true},
	}, true, now.Add(time.Hour))

	entities, err := s.ListCatalogEntities(ctx, folder.ID, store.EntityTag, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range entities {
		got[e.Name] = e.BookCount
	}
	if got["Fantasy"] != 2 || got["Sci-Fi"] != 2 || got["Horror"] != 0 {
		t.Fatalf("entity counts: %+v", got)
	}

	// Paging by normalized name in two steps must produce the same
	// ordering as one page.
	var paged []string
	after := ""
	for {
		page, err := s.ListCatalogEntities(ctx, folder.ID, store.EntityTag, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			paged = append(paged, e.Name)
		}
		after = page[len(page)-1].NormalizedName
	}
	want := []string{"Fantasy", "Horror", "Sci-Fi"}
	if fmt.Sprint(paged) != fmt.Sprint(want) {
		t.Fatalf("entity paging: got %v want %v", paged, want)
	}

	for _, e := range entities {
		if e.Name != "Fantasy" {
			continue
		}
		got, err := s.CatalogEntityByID(ctx, folder.ID, e.ID, store.EntityTag)
		if err != nil || got.Name != "Fantasy" || got.BookCount != 2 {
			t.Fatalf("CatalogEntityByID: %+v %v", got, err)
		}
	}
	if _, err := s.CatalogEntityByID(ctx, folder.ID, "no-such-tag", store.EntityTag); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing entity: want ErrNotFound, got %v", err)
	}

	for _, limit := range []int{0, -1, store.MaxEntityListLimit + 1} {
		if _, err := s.ListCatalogEntities(ctx, folder.ID, store.EntityTag, "", limit); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatalf("entity limit %d: want ErrInvalidInput, got %v", limit, err)
		}
	}
	if _, err := s.ListCatalogEntities(ctx, folder.ID, store.EntityKind("nonsense"), "", 10); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("bad entity kind: want ErrInvalidInput, got %v", err)
	}
}

// testListBooksByEntitySeriesOrder covers the one entity kind with an
// order of its own: a series' books are shelved by position, with an
// unplaced book last rather than first, because an unplaced book is an
// unanswered question rather than book zero.
func testListBooksByEntitySeriesOrder(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "entity-series-order", store.FolderPlain)
	other := MkFolder(t, s, "entity-series-order-other", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "three.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-three",
			Title: "Three", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(3.0)}}},
		{RelativePath: "one.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-one",
			Title: "One", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(1.0)}}},
		{RelativePath: "unplaced.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-unplaced",
			Title: "Unplaced", Series: []store.ObservedSeries{{Name: "Trilogy"}}},
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-two",
			Title: "Two", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(2.0)}}},
	}, true, now)
	doReconcile(t, s, other.ID, []store.ObservedBook{
		{RelativePath: "other.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-other",
			Title: "Other", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(1.0)}}},
	}, true, now)

	entities, err := s.ListCatalogEntities(ctx, folder.ID, store.EntitySeries, "", 10)
	if err != nil || len(entities) != 1 {
		t.Fatalf("series entities: %+v %v", entities, err)
	}
	seriesID := entities[0].ID

	page, next, err := s.ListBooksByEntity(ctx, folder.ID, seriesID, store.EntitySeries, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range page {
		got = append(got, b.Title)
	}
	want := []string{"One", "Two", "Three", "Unplaced"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("series book order: got %v want %v", got, want)
	}
	if next != nil {
		t.Fatalf("a page shorter than its limit offered a next cursor: %+v", next)
	}

	// Paging one volume at a time must produce the same order. One
	// reconciliation pass stamps every book with the same created_at, so
	// a cursor that did not carry the series position would filter on a
	// key the rows are not ordered by and skip volumes here.
	got = nil
	var cursor *store.CatalogBookCursor
	for range want {
		page, next, err := s.ListBooksByEntity(
			ctx, folder.ID, seriesID, store.EntitySeries, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 {
			t.Fatalf("page of one: got %d books", len(page))
		}
		got = append(got, page[0].Title)
		if next == nil {
			break
		}
		cursor = next
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("series order across pages: got %v want %v", got, want)
	}

	for _, limit := range []int{0, -1, 501} {
		if _, _, err := s.ListBooksByEntity(ctx, folder.ID, seriesID, store.EntitySeries, nil, limit); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatalf("book-by-entity limit %d: want ErrInvalidInput, got %v", limit, err)
		}
	}
}
