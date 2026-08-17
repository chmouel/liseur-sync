package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Merging and splitting a series (ADR-0021). The layering is not what is
// hard here: what is hard is that the next pass over the disk must agree
// with what was decided, so nearly every test below rescans.

// seedTwoShelves gives two folders a book each, in series of their own.
func seedTwoShelves(
	t *testing.T, s store.Store, prefix, firstName, secondName string, at time.Time,
) (store.Folder, store.Folder) {
	t.Helper()
	first := MkFolder(t, s, prefix+"-a", store.FolderPlain)
	second := MkFolder(t, s, prefix+"-b", store.FolderPlain)
	doReconcile(t, s, first.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: at,
			ContentSHA256: prefix + "-one", Title: "One",
			Series: []store.ObservedSeries{{Name: firstName, Position: Ptr(1.0)}}},
	}, true, at)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: at,
			ContentSHA256: prefix + "-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: secondName, Position: Ptr(1.0)}}},
	}, true, at)
	return first, second
}

func shelfNames(t *testing.T, s store.Store) []string {
	t.Helper()
	entities, err := s.ListCatalogEntities(
		context.Background(), anyReader, store.EntitySeries, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.Name)
	}
	return out
}

func shelfCount(t *testing.T, s store.Store, seriesID string) int {
	t.Helper()
	entity, err := s.CatalogEntityByID(
		context.Background(), anyReader, seriesID, store.EntitySeries)
	if err != nil {
		t.Fatal(err)
	}
	return entity.BookCount
}

// testSeriesMergeSurvivesAScan is the feature. Two shelves become one,
// and stay one when the folder that named the absorbed shelf is walked
// again — which is the whole reason a merge is not a DELETE.
func testSeriesMergeSurvivesAScan(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, second := seedTwoShelves(t, s, "merge-scan", "Metro", "Metro 2033", now)
	admin := MkUser(t, s, "merge-scan-admin")

	survivor := seriesIDByName(t, s, "Metro")
	absorbed := seriesIDByName(t, s, "Metro 2033")

	kept, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now)
	if err != nil {
		t.Fatal(err)
	}
	if kept != survivor {
		t.Fatalf("merge returned %q, want the survivor %q", kept, survivor)
	}
	if got := shelfNames(t, s); len(got) != 1 || got[0] != "Metro" {
		t.Fatalf("after the merge the library holds %v", got)
	}
	if got := shelfCount(t, s, survivor); got != 2 {
		t.Fatalf("the survivor holds %d books, want 2", got)
	}

	// The pass that undoes a naive merge: the second folder still calls
	// its book's series `Metro 2033`, and says so again.
	later := now.Add(time.Hour)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "merge-scan-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "Metro 2033", Position: Ptr(1.0)}}},
		{RelativePath: "three.epub", SizeBytes: 1, MTime: later,
			ContentSHA256: "merge-scan-three", Title: "Three",
			Series: []store.ObservedSeries{{Name: "metro 2033", Position: Ptr(2.0)}}},
	}, true, later)

	if got := shelfNames(t, s); len(got) != 1 || got[0] != "Metro" {
		t.Fatalf("a scan undid the merge: the library holds %v", got)
	}
	// The new volume lands on the surviving shelf, not on a shelf minted
	// under the absorbed name.
	if got := shelfCount(t, s, survivor); got != 3 {
		t.Fatalf("the survivor holds %d books after the scan, want 3", got)
	}
}

// testSeriesMergeCarriesClaims checks the two things a reader would
// notice: a book they filed by hand stays where they filed it, and
// nothing is renumbered to make the shelves fit together.
func testSeriesMergeCarriesClaims(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	first, _ := seedTwoShelves(t, s, "merge-claim", "Essays", "Papers", now)
	reader := MkUser(t, s, "merge-claim-reader")
	admin := MkUser(t, s, "merge-claim-admin")

	survivor := seriesIDByName(t, s, "Essays")
	absorbed := seriesIDByName(t, s, "Papers")
	book := knownByPath(t, s, first.ID)["one.epub"].ID

	// This reader says their book belongs on the shelf that is about to
	// be absorbed, at a number of their own choosing.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, book,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{SeriesID: absorbed, Position: Ptr(7.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}

	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now); err != nil {
		t.Fatal(err)
	}

	layers, err := s.BookSeriesLayers(ctx, reader.ID, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers.Effective) != 1 {
		t.Fatalf("the claim did not survive the merge: %+v", layers.Effective)
	}
	if layers.Effective[0].SeriesID != survivor {
		t.Fatalf("the claim was left pointing at a series that is gone: %+v",
			layers.Effective[0])
	}
	if layers.Effective[0].Position == nil || *layers.Effective[0].Position != 7 {
		t.Fatalf("the merge renumbered a reader's claim: %+v", layers.Effective[0])
	}
	if layers.Source != store.SeriesSourcePersonal {
		t.Fatalf("the claim stopped being personal: %v", layers.Source)
	}
}

// testSeriesUnbindRestoresFromDisk is the undo. Nothing records what the
// absorbed shelf held; the disk does, and the next pass reads it.
func testSeriesUnbindRestoresFromDisk(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, second := seedTwoShelves(t, s, "unbind", "Metro", "Metro 2033", now)
	admin := MkUser(t, s, "unbind-admin")

	survivor := seriesIDByName(t, s, "Metro")
	absorbed := seriesIDByName(t, s, "Metro 2033")
	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now); err != nil {
		t.Fatal(err)
	}

	bindings, err := s.SeriesBindings(ctx, survivor)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("a merge left %d bindings, want 1", len(bindings))
	}
	// The name is kept as it was written, so a shelf can say what it
	// absorbed rather than showing a normalization.
	if bindings[0].Name != "Metro 2033" || bindings[0].FolderID != "" {
		t.Fatalf("binding is not the global one the merge should write: %+v",
			bindings[0])
	}
	if bindings[0].CreatedBy != admin.ID {
		t.Fatalf("binding does not name its author: %+v", bindings[0])
	}

	if err := s.DeleteSeriesBinding(ctx, bindings[0].ID); err != nil {
		t.Fatal(err)
	}
	// Deleting the binding changes nothing on its own: the books are on
	// the surviving shelf until a pass says otherwise.
	if got := shelfCount(t, s, survivor); got != 2 {
		t.Fatalf("unbinding moved books by itself: survivor holds %d", got)
	}

	later := now.Add(time.Hour)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "unbind-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "Metro 2033", Position: Ptr(1.0)}}},
	}, true, later)

	names := shelfNames(t, s)
	if len(names) != 2 {
		t.Fatalf("the disk did not put the shelf back: %v", names)
	}
	if got := shelfCount(t, s, survivor); got != 1 {
		t.Fatalf("the survivor kept a book that went home: %d", got)
	}
	if err := s.DeleteSeriesBinding(ctx, bindings[0].ID); !errors.Is(
		err, store.ErrNotFound,
	) {
		t.Fatalf("deleting a binding twice: %v", err)
	}
}

// testSeriesSplitSurvivesAScan is the other half: one automatic fold
// undone, and still undone after the folder that caused it is walked
// again.
func testSeriesSplitSurvivesAScan(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	first, second := seedTwoShelves(t, s, "split", "Essays", "Essays", now)
	admin := MkUser(t, s, "split-admin")

	// Two folders, one name: ADR-0019's fold made them one shelf.
	shared := seriesIDByName(t, s, "Essays")
	if got := shelfCount(t, s, shared); got != 2 {
		t.Fatalf("the fold did not happen, so there is nothing to split: %d", got)
	}

	newID, err := s.SplitSeriesFolder(
		ctx, admin.ID, shared, second.ID, "Essays (Kolakowski)", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := shelfCount(t, s, shared); got != 1 {
		t.Fatalf("the original shelf holds %d books after the split, want 1", got)
	}
	if got := shelfCount(t, s, newID); got != 1 {
		t.Fatalf("the new shelf holds %d books, want 1", got)
	}

	// The pass that undoes a naive split: the second folder still calls
	// its directory `Essays`.
	later := now.Add(time.Hour)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "split-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "Essays", Position: Ptr(1.0)}}},
		{RelativePath: "three.epub", SizeBytes: 1, MTime: later,
			ContentSHA256: "split-three", Title: "Three",
			Series: []store.ObservedSeries{{Name: "Essays", Position: Ptr(2.0)}}},
	}, true, later)

	if got := shelfCount(t, s, newID); got != 2 {
		t.Fatalf("a scan undid the split: the new shelf holds %d", got)
	}
	if got := shelfCount(t, s, shared); got != 1 {
		t.Fatalf("the split leaked into the other folder: %d", got)
	}

	// The binding belongs to the folder whose books left, and to no
	// other: the first folder still means what it always meant.
	doReconcile(t, s, first.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "split-one", Title: "One",
			Series: []store.ObservedSeries{{Name: "Essays", Position: Ptr(1.0)}}},
	}, true, later)
	if got := shelfCount(t, s, shared); got != 1 {
		t.Fatalf("the first folder's book moved: %d", got)
	}
}

// testSeriesSplitTakesAbsorbedNames covers the case that makes split's
// binding more than one row: a shelf that has already absorbed a name
// must hand *both* names to the folder that leaves, or the next pass
// sends its books straight back through the merge.
func testSeriesSplitTakesAbsorbedNames(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, second := seedTwoShelves(t, s, "split-merged", "Essays", "Papers", now)
	admin := MkUser(t, s, "split-merged-admin")

	survivor := seriesIDByName(t, s, "Essays")
	absorbed := seriesIDByName(t, s, "Papers")
	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now); err != nil {
		t.Fatal(err)
	}

	newID, err := s.SplitSeriesFolder(
		ctx, admin.ID, survivor, second.ID, "Papers, collected", now)
	if err != nil {
		t.Fatal(err)
	}

	later := now.Add(time.Hour)
	doReconcile(t, s, second.ID, []store.ObservedBook{
		{RelativePath: "two.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "split-merged-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "Papers", Position: Ptr(1.0)}}},
	}, true, later)

	if got := shelfCount(t, s, newID); got != 1 {
		t.Fatalf("the folder's own name was not bound to the split shelf: %d", got)
	}
	if got := shelfCount(t, s, survivor); got != 1 {
		t.Fatalf("the merge pulled the split books back: %d", got)
	}
}

// testSeriesMergeRefusals pins the refusals. Each is a request that has a
// meaning the store must not guess at.
func testSeriesMergeRefusals(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, second := seedTwoShelves(t, s, "merge-refuse", "Metro", "Metro 2033", now)
	admin := MkUser(t, s, "merge-refuse-admin")
	survivor := seriesIDByName(t, s, "Metro")
	absorbed := seriesIDByName(t, s, "Metro 2033")

	if _, err := s.MergeSeries(ctx, admin.ID, survivor, survivor, now); !errors.Is(
		err, store.ErrInvalidInput,
	) {
		t.Fatalf("merging a series into itself: %v", err)
	}
	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, "nope", now); !errors.Is(
		err, store.ErrNotFound,
	) {
		t.Fatalf("merging into a series that is not there: %v", err)
	}
	if _, err := s.MergeSeries(ctx, admin.ID, "nope", survivor, now); !errors.Is(
		err, store.ErrNotFound,
	) {
		t.Fatalf("merging a series that is not there: %v", err)
	}

	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now); err != nil {
		t.Fatal(err)
	}
	// Merging into a series that has itself been absorbed is refused by
	// its absence rather than by a check: the row is gone.
	if _, err := s.MergeSeries(ctx, admin.ID, survivor, absorbed, now); !errors.Is(
		err, store.ErrNotFound,
	) {
		t.Fatalf("merging into an absorbed series: %v", err)
	}

	// Split's own refusals, on the shelf the merge left.
	if _, err := s.SplitSeriesFolder(
		ctx, admin.ID, survivor, second.ID, "  ", now,
	); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("splitting onto a nameless shelf: %v", err)
	}
	if _, err := s.SplitSeriesFolder(
		ctx, admin.ID, survivor, second.ID, "Metro", now,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("splitting onto an occupied name: %v", err)
	}
	empty := MkFolder(t, s, "merge-refuse-empty", store.FolderPlain)
	if _, err := s.SplitSeriesFolder(
		ctx, admin.ID, survivor, empty.ID, "Nothing", now,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("splitting off a folder with no books on the shelf: %v", err)
	}
}

// testSeriesSplitOfOneFolderIsARename refuses the request that has a
// better answer elsewhere: a shelf a single folder produced is renamed,
// not split.
func testSeriesSplitOfOneFolderIsARename(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	folder, series := seedRenameLibrary(t, s, "split-one-folder", "Essays", now)
	admin := MkUser(t, s, "split-one-folder-admin")

	if _, err := s.SplitSeriesFolder(
		ctx, admin.ID, series, folder.ID, "Essays, collected", now,
	); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("splitting the only folder off a shelf: %v", err)
	}
}

// testSeriesBindingsDieWithTheirFolder: a split is a statement about one
// folder's books, so removing the folder removes the statement — and
// nothing else's.
func testSeriesBindingsDieWithTheirFolder(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	_, second := seedTwoShelves(t, s, "binding-folder", "Essays", "Papers", now)
	admin := MkUser(t, s, "binding-folder-admin")

	survivor := seriesIDByName(t, s, "Essays")
	absorbed := seriesIDByName(t, s, "Papers")
	if _, err := s.MergeSeries(ctx, admin.ID, absorbed, survivor, now); err != nil {
		t.Fatal(err)
	}
	split, err := s.SplitSeriesFolder(
		ctx, admin.ID, survivor, second.ID, "Papers, collected", now)
	if err != nil {
		t.Fatal(err)
	}
	if bindings, err := s.SeriesBindings(ctx, split); err != nil {
		t.Fatal(err)
	} else if len(bindings) != 2 {
		t.Fatalf("a split of a merged shelf wrote %d bindings, want both names",
			len(bindings))
	}

	if err := s.DeleteFolder(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if bindings, err := s.SeriesBindings(ctx, split); err != nil {
		t.Fatal(err)
	} else if len(bindings) != 0 {
		t.Fatalf("the folder went but its bindings stayed: %+v", bindings)
	}
	// The merge's own binding has no folder and is nobody's to remove.
	bindings, err := s.SeriesBindings(ctx, survivor)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].FolderID != "" {
		t.Fatalf("a folder took the global binding with it: %+v", bindings)
	}
}
