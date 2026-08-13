package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testWatchedSourceReconciliation pins the durable half of the watched
// scanner: what a sweep may record, what only a completed sweep may
// conclude, and the fact that source presence is a different axis from
// blob presence.
//
// The scanner's own traversal is tested against a real filesystem in
// internal/content. What has to be proved here is that both backends
// agree on the rules, because these are the statements that decide
// whether a household's books disappear.
func testWatchedSourceReconciliation(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "watched-owner")
	outsider := MkUser(t, s, "watched-outsider")
	now := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)
	root := "/srv/books"
	library := store.Library{
		ID: "lib-watched", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryWatched, Name: "Watched", RootPath: &root,
		CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	// A second library proves the sweep of one never reaches the other.
	otherRoot := "/srv/other"
	other := store.Library{
		ID: "lib-watched-other", OwnerUserID: outsider.ID,
		QuotaUserID: outsider.ID, Kind: store.LibraryWatched,
		Name: "Other", RootPath: &otherRoot, CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, other); err != nil {
		t.Fatal(err)
	}
	managed := store.Library{
		ID: "lib-managed", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Managed", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, managed); err != nil {
		t.Fatal(err)
	}

	mkBook := func(actorID, libraryID, bookID, path, blob string) {
		t.Helper()
		if err := s.CreateCatalogBook(ctx, actorID, store.CatalogBook{
			ID: bookID, LibraryID: libraryID, Status: store.BookActive,
			Title: bookID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		file := store.BookFile{
			ID: bookID + "-file", LibraryID: libraryID, BookID: bookID,
			BlobSHA256: blob, Source: store.IngestWatched,
			MediaType:          "application/epub+zip",
			SourceRelativePath: &path, OriginalFilename: path,
			Availability: store.BookFileAvailable,
			CreatedAt:    now, UpdatedAt: now,
		}
		if err := inserter.InsertBookFileForTest(ctx, file, 100); err != nil {
			t.Fatal(err)
		}
	}
	mkBook(owner.ID, library.ID, "book-kept", "kept.epub", "blob-kept")
	mkBook(owner.ID, library.ID, "book-gone", "gone.epub", "blob-gone")
	mkBook(outsider.ID, other.ID, "book-elsewhere", "kept.epub", "blob-elsewhere")

	// A path lookup is scoped to one library, even when two libraries
	// happen to use the same relative path.
	found, err := s.WatchedFilesByPath(ctx, library.ID, "kept.epub")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].BookID != "book-kept" {
		t.Fatalf("path lookup crossed a library: %+v", found)
	}
	if found[0].SizeBytes != 100 {
		t.Fatalf("expected the blob's size, got %d", found[0].SizeBytes)
	}
	if found[0].SourceModifiedAt != nil || found[0].SourceAbsent {
		t.Fatalf("a file no sweep has seen should carry no observation: %+v",
			found[0])
	}

	// A sweep records what it saw. Only the paths it names are affected.
	sweepStart := now.Add(time.Hour)
	modified := now.Add(-24 * time.Hour)
	seen, err := s.MarkWatchedSourcesSeen(ctx, library.ID,
		[]store.WatchedObservation{{
			SourceRelativePath: "kept.epub",
			SizeBytes:          100,
			ModifiedAt:         modified,
		}}, sweepStart)
	if err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("expected one path recorded, got %d", seen)
	}
	found, err = s.WatchedFilesByPath(ctx, library.ID, "kept.epub")
	if err != nil {
		t.Fatal(err)
	}
	if found[0].SourceModifiedAt == nil ||
		!found[0].SourceModifiedAt.Equal(modified) {
		t.Fatalf("the observed modification time was not recorded: %+v", found[0])
	}

	// A completed sweep concludes that the path it did not see is gone —
	// and only that one, in only that library.
	absent, err := s.MarkWatchedSourcesAbsent(
		ctx, library.ID, sweepStart, sweepStart.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if absent != 1 {
		t.Fatalf("expected one file marked absent, got %d", absent)
	}
	gone, err := s.WatchedFilesByPath(ctx, library.ID, "gone.epub")
	if err != nil {
		t.Fatal(err)
	}
	if !gone[0].SourceAbsent {
		t.Fatal("the unseen path was not marked absent")
	}
	elsewhere, err := s.WatchedFilesByPath(ctx, other.ID, "kept.epub")
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere[0].SourceAbsent {
		t.Fatal("one library's sweep marked another library's file absent")
	}

	// An absent source hides the book even though its blob is present:
	// the two axes are independent, and this is the one the scanner owns.
	reconcileAvailability(t, s, sweepStart.Add(2*time.Minute))
	assertBookStatus(t, s, owner.ID, "book-gone", store.BookMissing)
	assertBookStatus(t, s, owner.ID, "book-kept", store.BookActive)

	// The file returning restores it, without anything touching the blob.
	returning := sweepStart.Add(time.Hour)
	if _, err := s.MarkWatchedSourcesSeen(ctx, library.ID,
		[]store.WatchedObservation{{
			SourceRelativePath: "gone.epub",
			SizeBytes:          100,
			ModifiedAt:         modified,
		}}, returning); err != nil {
		t.Fatal(err)
	}
	reconcileAvailability(t, s, returning.Add(time.Minute))
	assertBookStatus(t, s, owner.ID, "book-gone", store.BookActive)

	// A file created after a sweep began is exempt from that sweep's
	// conclusions: the sweep never proved anything about it.
	late := returning.Add(2 * time.Hour)
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-late", LibraryID: library.ID, Status: store.BookActive,
		Title: "Late", CreatedAt: late.Add(time.Hour),
		UpdatedAt: late.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	latePath := "late.epub"
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "book-late-file", LibraryID: library.ID, BookID: "book-late",
		BlobSHA256: "blob-late", Source: store.IngestWatched,
		MediaType:          "application/epub+zip",
		SourceRelativePath: &latePath, OriginalFilename: latePath,
		Availability: store.BookFileAvailable,
		CreatedAt:    late.Add(time.Hour), UpdatedAt: late.Add(time.Hour),
	}, 100); err != nil {
		t.Fatal(err)
	}
	// The older rows are seen by this sweep, so the only row its
	// conclusion could touch is the one created after it began.
	if _, err := s.MarkWatchedSourcesSeen(ctx, library.ID,
		[]store.WatchedObservation{
			{SourceRelativePath: "kept.epub", SizeBytes: 100, ModifiedAt: modified},
			{SourceRelativePath: "gone.epub", SizeBytes: 100, ModifiedAt: modified},
		}, late.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	marked, err := s.MarkWatchedSourcesAbsent(
		ctx, library.ID, late, late.Add(2*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 0 {
		t.Fatalf("a sweep concluded something about a row it never saw: %d", marked)
	}

	// Review is a queue, and it says why.
	if changed, err := s.SetCatalogBookReview(ctx, library.ID, "book-kept",
		"the file at this path was replaced", late); err != nil || !changed {
		t.Fatalf("set review: %v %v", changed, err)
	}
	inReview, err := s.ListBooksInReview(ctx, owner.ID, library.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(inReview) != 1 || inReview[0].ID != "book-kept" ||
		inReview[0].ReviewReason == "" {
		t.Fatalf("unexpected review queue: %+v", inReview)
	}
	// A book in review is not offered through the ordinary catalog, and
	// the availability pass must leave it alone rather than argue.
	reconcileAvailability(t, s, late.Add(time.Minute))
	assertBookStatus(t, s, owner.ID, "book-kept", store.BookReview)

	// Clearing it returns the book to missing, from where the
	// availability pass — the only thing allowed to decide a book is
	// servable — restores it.
	if changed, err := s.SetCatalogBookReview(
		ctx, library.ID, "book-kept", "", late.Add(time.Hour)); err != nil ||
		!changed {
		t.Fatalf("clear review: %v %v", changed, err)
	}
	reconcileAvailability(t, s, late.Add(2*time.Hour))
	assertBookStatus(t, s, owner.ID, "book-kept", store.BookActive)

	// The review queue is a manage-role view.
	if _, err := s.ListBooksInReview(
		ctx, outsider.ID, library.ID, 10); err == nil {
		t.Fatal("an outsider read another library's review queue")
	}

	// The scanner's library list is a housekeeping query: it reports the
	// instance's watched roots regardless of who owns them, and never a
	// managed library.
	watched, err := s.ListWatchedLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(watched) != 2 {
		t.Fatalf("expected both watched libraries, got %d", len(watched))
	}
	for _, lib := range watched {
		if lib.Kind != store.LibraryWatched {
			t.Fatalf("a managed library was offered to the scanner: %+v", lib)
		}
		if lib.RootPath == nil || *lib.RootPath == "" {
			t.Fatalf("a library with no root was offered to the scanner: %+v", lib)
		}
	}
}

func assertBookStatus(
	t *testing.T, s store.Store, userID, bookID string, want store.BookStatus,
) {
	t.Helper()
	book, err := s.CatalogBookByID(
		context.Background(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatalf("read book %q: %v", bookID, err)
	}
	if book.Status != want {
		t.Fatalf("book %q is %q, want %q", bookID, book.Status, want)
	}
}

// testWatchedStoreRejectsBadInput pins the argument checks, because these
// methods are called by a background job with no user to show an error to:
// a silently accepted nonsense sweep is a sweep that marks the wrong
// things missing.
func testWatchedStoreRejectsBadInput(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)

	if _, err := s.WatchedFilesByPath(ctx, "", "book.epub"); err == nil {
		t.Fatal("expected a blank library to be refused")
	}
	if _, err := s.WatchedFilesByPath(ctx, "lib", ""); err == nil {
		t.Fatal("expected a blank path to be refused")
	}
	if _, err := s.MarkWatchedSourcesSeen(
		ctx, "lib", nil, now); err == nil {
		t.Fatal("expected an empty observation batch to be refused")
	}
	if _, err := s.MarkWatchedSourcesSeen(ctx, "lib",
		[]store.WatchedObservation{{
			SourceRelativePath: "", SizeBytes: 1, ModifiedAt: now,
		}}, now); err == nil {
		t.Fatal("expected a pathless observation to be refused")
	}
	oversized := make([]store.WatchedObservation, store.MaxWatchedObservationBatch+1)
	for i := range oversized {
		oversized[i] = store.WatchedObservation{
			SourceRelativePath: "book.epub", SizeBytes: 1, ModifiedAt: now,
		}
	}
	if _, err := s.MarkWatchedSourcesSeen(
		ctx, "lib", oversized, now); err == nil {
		t.Fatal("expected an unbounded observation batch to be refused")
	}
	if _, err := s.MarkWatchedSourcesAbsent(
		ctx, "lib", time.Time{}, now, 10); err == nil {
		t.Fatal("expected a sweep with no start time to be refused")
	}
	if _, err := s.MarkWatchedSourcesAbsent(
		ctx, "lib", now, now, 0); err == nil {
		t.Fatal("expected an unbounded absence pass to be refused")
	}
	if _, err := s.SetCatalogBookReview(
		ctx, "lib", "", "why", now); err == nil {
		t.Fatal("expected a review of no book to be refused")
	}
}

// reconcileAvailability runs the availability pass to convergence. The
// production loop lives in internal/content, which is Linux-only; this is
// the same loop, so that a backend test can assert on what a book looks
// like after the pass rather than after one bounded batch of it.
func reconcileAvailability(t *testing.T, s store.Store, at time.Time) {
	t.Helper()
	for i := 0; i < 100; i++ {
		result, err := s.ReconcileCatalogAvailability(
			context.Background(), at, 100)
		if err != nil {
			t.Fatalf("reconcile availability: %v", err)
		}
		if !result.Changed() {
			return
		}
	}
	t.Fatal("catalog availability did not converge")
}
