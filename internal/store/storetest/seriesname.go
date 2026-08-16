package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Series renames (ADR-0020). A rename is a display layer; the name a
// scan observed stays the fold key, which is what these tests are really
// about — the layering itself is the easy part.

func renamedName(t *testing.T, s store.Store, userID, seriesID string) store.CatalogEntity {
	t.Helper()
	entity, err := s.CatalogEntityByID(
		context.Background(), userID, seriesID, store.EntitySeries)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

// seedRenameLibrary gives a folder one book in one series, which is all
// most of these tests need.
func seedRenameLibrary(
	t *testing.T, s store.Store, folderName, seriesName string, at time.Time,
) (store.Folder, string) {
	t.Helper()
	folder := MkFolder(t, s, folderName, store.FolderPlain)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: at,
			ContentSHA256: folderName + "-one", Title: "One",
			Series: []store.ObservedSeries{{Name: seriesName, Position: Ptr(1.0)}}},
	}, true, at)
	return folder, seriesIDByName(t, s, seriesName)
}

// testSeriesRenameLayers is the feature in one test: a personal rename
// beats a shared one, a shared one beats what the scan said, clearing a
// layer falls back to the one beneath, and nobody sees another reader's
// rename.
func testSeriesRenameLayers(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, series := seedRenameLibrary(t, s, "rename-layers", "Metro", now)
	reader := MkUser(t, s, "rename-reader")
	other := MkUser(t, s, "rename-other")
	admin := MkUser(t, s, "rename-admin")

	if got := renamedName(t, s, reader.ID, series); got.Name != "Metro" ||
		got.ScannedName != "Metro" || got.NameSource != store.SeriesSourceFolder {
		t.Fatalf("before any rename: %+v", got)
	}

	if err := s.SetSeriesName(ctx, admin.ID, series,
		store.SeriesSourceShared, "Metro 2033", now); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{reader.ID, other.ID, anyReader} {
		got := renamedName(t, s, u, series)
		if got.Name != "Metro 2033" || got.NameSource != store.SeriesSourceShared {
			t.Fatalf("shared rename not seen by %q: %+v", u, got)
		}
		// The scanned name is still reported, because a client offering
		// a revert has to know what it would revert to.
		if got.ScannedName != "Metro" {
			t.Fatalf("scanned name lost: %+v", got)
		}
	}

	if err := s.SetSeriesName(ctx, reader.ID, series,
		store.SeriesSourcePersonal, "Метро", now); err != nil {
		t.Fatal(err)
	}
	if got := renamedName(t, s, reader.ID, series); got.Name != "Метро" ||
		got.NameSource != store.SeriesSourcePersonal {
		t.Fatalf("personal rename does not win: %+v", got)
	}
	if got := renamedName(t, s, other.ID, series); got.Name != "Metro 2033" {
		t.Fatalf("one reader's rename leaked to another: %+v", got)
	}

	// A book payload shows the same name the shelf does, or the two
	// pages of one library disagree.
	book := bookIDByPath(t, s, folderIDOf(t, s, "rename-layers"), "one.epub")
	rel, err := s.CatalogBookRelationsForBooks(ctx, reader.ID, []string{book})
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Series[book]) != 1 || rel.Series[book][0].Name != "Метро" {
		t.Fatalf("book payload kept the scanned name: %+v", rel.Series[book])
	}

	if err := s.ClearSeriesName(ctx, reader.ID, series,
		store.SeriesSourcePersonal); err != nil {
		t.Fatal(err)
	}
	if got := renamedName(t, s, reader.ID, series); got.Name != "Metro 2033" {
		t.Fatalf("clearing a personal rename did not fall back: %+v", got)
	}
	if err := s.ClearSeriesName(ctx, admin.ID, series,
		store.SeriesSourceShared); err != nil {
		t.Fatal(err)
	}
	if got := renamedName(t, s, reader.ID, series); got.Name != "Metro" ||
		got.NameSource != store.SeriesSourceFolder {
		t.Fatalf("clearing every layer did not return to the scan: %+v", got)
	}
	// Clearing what is not there is an absence the caller asked for.
	if err := s.ClearSeriesName(ctx, reader.ID, series,
		store.SeriesSourcePersonal); err != nil {
		t.Fatalf("clearing an absent rename: %v", err)
	}
}

// testSeriesRenameSurvivesAScan is the rule the whole design exists for.
// A pass rewrites book_series wholesale and re-resolves every observed
// name, so a rename that had moved the fold key would be undone here —
// and a second folder observing the original name would start a shelf of
// its own instead of joining the renamed one.
func testSeriesRenameSurvivesAScan(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	folder, series := seedRenameLibrary(t, s, "rename-scan", "Metro", now)
	admin := MkUser(t, s, "rename-scan-admin")

	if err := s.SetSeriesName(ctx, admin.ID, series,
		store.SeriesSourceShared, "Метро", now); err != nil {
		t.Fatal(err)
	}

	// The same pass again, still calling the series what the disk calls
	// it, plus a volume that was not there before.
	later := now.Add(time.Hour)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "rename-scan-one", Title: "One",
			Series: []store.ObservedSeries{{Name: "Metro", Position: Ptr(1.0)}}},
		{RelativePath: "two.epub", SizeBytes: 1, MTime: later,
			ContentSHA256: "rename-scan-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "metro", Position: Ptr(2.0)}}},
	}, true, later)

	got := renamedName(t, s, anyReader, series)
	if got.Name != "Метро" {
		t.Fatalf("a scan undid the rename: %+v", got)
	}
	if got.BookCount != 2 {
		t.Fatalf("the renamed shelf did not take the new volume: %+v", got)
	}

	// A second folder observing the original spelling folds onto the
	// renamed series rather than minting one beside it.
	second := MkFolder(t, s, "rename-scan-other", store.FolderPlain)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "three.epub", SizeBytes: 1, MTime: later,
			ContentSHA256: "rename-scan-three", Title: "Three",
			Series: []store.ObservedSeries{{Name: "Metro", Position: Ptr(3.0)}}},
	}, true, later)

	entities, err := s.ListCatalogEntities(ctx, anyReader, store.EntitySeries, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Fatalf("the fold key moved with the name: %+v", entities)
	}
	if entities[0].ID != series || entities[0].BookCount != 3 {
		t.Fatalf("the second folder did not join the renamed shelf: %+v", entities[0])
	}
	books, _, err := s.ListBooksByEntity(ctx, anyReader, series, store.EntitySeries, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 3 || books[0].Title != "One" || books[2].Title != "Three" {
		t.Fatalf("the renamed shelf lost its reading order: %+v", books)
	}
}

// testSeriesRenameRefusals covers what a rename may not do. The
// collision refusal is the load-bearing one: renaming onto an occupied
// name is a request to merge two shelves, which is a decision ADR-0020
// deliberately does not make.
func testSeriesRenameRefusals(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	folder := MkFolder(t, s, "rename-refusals", store.FolderPlain)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "ref-a",
			Title: "A", Series: []store.ObservedSeries{{Name: "Dune"}}},
		{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "ref-b",
			Title: "B", Series: []store.ObservedSeries{{Name: "Foundation"}}},
	}, true, now)
	dune := seriesIDByName(t, s, "Dune")
	foundation := seriesIDByName(t, s, "Foundation")
	reader := MkUser(t, s, "refusal-reader")
	other := MkUser(t, s, "refusal-other")
	admin := MkUser(t, s, "refusal-admin")

	// Onto a name a scan already gave another series.
	if err := s.SetSeriesName(ctx, reader.ID, dune,
		store.SeriesSourcePersonal, "foundation", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("renaming onto an occupied name: want conflict, got %v", err)
	}
	// Onto a name only this reader gave another series.
	if err := s.SetSeriesName(ctx, reader.ID, foundation,
		store.SeriesSourcePersonal, "Chronicles", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSeriesName(ctx, reader.ID, dune,
		store.SeriesSourcePersonal, "chronicles", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("renaming onto one's own name: want conflict, got %v", err)
	}
	// Another reader's personal name is not in this one's view, so it
	// does not collide.
	if err := s.SetSeriesName(ctx, other.ID, dune,
		store.SeriesSourcePersonal, "Chronicles", now); err != nil {
		t.Fatalf("another reader's rename collided: %v", err)
	}
	// Renaming a series to what it is already called is not a
	// collision with itself.
	if err := s.SetSeriesName(ctx, admin.ID, dune,
		store.SeriesSourceShared, "Dune", now); err != nil {
		t.Fatalf("renaming a series to its own name: %v", err)
	}

	for _, name := range []string{"", "   "} {
		if err := s.SetSeriesName(ctx, reader.ID, dune,
			store.SeriesSourcePersonal, name, now); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatalf("empty name %q: want invalid input, got %v", name, err)
		}
	}
	if err := s.SetSeriesName(ctx, reader.ID, "no-such-series",
		store.SeriesSourcePersonal, "Anything", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("renaming a series that does not exist: want not found, got %v", err)
	}
	if err := s.ClearSeriesName(ctx, reader.ID, "no-such-series",
		store.SeriesSourcePersonal); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("clearing a series that does not exist: want not found, got %v", err)
	}
	// The folder layer is not writable: it is what the disk said.
	if err := s.SetSeriesName(ctx, reader.ID, dune,
		store.SeriesSourceFolder, "Anything", now); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("renaming the folder layer: want invalid input, got %v", err)
	}
}

// testSeriesRenamePagesOnTheNameShown catches the listing bug a rename
// invites: paging by the scanned name while ordering by the shown one
// would skip a renamed series or return it twice.
func testSeriesRenamePagesOnTheNameShown(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	folder := MkFolder(t, s, "rename-paging", store.FolderPlain)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "page-a",
			Title: "A", Series: []store.ObservedSeries{{Name: "Alpha"}}},
		{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "page-b",
			Title: "B", Series: []store.ObservedSeries{{Name: "Beta"}}},
		{RelativePath: "c.epub", SizeBytes: 1, MTime: now, ContentSHA256: "page-c",
			Title: "C", Series: []store.ObservedSeries{{Name: "Gamma"}}},
	}, true, now)
	reader := MkUser(t, s, "paging-reader")

	// Alpha becomes the last name alphabetically, so a listing that
	// paged on the scanned name would lose a row.
	if err := s.SetSeriesName(ctx, reader.ID, seriesIDByName(t, s, "Alpha"),
		store.SeriesSourcePersonal, "Zeta", now); err != nil {
		t.Fatal(err)
	}

	var seen []string
	after := ""
	for range 5 {
		page, err := s.ListCatalogEntities(ctx, reader.ID, store.EntitySeries, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, page[0].Name)
		after = page[len(page)-1].NormalizedName
	}
	want := []string{"Beta", "Gamma", "Zeta"}
	if len(seen) != len(want) {
		t.Fatalf("paging a renamed listing: want %v, got %v", want, seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("paging out of order: want %v, got %v", want, seen)
		}
	}
}

// testSeriesRenameDiesWithItsSeries states that a name is not a claim: a
// rename asserts nothing about which books exist, so it does not keep an
// orphan alive, and it goes when the series does.
func testSeriesRenameDiesWithItsSeries(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	folder, series := seedRenameLibrary(t, s, "rename-gc", "Metro", now)
	admin := MkUser(t, s, "rename-gc-admin")
	if err := s.SetSeriesName(ctx, admin.ID, series,
		store.SeriesSourceShared, "Метро", now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteFolder(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CatalogEntityByID(ctx, anyReader, series, store.EntitySeries); !errors.Is(
		err, store.ErrNotFound) {
		t.Fatalf("a renamed orphan survived: %v", err)
	}

	// The same name observed again is a new series with no rename on
	// it. If the cascade had left the old row behind, this would come
	// back called "Метро" for reasons nobody could see.
	again, series2 := seedRenameLibrary(t, s, "rename-gc-again", "Metro", now)
	_ = again
	if got := renamedName(t, s, admin.ID, series2); got.Name != "Metro" ||
		got.NameSource != store.SeriesSourceFolder {
		t.Fatalf("a deleted series' name outlived it: %+v", got)
	}
}

// folderIDOf finds a folder by the name a test gave it.
func folderIDOf(t *testing.T, s store.Store, name string) string {
	t.Helper()
	folders, err := s.ListFolders(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("no folder named %q", name)
	return ""
}
