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

	books, err := s.ListCatalogBooks(context.Background(), folder.ID, nil, 50)
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

	books, err = s.ListCatalogBooks(context.Background(), folder.ID, nil, 50)
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
	got, err := s.CatalogBookByID(ctx, bookID)
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
	returning := store.ObservedBook{
		RelativePath: "gone.epub", SizeBytes: 10, MTime: now,
		ContentSHA256: "sha-gone", Title: "Comes And Goes",
	}
	result = doReconcile(t, s, folder.ID, []store.ObservedBook{returning}, true, returnedAt)
	if result.Returned != 1 {
		t.Fatalf("want one returned book, got %+v", result)
	}
	got, err = s.CatalogBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.BookActive {
		t.Fatalf("book not returned to active: %+v", got)
	}
	if got.AbsentAt != nil {
		t.Fatalf("AbsentAt not cleared: %+v", got.AbsentAt)
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
	got, err := s.CatalogBookByID(ctx, bID)
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
	got, err := s.CatalogBookByID(ctx, bookID)
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
	if _, err := s.CatalogBookByID(ctx, oldBookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old book row survived a replacement: %v", err)
	}
	if _, err := s.UserBookWork(ctx, user.ID, oldBookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reading mapping survived a content replacement: %v", err)
	}
	newBook, err := s.CatalogBookByID(ctx, newBookID)
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

	got, err := s.CatalogBookByID(ctx, bookID)
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
	got, err := s.CatalogBookByID(ctx, bookID)
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
