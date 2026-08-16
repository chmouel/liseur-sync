package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testEntitiesFoldAcrossFolders is ADR-0019 in one test: a series, an
// author and a tag observed in two folders are one row each, holding
// every book that named them. Before ADR-0019 each of these was two
// rows, so a series split over two folders showed half its volumes and
// called the other half missing.
func testEntitiesFoldAcrossFolders(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	left := MkFolder(t, s, "fold-left", store.FolderPlain)
	right := MkFolder(t, s, "fold-right", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, left.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-one",
			Title:  "One",
			Series: []store.ObservedSeries{{Name: "Foundation", Position: Ptr(1.0)}},
			Tags:   []string{"Sci-Fi"},
			Contributors: []store.ObservedContributor{
				{Name: "Isaac Asimov", Role: store.ContributorRoleAuthor},
			}},
	}, true, now)
	doReconcile(t, s, right.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-two",
			Title:  "Two",
			Series: []store.ObservedSeries{{Name: "foundation", Position: Ptr(2.0)}},
			Tags:   []string{"sci-fi"},
			Contributors: []store.ObservedContributor{
				{Name: "isaac asimov", Role: store.ContributorRoleAuthor},
			}},
	}, true, now)

	// The fold is by normalized name, so the second folder's different
	// spelling joins the first rather than starting a row of its own,
	// and the first-seen display spelling is the one that is kept.
	for _, kind := range []store.EntityKind{
		store.EntitySeries, store.EntityTag, store.EntityContributor,
	} {
		entities, err := s.ListCatalogEntities(ctx, anyReader, kind, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(entities) != 1 {
			t.Fatalf("%s: want one library-wide row, got %+v", kind, entities)
		}
		if entities[0].BookCount != 2 {
			t.Fatalf("%s %q: want both folders' books, got %d",
				kind, entities[0].Name, entities[0].BookCount)
		}
		books, _, err := s.ListBooksByEntity(ctx, anyReader, entities[0].ID, kind, nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(books) != 2 {
			t.Fatalf("%s: shelf does not span folders: %+v", kind, books)
		}
	}
}

// testEntityOrphansAreCollected covers the other half of ADR-0019:
// entities outlive no folder. Removing a folder drops its memberships,
// and an entity nothing points at any more is deleted rather than left
// as an empty shelf — unless a reader's claim is what points at it.
func testEntityOrphansAreCollected(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "orphan-reader")
	keep := MkFolder(t, s, "orphan-keep", store.FolderPlain)
	drop := MkFolder(t, s, "orphan-drop", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, keep.ID, []store.ObservedBook{
		{RelativePath: "kept.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-kept",
			Title: "Kept", Tags: []string{"Shared"}},
	}, true, now)
	doReconcile(t, s, drop.ID, []store.ObservedBook{
		{RelativePath: "gone.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-gone",
			Title: "Gone", Tags: []string{"Shared", "Doomed"},
			Series: []store.ObservedSeries{{Name: "Claimed", Position: Ptr(1.0)}}},
	}, true, now)

	// A reader shelves a book from the surviving folder beside the
	// doomed folder's series. That claim is the only thing that will
	// still name the series once the folder is gone.
	claimed := seriesIDByName(t, s, "Claimed")
	keptBook := bookIDByPath(t, s, keep.ID, "kept.epub")
	if err := s.SetBookSeriesOverride(ctx, reader.ID, keptBook,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{SeriesID: claimed, Position: Ptr(2.0)}},
		now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteFolder(ctx, drop.ID); err != nil {
		t.Fatal(err)
	}

	tags := map[string]bool{}
	entities, err := s.ListCatalogEntities(ctx, anyReader, store.EntityTag, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		tags[e.Name] = true
	}
	if !tags["Shared"] {
		t.Error("a tag still used by another folder's book was collected")
	}
	if tags["Doomed"] {
		t.Error("a tag nothing names any more was left behind")
	}

	// The claimed series survives with no observed membership at all:
	// collecting it would silently delete a reader's shelf.
	if _, err := s.CatalogEntityByID(ctx, anyReader, claimed, store.EntitySeries); err != nil {
		t.Fatalf("a series held only by a claim was collected: %v", err)
	}
	books, _, err := s.ListBooksByEntity(ctx, reader.ID, claimed, store.EntitySeries, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].Title != "Kept" {
		t.Fatalf("the claimed shelf lost its book: %+v", books)
	}
}

// testEntityGCKeepsWhatAScanStillNames guards the narrower rule that a
// reconcile pass collects the entities its own rewrite emptied, without
// touching entities other folders still use.
func testEntityGCKeepsWhatAScanStillNames(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	left := MkFolder(t, s, "gc-left", store.FolderPlain)
	right := MkFolder(t, s, "gc-right", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, left.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a",
			Title: "A", Tags: []string{"Both", "OnlyLeft"}},
	}, true, now)
	doReconcile(t, s, right.ID, []store.ObservedBook{
		{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-b",
			Title: "B", Tags: []string{"Both"}},
	}, true, now)

	// The left book is re-read with one tag dropped. Its bytes changed,
	// so it is a new catalog row and its old memberships go with it.
	doReconcile(t, s, left.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 2, MTime: now.Add(time.Hour), ContentSHA256: "sha-a2",
			Title: "A", Tags: []string{"Both"}},
	}, true, now.Add(time.Hour))

	names := map[string]bool{}
	entities, err := s.ListCatalogEntities(ctx, anyReader, store.EntityTag, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		names[e.Name] = true
	}
	if names["OnlyLeft"] {
		t.Error("a tag no book names any more survived its folder's pass")
	}
	if !names["Both"] {
		t.Error("a tag another folder still names was collected")
	}

	// An incomplete pass observes nothing and must not be read as an
	// emptying: the entity is still there afterwards.
	if _, err := s.ReconcileFolder(ctx, right.ID, nil, false, now.Add(2*time.Hour)); err != nil &&
		!errors.Is(err, store.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := s.CatalogEntityByID(ctx, anyReader,
		tagIDByName(t, s, "Both"), store.EntityTag); err != nil {
		t.Fatalf("an incomplete pass collected a live tag: %v", err)
	}
}

func tagIDByName(t *testing.T, s store.Store, name string) string {
	t.Helper()
	entities, err := s.ListCatalogEntities(
		context.Background(), anyReader, store.EntityTag, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if e.Name == name {
			return e.ID
		}
	}
	t.Fatalf("no tag named %q", name)
	return ""
}
