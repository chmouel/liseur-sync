package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The series claim suite (ADR-0018). A claim is one layer's whole answer
// to which series a book is in, resolved at read time over what the last
// reconcile pass observed.

// seriesNamesFor is what one reader sees a book's series as, in the
// batched read every shelf renders through.
func seriesNamesFor(
	t *testing.T, s store.Store, userID, bookID string,
) []string {
	t.Helper()
	rel, err := s.CatalogBookRelationsForBooks(
		context.Background(), userID, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, series := range rel.Series[bookID] {
		place := "-"
		if series.Position != nil {
			place = fmt.Sprintf("%g", *series.Position)
		}
		out = append(out, fmt.Sprintf("%s@%s/%s", series.Name, place, series.Source))
	}
	return out
}

func bookIDByPath(
	t *testing.T, s store.Store, folderID, path string,
) string {
	t.Helper()
	books, err := s.ListCatalogBooks(context.Background(), "", folderID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.RelativePath == path {
			return b.ID
		}
	}
	t.Fatalf("no book at %q", path)
	return ""
}

func seriesIDByName(t *testing.T, s store.Store, name string) string {
	t.Helper()
	entities, err := s.ListCatalogEntities(
		context.Background(), anyReader, store.EntitySeries, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if e.Name == name {
			return e.ID
		}
	}
	t.Fatalf("no series named %q", name)
	return ""
}

// testSeriesClaimLayers is the whole point of the feature: a personal
// claim beats a shared one, a shared claim beats the folder, and neither
// is visible to a reader who did not make it.
func testSeriesClaimLayers(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	admin := MkUser(t, s, "claim-admin")
	reader := MkUser(t, s, "claim-reader")
	stranger := MkUser(t, s, "claim-stranger")
	folder := MkFolder(t, s, "series-claims", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "vol.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-vol",
			Title: "Volume", Series: []store.ObservedSeries{
				{Name: "As Scanned", Position: Ptr(4.0)}}},
	}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "vol.epub")

	if got := seriesNamesFor(t, s, reader.ID, bookID); fmt.Sprint(got) !=
		"[As Scanned@4/folder]" {
		t.Fatalf("before any claim: %v", got)
	}

	// The shared layer corrects the library once, for everybody.
	if _, err := s.SetBookSeriesOverride(ctx, admin.ID, bookID,
		store.SeriesSourceShared,
		[]store.SeriesClaimItem{{Name: "Corrected", Position: Ptr(1.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}
	for _, who := range []store.User{admin, reader, stranger} {
		if got := seriesNamesFor(t, s, who.ID, bookID); fmt.Sprint(got) !=
			"[Corrected@1/shared]" {
			t.Fatalf("shared claim as %s: %v", who.Name, got)
		}
	}

	// One reader disagrees, and only that reader sees the disagreement.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Mine", Position: Ptr(2.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}
	if got := seriesNamesFor(t, s, reader.ID, bookID); fmt.Sprint(got) !=
		"[Mine@2/personal]" {
		t.Fatalf("personal claim: %v", got)
	}
	if got := seriesNamesFor(t, s, stranger.ID, bookID); fmt.Sprint(got) !=
		"[Corrected@1/shared]" {
		t.Fatalf("a personal claim leaked to another reader: %v", got)
	}

	// Clearing falls back to the layer beneath rather than to the disk.
	if _, err := s.ClearBookSeriesOverride(
		ctx, reader.ID, bookID, store.SeriesSourcePersonal,
		store.SeriesClaimMutation{At: now.Add(time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	if got := seriesNamesFor(t, s, reader.ID, bookID); fmt.Sprint(got) !=
		"[Corrected@1/shared]" {
		t.Fatalf("after clearing the personal claim: %v", got)
	}
	if _, err := s.ClearBookSeriesOverride(
		ctx, admin.ID, bookID, store.SeriesSourceShared,
		store.SeriesClaimMutation{At: now.Add(2 * time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	if got := seriesNamesFor(t, s, reader.ID, bookID); fmt.Sprint(got) !=
		"[As Scanned@4/folder]" {
		t.Fatalf("after clearing the shared claim: %v", got)
	}
	// Clearing an absent claim asked for an absence and got one.
	if _, err := s.ClearBookSeriesOverride(
		ctx, reader.ID, bookID, store.SeriesSourcePersonal,
		store.SeriesClaimMutation{At: now.Add(3 * time.Second)},
	); err != nil {
		t.Fatalf("clearing nothing: %v", err)
	}
}

// testSeriesClaimEmptyMeansNoSeries pins the distinction the claim table
// exists for: a claim with no memberships says the book is in no series,
// which is not the same as having made no claim.
func testSeriesClaimEmptyMeansNoSeries(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "empty-claim-reader")
	folder := MkFolder(t, s, "series-empty-claim", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "stray.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-stray",
			Title: "Stray", Series: []store.ObservedSeries{{Name: "Implied By A Directory"}}},
	}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "stray.epub")

	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal, nil, store.SeriesClaimMutation{At: now}); err != nil {
		t.Fatal(err)
	}
	if got := seriesNamesFor(t, s, reader.ID, bookID); len(got) != 0 {
		t.Fatalf("an empty claim left the book in a series: %v", got)
	}

	layers, err := s.BookSeriesLayers(ctx, reader.ID, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers.Folder) != 1 || layers.Folder[0].Name != "Implied By A Directory" {
		t.Fatalf("the folder layer should still say what it saw: %+v", layers.Folder)
	}
	if layers.Personal == nil || len(layers.Personal) != 0 {
		t.Fatalf("an empty claim must read as present and empty, got %+v",
			layers.Personal)
	}
	if layers.Shared != nil {
		t.Fatalf("no shared claim was made, got %+v", layers.Shared)
	}
	if layers.Source != store.SeriesSourcePersonal {
		t.Fatalf("effective source: %q", layers.Source)
	}

	// The series entity survives losing its only book, counting zero,
	// exactly as it does when a book goes missing.
	entities, err := s.ListCatalogEntities(ctx, reader.ID, store.EntitySeries, "", 10)
	if err != nil || len(entities) != 1 {
		t.Fatalf("series entities: %+v %v", entities, err)
	}
	if entities[0].BookCount != 0 {
		t.Fatalf("count for a series nobody is in: %d", entities[0].BookCount)
	}
	// Another reader still sees the book in it.
	other := MkUser(t, s, "empty-claim-other")
	entities, err = s.ListCatalogEntities(ctx, other.ID, store.EntitySeries, "", 10)
	if err != nil || len(entities) != 1 || entities[0].BookCount != 1 {
		t.Fatalf("another reader's count: %+v %v", entities, err)
	}
}

func testSeriesClaimRevisionPrecondition(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "timestamp-reader")
	folder := MkFolder(t, s, "series-timestamps", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-timestamp", Title: "Book",
	}}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "book.epub")
	if got, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal, []store.SeriesClaimItem{{Name: "First"}},
		store.SeriesClaimMutation{ClientTS: "first", IfUpdatedAt: nil, At: now},
	); err != nil || got != store.SeriesClaimApplied {
		t.Fatalf("first claim: %q, %v", got, err)
	}
	layers, err := s.BookSeriesLayers(ctx, reader.ID, bookID)
	if err != nil || layers.PersonalUpdatedAt == nil {
		t.Fatalf("claim revision: %+v %v", layers, err)
	}
	revision := layers.PersonalUpdatedAt
	staleRevision := time.Unix(0, 0).UTC()
	if got, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal, []store.SeriesClaimItem{{Name: "First"}},
		store.SeriesClaimMutation{ClientTS: "first", IfUpdatedAt: nil, At: now.Add(time.Second)},
	); err != nil || got != store.SeriesClaimDuplicate {
		t.Fatalf("duplicate claim: %q, %v", got, err)
	}
	if got, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal, []store.SeriesClaimItem{{Name: "Stale"}},
		store.SeriesClaimMutation{
			ClientTS:    "stale",
			IfUpdatedAt: &staleRevision,
			At:          now.Add(time.Second),
		},
	); err != nil || got != store.SeriesClaimStale {
		t.Fatalf("stale claim: %q, %v", got, err)
	}
	if got, err := s.ClearBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		store.SeriesClaimMutation{
			ClientTS:    "delete",
			IfUpdatedAt: &staleRevision,
			At:          now.Add(time.Second),
		},
	); err != nil || got != store.SeriesClaimStale {
		t.Fatalf("stale delete: %q, %v", got, err)
	}
	if got, err := s.ClearBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		store.SeriesClaimMutation{ClientTS: "delete", IfUpdatedAt: revision, At: now.Add(time.Second)},
	); err != nil || got != store.SeriesClaimApplied {
		t.Fatalf("delete: %q, %v", got, err)
	}
	if got, err := s.ClearBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		store.SeriesClaimMutation{ClientTS: "delete", IfUpdatedAt: nil, At: now.Add(2 * time.Second)},
	); err != nil || got != store.SeriesClaimDuplicate {
		t.Fatalf("duplicate delete: %q, %v", got, err)
	}
}

// testSeriesClaimRevisionIsMillisecondPrecise pins the precision the
// protocol promises. A client quotes a revision back as a precondition,
// and a client that keeps it as milliseconds since the epoch cannot
// quote a microsecond. A revision finer than that would make every
// precondition miss, and a reader whose claim is waiting to be sent
// would be told it was stale forever, on every retry, for good.
func testSeriesClaimRevisionIsMillisecondPrecise(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "precision-reader")
	folder := MkFolder(t, s, "series-precision", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 123456789, time.UTC)
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-precision", Title: "Book",
	}}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "book.epub")
	if got, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal, []store.SeriesClaimItem{{Name: "First"}},
		store.SeriesClaimMutation{ClientTS: "first", At: now},
	); err != nil || got != store.SeriesClaimApplied {
		t.Fatalf("first claim: %q, %v", got, err)
	}
	layers, err := s.BookSeriesLayers(ctx, reader.ID, bookID)
	if err != nil || layers.PersonalUpdatedAt == nil {
		t.Fatalf("claim revision: %+v %v", layers, err)
	}
	revision := layers.PersonalUpdatedAt.UTC()
	if revision.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("revision %s is finer than a millisecond", revision.Format(time.RFC3339Nano))
	}
	// What a client holds: the revision as whole milliseconds, taken
	// apart and put back together the way a database column would.
	quoted := time.UnixMilli(revision.UnixMilli()).UTC()
	if got, err := s.ClearBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		store.SeriesClaimMutation{
			ClientTS: "delete", IfUpdatedAt: &quoted, At: now.Add(time.Second),
		},
	); err != nil || got != store.SeriesClaimApplied {
		t.Fatalf("delete against a millisecond revision: %q, %v", got, err)
	}
}

// testSeriesClaimSurvivesReconcile is the rule that makes the layer
// worth having: a pass rewrites what it observed and leaves every claim
// alone, including a claim on a series no pass has ever seen.
func testSeriesClaimSurvivesReconcile(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "survive-reader")
	folder := MkFolder(t, s, "series-survive", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	observed := []store.ObservedBook{
		{RelativePath: "book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-book",
			Title: "Book", Series: []store.ObservedSeries{{Name: "Scanned"}}},
	}
	doReconcile(t, s, folder.ID, observed, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "book.epub")

	// A name nothing has scanned mints the series.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Invented", Position: Ptr(1.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}
	invented := seriesIDByName(t, s, "Invented")

	// Three more passes, one of them a no-op and one incomplete.
	doReconcile(t, s, folder.ID, observed, true, now.Add(time.Hour))
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "book.epub", SizeBytes: 1, MTime: now, Unchanged: true},
	}, true, now.Add(2*time.Hour))
	doReconcile(t, s, folder.ID, nil, false, now.Add(3*time.Hour))

	if got := seriesNamesFor(t, s, reader.ID, bookID); fmt.Sprint(got) !=
		"[Invented@1/personal]" {
		t.Fatalf("a pass disturbed a claim: %v", got)
	}
	if got := seriesIDByName(t, s, "Invented"); got != invented {
		t.Fatalf("the invented series was replaced: %q then %q", invented, got)
	}
	// The folder layer still reports what the disk says, unchanged.
	layers, err := s.BookSeriesLayers(ctx, reader.ID, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers.Folder) != 1 || layers.Folder[0].Name != "Scanned" {
		t.Fatalf("folder layer after the passes: %+v", layers.Folder)
	}
}

// testSeriesClaimFollowsIdentity holds the claim to ADR-0017's identity
// rules: a Calibre book keeps its claim when the curator moves it, and a
// plain-folder book replaced by different bytes at the same path does
// not, because content change is not identity transfer.
func testSeriesClaimFollowsIdentity(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "identity-reader")
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	calibre := MkFolder(t, s, "series-identity-calibre", store.FolderCalibre)
	doReconcile(t, s, calibre.ID, []store.ObservedBook{
		{CalibreID: Ptr(int64(7)), RelativePath: "Author/Title (7)/book.epub",
			SizeBytes: 1, MTime: now, ContentSHA256: "sha-cal", Title: "Title"},
	}, true, now)
	calibreBook := bookIDByPath(t, s, calibre.ID, "Author/Title (7)/book.epub")
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, calibreBook,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Kept", Position: Ptr(1.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}
	// The curator renames the book, so Calibre rewrites its directory.
	doReconcile(t, s, calibre.ID, []store.ObservedBook{
		{CalibreID: Ptr(int64(7)), RelativePath: "Author/Renamed (7)/book.epub",
			SizeBytes: 1, MTime: now, ContentSHA256: "sha-cal", Title: "Renamed"},
	}, true, now.Add(time.Hour))
	if got := seriesNamesFor(t, s, reader.ID, calibreBook); fmt.Sprint(got) !=
		"[Kept@1/personal]" {
		t.Fatalf("a Calibre move lost the claim: %v", got)
	}

	plain := MkFolder(t, s, "series-identity-plain", store.FolderPlain)
	doReconcile(t, s, plain.ID, []store.ObservedBook{
		{RelativePath: "book.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "sha-before", Title: "Before"},
	}, true, now)
	plainBook := bookIDByPath(t, s, plain.ID, "book.epub")
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, plainBook,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Lost", Position: Ptr(1.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}
	// Different bytes at the same path are a different book.
	doReconcile(t, s, plain.ID, []store.ObservedBook{
		{RelativePath: "book.epub", SizeBytes: 2, MTime: now.Add(time.Hour),
			ContentSHA256: "sha-after", Title: "After", Replaces: true},
	}, true, now.Add(time.Hour))
	replaced := bookIDByPath(t, s, plain.ID, "book.epub")
	if replaced == plainBook {
		t.Fatalf("replacing the bytes should have replaced the row")
	}
	if got := seriesNamesFor(t, s, reader.ID, replaced); len(got) != 0 {
		t.Fatalf("a claim about the old file survived onto the new one: %v", got)
	}
}

// testSeriesClaimOrdersAndPages covers the sharp edge: a claimed
// position feeds the same sort key and cursor the scanned one did, so an
// overridden series pages in the order it is shown, unplaced last.
func testSeriesClaimOrdersAndPages(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "order-reader")
	folder := MkFolder(t, s, "series-claim-order", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a",
			Title: "A", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(1.0)}}},
		{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-b",
			Title: "B", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(2.0)}}},
		{RelativePath: "c.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-c",
			Title: "C", Series: []store.ObservedSeries{{Name: "Trilogy", Position: Ptr(3.0)}}},
		// The volume the folder never numbered.
		{RelativePath: "d.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-d",
			Title: "D"},
	}, true, now)
	trilogy := seriesIDByName(t, s, "Trilogy")

	// The reader reverses the first three and adopts the fourth.
	if err := s.ReorderSeries(ctx, reader.ID, trilogy,
		store.SeriesSourcePersonal, []store.SeriesPlacement{
			{BookID: bookIDByPath(t, s, folder.ID, "a.epub"), Position: Ptr(3.0)},
			{BookID: bookIDByPath(t, s, folder.ID, "b.epub"), Position: Ptr(2.0)},
			{BookID: bookIDByPath(t, s, folder.ID, "c.epub"), Position: Ptr(1.0)},
			{BookID: bookIDByPath(t, s, folder.ID, "d.epub")},
		}, now); err != nil {
		t.Fatal(err)
	}

	want := []string{"C", "B", "A", "D"}
	page, next, err := s.ListBooksByEntity(ctx, reader.ID, trilogy, store.EntitySeries, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range page {
		got = append(got, b.Title)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("claimed series order: got %v want %v", got, want)
	}
	if next != nil {
		t.Fatalf("a short page offered a cursor: %+v", next)
	}

	// One at a time must agree, which is what pins the cursor to the
	// resolved position rather than the scanned one.
	got = nil
	var cursor *store.CatalogBookCursor
	for range want {
		page, next, err := s.ListBooksByEntity(ctx, reader.ID, trilogy, store.EntitySeries, cursor, 1)
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
		t.Fatalf("claimed series order across pages: got %v want %v", got, want)
	}

	// Nobody else was reordered.
	other := MkUser(t, s, "order-other")
	page, _, err = s.ListBooksByEntity(ctx, other.ID, trilogy, store.EntitySeries, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, b := range page {
		got = append(got, b.Title)
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"A", "B", "C"}) {
		t.Fatalf("another reader saw a reordering: %v", got)
	}
}

// testSeriesReorderKeepsOtherMemberships is why a reorder reads before
// it writes: a claim speaks for the whole book, so renumbering a trilogy
// must carry across the volume's place in an omnibus.
func testSeriesReorderKeepsOtherMemberships(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "omnibus-reader")
	folder := MkFolder(t, s, "series-omnibus", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "vol.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-vol",
			Title: "Volume", Series: []store.ObservedSeries{
				{Name: "Trilogy", Position: Ptr(1.0)},
				{Name: "Omnibus", Position: Ptr(9.0)},
			}},
	}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "vol.epub")
	trilogy := seriesIDByName(t, s, "Trilogy")

	if err := s.ReorderSeries(ctx, reader.ID, trilogy,
		store.SeriesSourcePersonal, []store.SeriesPlacement{
			{BookID: bookID, Position: Ptr(2.0)},
		}, now); err != nil {
		t.Fatal(err)
	}
	got := seriesNamesFor(t, s, reader.ID, bookID)
	if fmt.Sprint(got) != "[Omnibus@9/personal Trilogy@2/personal]" {
		t.Fatalf("a reorder dropped a membership: %v", got)
	}
}

// testSeriesClaimRefusals covers what the store will not be asked to do.
func testSeriesClaimRefusals(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	reader := MkUser(t, s, "refusal-reader")
	folder := MkFolder(t, s, "series-refusals", store.FolderPlain)
	other := MkFolder(t, s, "series-refusals-other", store.FolderPlain)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "here.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-here",
			Title: "Here"},
	}, true, now)
	doReconcile(t, s, other.ID, []store.ObservedBook{
		{RelativePath: "there.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-there",
			Title: "There", Series: []store.ObservedSeries{{Name: "Elsewhere"}}},
	}, true, now)
	bookID := bookIDByPath(t, s, folder.ID, "here.epub")
	elsewhere := seriesIDByName(t, s, "Elsewhere")

	// The folder layer is what the disk said; nobody claims it.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourceFolder, nil,
		store.SeriesClaimMutation{At: now},
	); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("claiming the folder layer: want ErrInvalidInput, got %v", err)
	}
	// A series first observed in another folder is still this book's to
	// join: a series is a library-wide row (ADR-0019), and shelving a
	// stray volume beside the rest of its series is the whole point.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{SeriesID: elsewhere}}, store.SeriesClaimMutation{At: now}); err != nil {
		t.Fatalf("joining a series held in another folder: %v", err)
	}
	// An item that names nothing names nothing.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "   "}},
		store.SeriesClaimMutation{At: now},
	); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("a nameless claim: want ErrInvalidInput, got %v", err)
	}
	// A book that is not there cannot be claimed about.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, "no-such-book",
		store.SeriesSourcePersonal, nil,
		store.SeriesClaimMutation{At: now},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("claiming about a missing book: want ErrNotFound, got %v", err)
	}
	// Neither can a series that does not exist be reordered.
	if err := s.ReorderSeries(ctx, reader.ID, "no-such-series",
		store.SeriesSourcePersonal, nil, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reordering a series that is not there: want ErrNotFound, got %v", err)
	}
}
