package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// MkFolder creates a folder of the given kind, rooted at a path that is
// unique to the folder's id so that two folders in the same test never
// collide on the root_path unique index.
func MkFolder(t *testing.T, s store.Store, name string, kind store.FolderKind) store.Folder {
	t.Helper()
	now := time.Now().UTC()
	f := store.Folder{
		ID: "f-" + name, Name: name, RootPath: "/srv/" + name, Kind: kind,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateFolder(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if err := s.AssignUserFolder(context.Background(), user.ID, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// doReconcile runs one pass and fails the test on error, which is what
// every caller below wants: reconciliation itself is not what most of
// these tests are checking.
func doReconcile(
	t *testing.T, s store.Store, folderID string,
	observed []store.ObservedBook, complete bool, at time.Time,
) store.ReconcileResult {
	t.Helper()
	result, err := s.ReconcileFolder(context.Background(), folderID, observed, complete, at)
	if err != nil {
		t.Fatalf("ReconcileFolder: %v", err)
	}
	return result
}

// knownByPath and knownByCalibreID turn BooksInFolder's flat list into
// the lookup a test actually wants: "what id did the book at this
// identity get".
func knownByPath(t *testing.T, s store.Store, folderID string) map[string]store.KnownBook {
	t.Helper()
	known, err := s.BooksInFolder(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]store.KnownBook, len(known))
	for _, b := range known {
		out[b.RelativePath] = b
	}
	return out
}

func knownByCalibreID(t *testing.T, s store.Store, folderID string) map[int64]store.KnownBook {
	t.Helper()
	known, err := s.BooksInFolder(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[int64]store.KnownBook, len(known))
	for _, b := range known {
		if b.CalibreID != nil {
			out[*b.CalibreID] = b
		}
	}
	return out
}

// testReconcileIdempotency covers the base case: observing the same
// file twice must not create a second row, and a pass that recognised
// the file as unchanged must report that it changed nothing.
func testReconcileIdempotency(t *testing.T, open OpenFunc) {
	s := open(t)
	folder := MkFolder(t, s, "idempotency", store.FolderPlain)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	obs := store.ObservedBook{
		RelativePath: "book.epub", SizeBytes: 100, MTime: now,
		ContentSHA256: "sha-1", Title: "One Book",
	}
	first := doReconcile(t, s, folder.ID, []store.ObservedBook{obs}, true, now)
	if first.Added != 1 || !first.Changed() {
		t.Fatalf("first pass: %+v", first)
	}

	books, err := s.ListCatalogBooks(context.Background(), "", folder.ID, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("want one book after first pass, got %d", len(books))
	}

	// The second pass is what a real scanner would submit for a file it
	// stat'd but did not re-read, because size and mtime had not moved:
	// Unchanged, and none of the metadata fields populated.
	unchangedObs := store.ObservedBook{
		RelativePath: "book.epub", SizeBytes: 100, MTime: now, Unchanged: true,
	}
	second := doReconcile(t, s, folder.ID, []store.ObservedBook{unchangedObs}, true, now)
	if second.Changed() {
		t.Fatalf("second identical pass reported a change: %+v", second)
	}

	books, err = s.ListCatalogBooks(context.Background(), "", folder.ID, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("want one book after second pass, got %d: %+v", len(books), books)
	}
}

// testReconcileMissingAndReturning covers rule 1's other half: a
// complete pass that does not see a file marks it missing and stamps
// AbsentAt, and a later pass that sees it again returns it to active
// and clears AbsentAt.
func testReconcileMissingAndReturning(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "missing-returning", store.FolderPlain)
	now := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)

	obs := store.ObservedBook{
		RelativePath: "gone.epub", SizeBytes: 10, MTime: now,
		ContentSHA256: "sha-gone", Title: "Comes And Goes",
	}
	anchor := store.ObservedBook{
		RelativePath: "anchor.epub", SizeBytes: 10, MTime: now,
		ContentSHA256: "sha-anchor", Title: "Always There",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{obs, anchor}, true, now)
	bookID := knownByPath(t, s, folder.ID)["gone.epub"].ID

	// The anchor book is observed again so the pass is not the
	// zero-observation case rule 2 protects: this pass genuinely saw
	// the whole folder and genuinely did not find "gone.epub" in it.
	missingAt := now.Add(time.Hour)
	result := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{{RelativePath: "anchor.epub", SizeBytes: 10, MTime: now, Unchanged: true}},
		true, missingAt)
	if result.Missing != 1 {
		t.Fatalf("want one missing book, got %+v", result)
	}
	got, err := s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.BookMissing {
		t.Fatalf("book not marked missing: %+v", got)
	}
	if got.AbsentAt == nil || !got.AbsentAt.UTC().Equal(missingAt) {
		t.Fatalf("AbsentAt not stamped: %+v", got.AbsentAt)
	}

	returnedAt := missingAt.Add(time.Hour)
	// The file is back with the same stat, so the pass recognises it
	// without a re-read: the return travels the Unchanged fast path,
	// which is the one touch that must still move updated_at — the book
	// visibly changed from missing to active even though no fact did.
	returning := store.ObservedBook{
		RelativePath: "gone.epub", SizeBytes: 10, MTime: now, Unchanged: true,
	}
	result = doReconcile(t, s, folder.ID, []store.ObservedBook{returning}, true, returnedAt)
	if result.Returned != 1 || result.Updated != 0 {
		t.Fatalf("want one returned book and nothing updated, got %+v", result)
	}
	got, err = s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.BookActive {
		t.Fatalf("book not returned to active: %+v", got)
	}
	if got.AbsentAt != nil {
		t.Fatalf("AbsentAt not cleared: %+v", got.AbsentAt)
	}
	if got.SeenAt == nil || !got.SeenAt.UTC().Equal(returnedAt) {
		t.Fatalf("the return did not refresh seen_at: %+v", got.SeenAt)
	}
	if !got.UpdatedAt.UTC().Equal(returnedAt) {
		t.Fatalf("the return did not move updated_at: %+v", got.UpdatedAt)
	}
}

// testReconcileIncompletePassMarksNothingMissing covers the first of
// ADR-0017's two safety rules: a pass that knows it did not see
// everything must never conclude that what it did not see is gone.
func testReconcileIncompletePassMarksNothingMissing(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "incomplete", store.FolderPlain)
	now := time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC)

	a := store.ObservedBook{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a", Title: "A"}
	b := store.ObservedBook{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-b", Title: "B"}
	doReconcile(t, s, folder.ID, []store.ObservedBook{a, b}, true, now)
	bID := knownByPath(t, s, folder.ID)["b.epub"].ID

	// A pass that only saw "a.epub" this time, but says so honestly.
	later := now.Add(time.Hour)
	result := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{{RelativePath: "a.epub", SizeBytes: 1, MTime: now, Unchanged: true}},
		false, later)
	if result.Missing != 0 {
		t.Fatalf("an incomplete pass marked books missing: %+v", result)
	}
	got, err := s.CatalogBookByID(ctx, "", bID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.BookActive {
		t.Fatalf("unobserved book was marked missing by an incomplete pass: %+v", got)
	}
}

// testReconcileZeroObservationPassMarksNothingMissing covers ADR-0017's
// second safety rule: an unmounted mount point is usually still
// readable and empty, indistinguishable from a folder somebody emptied,
// and hiding a whole catalog is the worse of the two errors.
func testReconcileZeroObservationPassMarksNothingMissing(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "zero-observation", store.FolderPlain)
	now := time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC)

	obs := store.ObservedBook{
		RelativePath: "alone.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-alone", Title: "Alone",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{obs}, true, now)
	bookID := knownByPath(t, s, folder.ID)["alone.epub"].ID

	result := doReconcile(t, s, folder.ID, nil, true, now.Add(time.Hour))
	if result.Missing != 0 {
		t.Fatalf("a zero-observation complete pass marked books missing: %+v", result)
	}
	got, err := s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.BookActive {
		t.Fatalf("book was marked missing by an empty pass: %+v", got)
	}
}

// testReconcileReplacementDropsReadingMapping covers rule 4: content
// change is not identity transfer. A reader's position on the old bytes
// must not silently become their position on the new ones.
func testReconcileReplacementDropsReadingMapping(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "replacement", store.FolderPlain)
	now := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "replacement-reader")

	obs := store.ObservedBook{
		RelativePath: "swap.epub", SizeBytes: 100, MTime: now,
		ContentSHA256: "sha-original", Title: "Original",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{obs}, true, now)
	oldBookID := knownByPath(t, s, folder.ID)["swap.epub"].ID

	work := store.Work{ID: "replacement-work", UserID: user.ID, Title: "Original", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, oldBookID, work,
		[]store.Edition{{UserID: user.ID, SHA256: "replacement-work-sha", WorkID: work.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "replacement-work-sha"}},
		false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBookWork(ctx, user.ID, oldBookID); err != nil {
		t.Fatalf("mapping not recorded before replacement: %v", err)
	}

	replaced := store.ObservedBook{
		RelativePath: "swap.epub", SizeBytes: 200, MTime: now.Add(time.Hour),
		ContentSHA256: "sha-replaced", Title: "Replaced", Replaces: true,
	}
	result := doReconcile(t, s, folder.ID, []store.ObservedBook{replaced}, true, now.Add(time.Hour))
	if result.Replaced != 1 {
		t.Fatalf("want one replacement, got %+v", result)
	}

	newBookID := knownByPath(t, s, folder.ID)["swap.epub"].ID
	if newBookID == oldBookID {
		t.Fatalf("replacement kept the old book id")
	}
	if _, err := s.CatalogBookByID(ctx, "", oldBookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old book row survived a replacement: %v", err)
	}
	if _, err := s.UserBookWork(ctx, user.ID, oldBookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reading mapping survived a content replacement: %v", err)
	}
	newBook, err := s.CatalogBookByID(ctx, "", newBookID)
	if err != nil {
		t.Fatal(err)
	}
	if newBook.ContentSHA256 != "sha-replaced" {
		t.Fatalf("new book has stale content: %+v", newBook)
	}
}

// testReconcileUnchangedKeepsMetadata covers the other half of the
// Unchanged contract: a pass that did not re-read a file carries no
// metadata, and the store must not read that emptiness as a deletion.
func testReconcileUnchangedKeepsMetadata(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "unchanged-metadata", store.FolderPlain)
	now := time.Date(2026, time.January, 6, 0, 0, 0, 0, time.UTC)

	full := store.ObservedBook{
		RelativePath: "full.epub", SizeBytes: 100, MTime: now,
		ContentSHA256: "sha-full", Title: "Full Metadata",
		Contributors: []store.ObservedContributor{
			{Name: "Ada Palmer", Role: store.ContributorRoleAuthor, Position: 1},
		},
		Series: []store.ObservedSeries{{Name: "Terra Ignota", Position: Ptr(1.0)}},
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{full}, true, now)
	bookID := knownByPath(t, s, folder.ID)["full.epub"].ID

	unchanged := store.ObservedBook{
		RelativePath: "full.epub", SizeBytes: 100, MTime: now, Unchanged: true,
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{unchanged}, true, now.Add(time.Hour))

	got, err := s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Full Metadata" {
		t.Fatalf("title blanked by an unchanged observation: %+v", got)
	}
	relations, err := s.CatalogBookRelationsForBooks(ctx, anyReader, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	if len(relations.Contributors[bookID]) != 1 || relations.Contributors[bookID][0].Name != "Ada Palmer" {
		t.Fatalf("contributors blanked by an unchanged observation: %+v", relations.Contributors[bookID])
	}
	if len(relations.Series[bookID]) != 1 || relations.Series[bookID][0].Name != "Terra Ignota" {
		t.Fatalf("series blanked by an unchanged observation: %+v", relations.Series[bookID])
	}
}

// testReconcileRepeatPassCountsNoUpdates covers the honesty of
// Updated. A Calibre pass re-reads every row of metadata.db every time
// (ADR-0022), so it re-submits full observations for books nothing
// touched; counting those writes would report a whole library as
// updated twice an hour and bury the pass that actually changed
// something. Updated therefore counts differences, in the row's facts
// or in its relations, and a pass over an untouched folder reports
// that nothing happened.
func testReconcileRepeatPassCountsNoUpdates(t *testing.T, open OpenFunc) {
	s := open(t)
	folder := MkFolder(t, s, "calibre-repeat", store.FolderCalibre)
	now := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)

	calibreID := int64(30)
	observe := func(title string, tags []string) store.ObservedBook {
		return store.ObservedBook{
			CalibreID: &calibreID, RelativePath: "A Writer/Book (30)/book.epub",
			SizeBytes: 100, MTime: now, ContentSHA256: "sha-repeat",
			Title: title, Tags: tags,
			Contributors: []store.ObservedContributor{
				{Name: "A Writer", Role: store.ContributorRoleAuthor},
			},
			Series: []store.ObservedSeries{{Name: "A Series", Position: Ptr(1.0)}},
		}
	}

	first := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{observe("Book", []string{"history"})}, true, now)
	if first.Added != 1 {
		t.Fatalf("first pass: %+v", first)
	}
	bookID := knownByCalibreID(t, s, folder.ID)[calibreID].ID

	// The same observation again, as every safety-timer pass submits it.
	second := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{observe("Book", []string{"history"})},
		true, now.Add(30*time.Minute))
	if second.Updated != 0 || second.Changed() {
		t.Fatalf("an untouched book was reported updated: %+v", second)
	}
	// The pass proved the file present, so seen_at advances — but
	// updated_at is the modification time clients see, and nothing was
	// modified.
	afterRepeat, err := s.CatalogBookByID(context.Background(), "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRepeat.UpdatedAt.Equal(now) {
		t.Fatalf("an untouched book's updated_at moved: %v", afterRepeat.UpdatedAt)
	}
	if afterRepeat.SeenAt == nil || !afterRepeat.SeenAt.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("the repeat pass did not refresh seen_at: %v", afterRepeat.SeenAt)
	}

	// A metadata edit in the row's own columns.
	third := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{observe("Book, Revised", []string{"history"})},
		true, now.Add(time.Hour))
	if third.Updated != 1 {
		t.Fatalf("a title edit was not counted: %+v", third)
	}
	afterEdit, err := s.CatalogBookByID(context.Background(), "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterEdit.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("a real edit did not move updated_at: %v", afterEdit.UpdatedAt)
	}

	// An edit that only moves relations: same row facts, one more tag.
	fourth := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{observe("Book, Revised", []string{"history", "essays"})},
		true, now.Add(2*time.Hour))
	if fourth.Updated != 1 {
		t.Fatalf("a tag-only edit was not counted: %+v", fourth)
	}

	// And the edited state, resubmitted, is quiet again.
	fifth := doReconcile(t, s, folder.ID,
		[]store.ObservedBook{observe("Book, Revised", []string{"history", "essays"})},
		true, now.Add(3*time.Hour))
	if fifth.Updated != 0 || fifth.Changed() {
		t.Fatalf("the settled state was reported updated: %+v", fifth)
	}
}

// testReconcileCalibrePathMoveKeepsIdentity covers the reason Calibre
// folders key on calibre_id rather than path: Calibre rewrites a book's
// directory name on a title or author edit, and a server that keyed on
// path would read that as one book vanishing and another appearing,
// losing whoever was reading it.
func testReconcileCalibrePathMoveKeepsIdentity(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-move", store.FolderCalibre)
	now := time.Date(2026, time.January, 7, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-move-reader")

	calibreID := int64(7)
	before := store.ObservedBook{
		CalibreID: &calibreID, RelativePath: "Old Title (7)/book.epub",
		SizeBytes: 10, MTime: now, ContentSHA256: "sha-calibre", Title: "Old Title",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{before}, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[calibreID].ID

	work := store.Work{ID: "calibre-move-work", UserID: user.ID, Title: "Old Title", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, work,
		[]store.Edition{{UserID: user.ID, SHA256: "calibre-move-sha", WorkID: work.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "calibre-move-sha"}},
		false, now); err != nil {
		t.Fatal(err)
	}

	after := store.ObservedBook{
		CalibreID: &calibreID, RelativePath: "New Title (7)/book.epub",
		SizeBytes: 10, MTime: now, ContentSHA256: "sha-calibre", Title: "New Title",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{after}, true, now.Add(time.Hour))

	stillSameID := knownByCalibreID(t, s, folder.ID)[calibreID]
	if stillSameID.ID != bookID {
		t.Fatalf("a calibre title edit changed the book id: %s != %s", stillSameID.ID, bookID)
	}
	if stillSameID.RelativePath != "New Title (7)/book.epub" {
		t.Fatalf("book did not follow its new path: %+v", stillSameID)
	}
	got, err := s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New Title" {
		t.Fatalf("title not refreshed after the move: %+v", got)
	}
	mapping, err := s.UserBookWork(ctx, user.ID, bookID)
	if err != nil {
		t.Fatalf("reading mapping lost across a calibre title edit: %v", err)
	}
	if mapping.WorkID != work.ID {
		t.Fatalf("reading mapping changed across a calibre title edit: %+v", mapping)
	}
}

// testReconcileCalibrePathSwap covers the reason the sqlite and
// postgres implementations park every path under an unreachable value
// before writing a Calibre pass's new ones: two books can legitimately
// swap the paths they hold in one editing session, and the unique index
// must never see the intermediate state where both rows briefly want
// the other's path.
func testReconcileCalibrePathSwap(t *testing.T, open OpenFunc) {
	s := open(t)
	folder := MkFolder(t, s, "calibre-swap", store.FolderCalibre)
	now := time.Date(2026, time.January, 8, 0, 0, 0, 0, time.UTC)

	one, two := int64(1), int64(2)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &one, RelativePath: "path-x/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-1", Title: "One"},
		{CalibreID: &two, RelativePath: "path-y/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-2", Title: "Two"},
	}, true, now)

	swapped := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &one, RelativePath: "path-y/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-1", Title: "One"},
		{CalibreID: &two, RelativePath: "path-x/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-2", Title: "Two"},
	}, true, now.Add(time.Hour))
	if swapped.Updated != 2 {
		t.Fatalf("path swap did not update both books: %+v", swapped)
	}

	known := knownByCalibreID(t, s, folder.ID)
	if known[one].RelativePath != "path-y/book.epub" {
		t.Fatalf("book 1 did not take book 2's old path: %+v", known[one])
	}
	if known[two].RelativePath != "path-x/book.epub" {
		t.Fatalf("book 2 did not take book 1's old path: %+v", known[two])
	}
}

// testReconcileCalibrePurgesDeletedBooks covers ADR-0022: a Calibre
// library's metadata.db is a catalog somebody curates, so a book a
// complete pass no longer finds in it was removed rather than
// misplaced, and the row goes.
//
// What goes with it is the point of the second half. A work the
// deletion left with no book and no reading at all is bookkeeping and
// is collected; a work with an op behind it is somebody's reading and
// survives its file.
func testReconcileCalibrePurgesDeletedBooks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-purge", store.FolderCalibre)
	now := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-purge-reader")

	kept, dropped, read := int64(1), int64(2), int64(3)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &kept, RelativePath: "Kept (1)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-kept", Title: "Kept"},
		{CalibreID: &dropped, RelativePath: "Dropped (2)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-dropped", Title: "Dropped"},
		{CalibreID: &read, RelativePath: "Read (3)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-read", Title: "Read"},
	}, true, now)

	known := knownByCalibreID(t, s, folder.ID)
	droppedID, readID := known[dropped].ID, known[read].ID

	// One mapping with nothing behind it, one with an op behind it.
	emptyWork := store.Work{ID: "purge-empty-work", UserID: user.ID, Title: "Dropped", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, droppedID, emptyWork,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-dropped", WorkID: emptyWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-dropped"}}, false, now); err != nil {
		t.Fatal(err)
	}
	readWork := store.Work{ID: "purge-read-work", UserID: user.ID, Title: "Read", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, readID, readWork,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-read", WorkID: readWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-read"}}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendOps(ctx, user.ID, "d-purge", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000aa", WorkID: readWork.ID,
		EditionSHA: Ptr("sha-read"), ClientTS: now, Progression: 0.3,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	result := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &kept, RelativePath: "Kept (1)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-kept", Title: "Kept"},
	}, true, now.Add(time.Hour))
	if result.Purged != 2 {
		t.Fatalf("want two books purged, got %+v", result)
	}
	if result.Missing != 0 {
		t.Fatalf("a calibre pass marked a book missing instead of purging it: %+v", result)
	}

	after := knownByCalibreID(t, s, folder.ID)
	if len(after) != 1 {
		t.Fatalf("want one book left after the purge, got %d", len(after))
	}
	if _, err := s.CatalogBookByID(ctx, "", droppedID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a book gone from metadata.db survived the pass: %v", err)
	}
	if _, err := s.WorkByID(ctx, user.ID, emptyWork.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a work with no book and no reading survived the purge: %v", err)
	}
	surviving, err := s.WorkByID(ctx, user.ID, readWork.ID)
	if err != nil {
		t.Fatalf("a work with reading behind it was collected: %v", err)
	}
	if surviving.ID != readWork.ID {
		t.Fatalf("wrong work survived: %+v", surviving)
	}
	ids, err := s.WorkBookIDs(ctx, user.ID, readWork.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("a purged book left its mapping behind: %v", ids)
	}
}

// testReconcileCalibreUnservableBookIsKept is the other half of the
// purge contract. A book Calibre still lists but whose only format this
// server cannot serve — converted to KEPUB, or a file that is simply
// not on the disk — is marked missing and kept, because deleting it
// would throw away the reader's mapping for a book that is still in the
// library and can come back.
func testReconcileCalibreUnservableBookIsKept(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-unservable", store.FolderCalibre)
	now := time.Date(2026, time.February, 2, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-unservable-reader")

	id := int64(11)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &id, RelativePath: "Converted (11)/book.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "sha-convert", Title: "Converted"},
	}, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[id].ID

	work := store.Work{ID: "unservable-work", UserID: user.ID, Title: "Converted", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, work,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-convert", WorkID: work.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-convert"}}, false, now); err != nil {
		t.Fatal(err)
	}

	result := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &id, Unservable: true},
	}, true, now.Add(time.Hour))
	if result.Purged != 0 {
		t.Fatalf("an unservable book was purged: %+v", result)
	}
	if result.Missing != 1 {
		t.Fatalf("an unservable book was not marked missing: %+v", result)
	}
	known := knownByCalibreID(t, s, folder.ID)[id]
	if known.ID != bookID || known.Status != store.BookMissing {
		t.Fatalf("unservable book not kept as missing: %+v", known)
	}
	if known.RelativePath != "Converted (11)/book.epub" {
		t.Fatalf("an unservable observation overwrote what the catalog knew: %+v", known)
	}
	stored, err := s.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "Converted" {
		t.Fatalf("an unservable observation blanked the title: %+v", stored)
	}
	if _, err := s.UserBookWork(ctx, user.ID, bookID); err != nil {
		t.Fatalf("reading mapping lost to an unservable format: %v", err)
	}

	// The format comes back and so does the book.
	back := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &id, RelativePath: "Converted (11)/book.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "sha-convert", Title: "Converted"},
	}, true, now.Add(2*time.Hour))
	if back.Returned != 1 {
		t.Fatalf("a returning unservable book was not reported: %+v", back)
	}
	if knownByCalibreID(t, s, folder.ID)[id].Status != store.BookActive {
		t.Fatalf("book did not come back active")
	}
}

// testReconcileCalibreIncompletePassPurgesNothing keeps the two guards
// in front of the purge exactly where they are in front of the missing
// flag: a pass that did not see everything, or saw nothing at all, is
// not evidence that anything was deleted.
func testReconcileCalibreIncompletePassPurgesNothing(t *testing.T, open OpenFunc) {
	s := open(t)
	folder := MkFolder(t, s, "calibre-guards", store.FolderCalibre)
	now := time.Date(2026, time.February, 3, 0, 0, 0, 0, time.UTC)

	one, two := int64(21), int64(22)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &one, RelativePath: "One (21)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-21", Title: "One"},
		{CalibreID: &two, RelativePath: "Two (22)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-22", Title: "Two"},
	}, true, now)

	incomplete := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &one, RelativePath: "One (21)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-21", Title: "One"},
	}, false, now.Add(time.Hour))
	if incomplete.Purged != 0 {
		t.Fatalf("an incomplete pass purged: %+v", incomplete)
	}
	empty := doReconcile(t, s, folder.ID, nil, true, now.Add(2*time.Hour))
	if empty.Purged != 0 {
		t.Fatalf("a pass that observed nothing purged: %+v", empty)
	}
	if len(knownByCalibreID(t, s, folder.ID)) != 2 {
		t.Fatalf("books were lost to a pass that concluded nothing")
	}
}

// testReconcileCalibreCollectsExistingEmptyWorks covers the broad side of
// ADR-0022's work cleanup. The work need not have been unmapped by this pass:
// complete Calibre reconciliation also drains established bookkeeping left by
// older deletion and replacement behavior. Pending sync work remains protected.
func testReconcileCalibreCollectsExistingEmptyWorks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-orphan-cleanup", store.FolderCalibre)
	now := time.Date(2026, time.February, 3, 12, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-orphan-cleanup-reader")

	id := int64(23)
	observed := []store.ObservedBook{{
		CalibreID: &id, RelativePath: "Kept (23)/book.epub", SizeBytes: 1,
		MTime: now, ContentSHA256: "sha-kept-23", Title: "Kept",
	}}
	doReconcile(t, s, folder.ID, observed, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[id].ID
	mapped := store.Work{
		ID: "kept-mapped-work", UserID: user.ID, Title: "Kept", CreatedAt: now,
	}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, mapped,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-kept-23", WorkID: mapped.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-kept-23"}}, false, now); err != nil {
		t.Fatal(err)
	}

	orphan := store.Work{
		ID: "old-empty-orphan", UserID: user.ID, Title: "Old orphan", CreatedAt: now,
	}
	if err := s.CreateWork(ctx, orphan,
		&store.Edition{UserID: user.ID, SHA256: "sha-new-23", WorkID: orphan.ID},
		[]store.Identifier{{Kind: "sha256", Value: "sha-new-23"}}); err != nil {
		t.Fatal(err)
	}
	pendingID, _, err := s.CreatePendingWork(ctx, user.ID, "pending-orphan-md5")
	if err != nil {
		t.Fatal(err)
	}

	changed := []store.ObservedBook{{
		CalibreID: &id, RelativePath: "Kept (23)/book.epub", SizeBytes: 2,
		MTime: now.Add(time.Minute), ContentSHA256: "sha-new-23", Title: "Kept",
	}}
	result := doReconcile(t, s, folder.ID, changed, true, now.Add(time.Hour))
	if result.Purged != 0 {
		t.Fatalf("cleanup-only pass unexpectedly purged a book: %+v", result)
	}
	if _, err := s.WorkByID(ctx, user.ID, orphan.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("existing empty work survived a complete Calibre pass: %v", err)
	}
	if _, err := s.WorkByID(ctx, user.ID, pendingID); err != nil {
		t.Fatalf("pending sync work was collected: %v", err)
	}
	aliasOwner, err := s.WorkIDByAlias(ctx, user.ID, "sha256", "sha-new-23")
	if err != nil || aliasOwner != mapped.ID {
		t.Fatalf("stale orphan blocked digest registration: %q %v", aliasOwner, err)
	}
	edition, err := s.EditionBySHA(ctx, user.ID, "sha-new-23")
	if err != nil || edition.WorkID != mapped.ID {
		t.Fatalf("new digest edition did not move to mapped work: %+v %v", edition, err)
	}
}

// testReconcilePlainFolderNeverPurges guards the other side of ADR-0022:
// only a Calibre folder has a catalog to be absent from. Under a plain
// folder absence is evidence about a disk — an unmounted share, a
// half-finished copy — and the row is kept.
func testReconcilePlainFolderNeverPurges(t *testing.T, open OpenFunc) {
	s := open(t)
	folder := MkFolder(t, s, "plain-no-purge", store.FolderPlain)
	now := time.Date(2026, time.February, 4, 0, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a", Title: "A"},
		{RelativePath: "b.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-b", Title: "B"},
	}, true, now)

	result := doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a", Title: "A"},
	}, true, now.Add(time.Hour))
	if result.Purged != 0 || result.Missing != 1 {
		t.Fatalf("a plain folder purged instead of marking missing: %+v", result)
	}
	if knownByPath(t, s, folder.ID)["b.epub"].Status != store.BookMissing {
		t.Fatalf("a plain folder's absent book was not kept as missing")
	}
}

// testReconcileCalibreDigestChangeFollowsReader is the fix for the
// duplicate that a Calibre metadata edit used to produce. Calibre
// rewrites the publication when it embeds edited metadata, so the digest
// a device reports next is not the one the reader's work was resolved
// from — and a work that cannot be reached from the new digest is a
// second work for the same book.
func testReconcileCalibreDigestChangeFollowsReader(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-rekey", store.FolderCalibre)
	now := time.Date(2026, time.February, 5, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-rekey-reader")

	id := int64(31)
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		CalibreID: &id, RelativePath: "Old (31)/book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-before", Title: "Old Title",
		Contributors: []store.ObservedContributor{{Name: "A Writer", Role: store.ContributorRoleAuthor}},
	}}, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[id].ID

	work := store.Work{ID: "rekey-work", UserID: user.ID, Title: "Old Title", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, work,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-before", WorkID: work.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-before"}}, false, now); err != nil {
		t.Fatal(err)
	}

	result := doReconcile(t, s, folder.ID, []store.ObservedBook{{
		CalibreID: &id, RelativePath: "New (31)/book.epub", SizeBytes: 2, MTime: now.Add(time.Minute),
		ContentSHA256: "sha-after", Title: "New Title",
		Contributors: []store.ObservedContributor{{Name: "A Writer", Role: store.ContributorRoleAuthor}},
	}}, true, now.Add(time.Hour))
	if result.Rekeyed != 1 {
		t.Fatalf("the reader's work did not follow the rewritten file: %+v", result)
	}

	got, err := s.WorkIDByAlias(ctx, user.ID, "sha256", "sha-after")
	if err != nil || got != work.ID {
		t.Fatalf("new digest does not reach the reader's work: %q %v", got, err)
	}
	// The old digest still works, so a device that has not re-synced is
	// not stranded.
	got, err = s.WorkIDByAlias(ctx, user.ID, "sha256", "sha-before")
	if err != nil || got != work.ID {
		t.Fatalf("old digest stopped reaching the work: %q %v", got, err)
	}
	got, err = s.WorkIDByAlias(ctx, user.ID, "ta", "new title|a writer")
	if err != nil || got != work.ID {
		t.Fatalf("new title/author fingerprint does not reach the work: %q %v", got, err)
	}
	// An edition for the new digest is what an op can hang itself on.
	if _, err := s.AppendOps(ctx, user.ID, "d-rekey", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000bb", WorkID: work.ID,
		EditionSHA: Ptr("sha-after"), ClientTS: now, Progression: 0.5,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatalf("no edition for the rewritten file: %v", err)
	}
}

// testReconcileDigestCollisionChangesNothing is the limit of the rule
// above. When the new digest already names another work, the two works
// meeting is a merge — a decision with reading history on both sides —
// and a scan does not make it on the reader's behalf.
func testReconcileDigestCollisionChangesNothing(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "calibre-collide", store.FolderCalibre)
	now := time.Date(2026, time.February, 6, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "calibre-collide-reader")

	id := int64(41)
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		CalibreID: &id, RelativePath: "Old (41)/book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "collide-before", Title: "Old Title",
	}}, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[id].ID

	work := store.Work{ID: "collide-work", UserID: user.ID, Title: "Old Title", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, work,
		[]store.Edition{{UserID: user.ID, SHA256: "collide-before", WorkID: work.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "collide-before"}}, false, now); err != nil {
		t.Fatal(err)
	}
	// Somebody else's work already holds the digest the file is about to
	// have.
	other := MkWork(t, s, user, "collide-other-work", "collide-after")
	if _, err := s.AppendOps(ctx, user.ID, "d-collide", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000cc", WorkID: other.ID,
		EditionSHA: Ptr("collide-after"), ClientTS: now, Progression: 0.2,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		CalibreID: &id, RelativePath: "New (41)/book.epub", SizeBytes: 2, MTime: now.Add(time.Minute),
		ContentSHA256: "collide-after", Title: "New Title",
	}}, true, now.Add(time.Hour))

	got, err := s.WorkIDByAlias(ctx, user.ID, "sha256", "collide-after")
	if err != nil {
		t.Fatal(err)
	}
	if got != other.ID {
		t.Fatalf("a scan moved a digest off the work that already held it: %q", got)
	}
	mapping, err := s.UserBookWork(ctx, user.ID, bookID)
	if err != nil || mapping.WorkID != work.ID {
		t.Fatalf("the book's own mapping moved: %+v %v", mapping, err)
	}
}

// testReconcilePartialDigestCollisionChangesNothing covers work graphs in
// which only one of the two sha256 records exists. CreateWork deliberately
// permits an edition without an alias and an alias without an edition, so a
// scan must check both owners before adding either missing half.
func testReconcilePartialDigestCollisionChangesNothing(t *testing.T, open OpenFunc) {
	tests := []struct {
		name        string
		withAlias   bool
		withEdition bool
	}{
		{name: "edition owned by another work", withEdition: true},
		{name: "alias owned by another work", withAlias: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			ctx := context.Background()
			folder := MkFolder(t, s, "calibre-partial-collide", store.FolderCalibre)
			now := time.Date(2026, time.February, 7, 0, 0, 0, 0, time.UTC)
			user := MkUser(t, s, "calibre-partial-collide-reader")

			calibreID := int64(50 + i)
			doReconcile(t, s, folder.ID, []store.ObservedBook{{
				CalibreID: &calibreID, RelativePath: "Old/book.epub", SizeBytes: 1,
				MTime: now, ContentSHA256: "partial-before", Title: "Old Title",
			}}, true, now)
			bookID := knownByCalibreID(t, s, folder.ID)[calibreID].ID

			mapped := store.Work{
				ID: "partial-mapped", UserID: user.ID, Title: "Old Title", CreatedAt: now,
			}
			if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, mapped,
				[]store.Edition{{UserID: user.ID, SHA256: "partial-before", WorkID: mapped.ID}},
				[]store.Identifier{{Kind: "sha256", Value: "partial-before"}}, false, now); err != nil {
				t.Fatal(err)
			}

			other := store.Work{
				ID: "partial-other", UserID: user.ID, Title: "Other", CreatedAt: now,
			}
			var edition *store.Edition
			if tt.withEdition {
				edition = &store.Edition{
					UserID: user.ID, SHA256: "partial-after", WorkID: other.ID,
				}
			}
			var aliases []store.Identifier
			if tt.withAlias {
				aliases = []store.Identifier{{Kind: "sha256", Value: "partial-after"}}
			}
			if err := s.CreateWork(ctx, other, edition, aliases); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendOps(ctx, user.ID, "d-partial-collide", []store.Op{{
				OpID: "018e6f1a-0000-7000-8000-0000000000dd", WorkID: other.ID,
				ClientTS: now, Progression: 0.2, Origin: store.OriginNative,
			}}); err != nil {
				t.Fatal(err)
			}

			doReconcile(t, s, folder.ID, []store.ObservedBook{{
				CalibreID: &calibreID, RelativePath: "New/book.epub", SizeBytes: 2,
				MTime: now.Add(time.Minute), ContentSHA256: "partial-after", Title: "New Title",
			}}, true, now.Add(time.Hour))

			aliasOwner, aliasErr := s.WorkIDByAlias(ctx, user.ID, "sha256", "partial-after")
			if tt.withAlias {
				if aliasErr != nil || aliasOwner != other.ID {
					t.Fatalf("digest alias changed owner: %q %v", aliasOwner, aliasErr)
				}
			} else if !errors.Is(aliasErr, store.ErrNotFound) {
				t.Fatalf("scan added an alias despite an edition collision: %q %v", aliasOwner, aliasErr)
			}

			gotEdition, editionErr := s.EditionBySHA(ctx, user.ID, "partial-after")
			if tt.withEdition {
				if editionErr != nil || gotEdition.WorkID != other.ID {
					t.Fatalf("digest edition changed owner: %+v %v", gotEdition, editionErr)
				}
			} else if !errors.Is(editionErr, store.ErrNotFound) {
				t.Fatalf("scan added an edition despite an alias collision: %+v %v", gotEdition, editionErr)
			}

			mapping, err := s.UserBookWork(ctx, user.ID, bookID)
			if err != nil || mapping.WorkID != mapped.ID {
				t.Fatalf("the book's own mapping moved: %+v %v", mapping, err)
			}
		})
	}
}
