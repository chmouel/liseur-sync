package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testDuplicateContentIsReportedNotResolved covers duplicate detection.
// Uploading the same file twice is allowed on purpose, so the server owes
// the user the one thing it can say for certain: these two entries are
// the same bytes. It must say it without deciding which one to keep.
func testDuplicateContentIsReportedNotResolved(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "dup-owner")
	reader := MkUser(t, s, "dup-reader")
	outsider := MkUser(t, s, "dup-outsider")
	now := time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-dup", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Duplicates", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	// A second library holding the very same bytes: content is
	// deduplicated across the whole store, so a blob shared with somewhere
	// the user cannot see must not be reported as a duplicate here.
	other := store.Library{
		ID: "lib-dup-other", OwnerUserID: outsider.ID, QuotaUserID: outsider.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Elsewhere", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now,
	); err != nil {
		t.Fatal(err)
	}

	shared := ingestBlob("dup-shared", 40)
	lonely := ingestBlob("dup-lonely", 41)
	add := func(bookID, title string, blob store.BlobInfo, libraryID string,
		status store.BookStatus, at time.Time,
	) {
		t.Helper()
		actor := owner.ID
		if libraryID == other.ID {
			actor = outsider.ID
		}
		if err := s.CreateCatalogBook(ctx, actor, store.CatalogBook{
			ID: bookID, LibraryID: libraryID, Status: store.BookActive,
			Title: title, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: "file-" + bookID, LibraryID: libraryID, BookID: bookID,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: title + ".epub", MediaType: "application/epub+zip",
			Availability: store.BookFileAvailable, CreatedAt: at, UpdatedAt: at,
		}, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		if status == store.BookTrashed {
			expires := at.Add(48 * time.Hour)
			if _, err := s.TrashCatalogBook(
				ctx, owner.ID, bookID, at, expires,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	add("book-dup-a", "Morning Star", shared, library.ID,
		store.BookActive, now)
	add("book-dup-b", "Morning Star (again)", shared, library.ID,
		store.BookActive, now.Add(time.Minute))
	add("book-dup-alone", "Only Copy", lonely, library.ID,
		store.BookActive, now.Add(2*time.Minute))
	add("book-dup-elsewhere", "Someone Else's", shared, other.ID,
		store.BookActive, now.Add(3*time.Minute))

	duplicates, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicates) != 2 {
		t.Fatalf("duplicates = %+v, want the two books sharing bytes", duplicates)
	}
	if duplicates[0].Book.ID != "book-dup-a" ||
		duplicates[1].Book.ID != "book-dup-b" {
		t.Fatalf("duplicates are not grouped oldest first: %+v", duplicates)
	}
	for _, duplicate := range duplicates {
		if duplicate.SHA256 != shared.SHA256 {
			t.Fatalf("duplicate %q reported under %q, want %q",
				duplicate.Book.ID, duplicate.SHA256, shared.SHA256)
		}
	}

	// A reader may see the coincidence; resolving it is a deletion, which
	// read access cannot do. Someone with no access sees nothing at all.
	if readerView, err := s.ListDuplicateContentBooks(
		ctx, reader.ID, library.ID, 50,
	); err != nil || len(readerView) != 2 {
		t.Fatalf("reader view = %+v, err = %v", readerView, err)
	}
	if _, err := s.ListDuplicateContentBooks(
		ctx, outsider.ID, library.ID, 50,
	); err == nil {
		t.Fatal("an outsider listed another library's duplicates")
	}

	// Deleting one copy resolves it: the trashed book keeps its file, so
	// the pair would still look shared to a query that forgot to exclude
	// it, and the user would be told to resolve what they just resolved.
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "book-dup-b", now.Add(time.Hour),
		now.Add(49*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	after, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("deleting one copy left duplicates reported: %+v", after)
	}

	// Restoring brings the coincidence back, because it brings the second
	// entry back.
	if _, err := s.RestoreCatalogBook(
		ctx, owner.ID, "book-dup-b", now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	restored, err := s.ListDuplicateContentBooks(ctx, owner.ID, library.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("restore did not bring the duplicate back: %+v", restored)
	}

	for _, limit := range []int{0, -1, 501} {
		if _, err := s.ListDuplicateContentBooks(
			ctx, owner.ID, library.ID, limit,
		); err == nil {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
}

// testSimilarBooksAreAskedAboutNotAsserted covers ADR-0010 phase 2. The
// weaker report has to be right often enough that a librarian keeps
// reading it, so each case below is one somebody could reasonably expect
// it to get wrong.
func testSimilarBooksAreAskedAboutNotAsserted(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "similar-owner")
	outsider := MkUser(t, s, "similar-outsider")
	now := time.Date(2026, time.November, 4, 9, 0, 0, 0, time.UTC)
	library := store.Library{
		ID: "lib-similar", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Similar", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	add := func(bookID, title string, blob store.BlobInfo, fill func(*store.BookMetadata)) {
		t.Helper()
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: bookID, LibraryID: library.ID, Status: store.BookActive,
			Title: title, TitleSource: store.MetadataEmbedded,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
			ID: "file-" + bookID, LibraryID: library.ID, BookID: bookID,
			BlobSHA256: blob.SHA256, Source: store.IngestUpload,
			OriginalFilename: bookID + ".epub", MediaType: "application/epub+zip",
			Availability: store.BookFileAvailable, CreatedAt: now, UpdatedAt: now,
		}, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
		if fill == nil {
			return
		}
		meta, err := s.CatalogBookMetadata(ctx, owner.ID, bookID, store.LibraryRoleRead)
		if err != nil {
			t.Fatal(err)
		}
		fill(&meta)
		if _, err := s.ApplyCatalogBookMetadata(ctx, owner.ID,
			store.ApplyBookMetadataRequest{
				Metadata: meta, ExpectedRevision: meta.Book.Revision, UpdatedAt: now,
			}); err != nil {
			t.Fatal(err)
		}
	}
	herbert := func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{{
			ContributorID: "sim-herbert", Name: "Frank Herbert",
			NormalizedName: "frank herbert", Role: "author",
			Source: store.MetadataEmbedded,
		}}
	}

	// Two builds of one novel: different bytes, one author, titles that
	// differ only in the ways the rule folds away.
	add("sim-a", "Dune", ingestBlob("sim-a", 40), herbert)
	add("sim-b", "DUNE!", ingestBlob("sim-b", 41), herbert)
	// The same title by somebody else. Title alone would group these,
	// which is exactly the report a librarian learns to ignore.
	add("sim-other-author", "Dune", ingestBlob("sim-c", 42), func(m *store.BookMetadata) {
		m.Contributors = []store.BookContributor{{
			ContributorID: "sim-someone", Name: "Someone Else",
			NormalizedName: "someone else", Role: "author",
			Source: store.MetadataEmbedded,
		}}
	})
	// Two numbered volumes sharing a series name, which is the failure
	// the ADR named when it deferred this.
	volume := func(position float64) func(*store.BookMetadata) {
		return func(m *store.BookMetadata) {
			herbert(m)
			p := position
			m.Series = []store.BookSeries{{
				SeriesID: "sim-series", Name: "Chronicles",
				NormalizedName: "chronicles", Position: &p,
				Source: store.MetadataEmbedded,
			}}
		}
	}
	add("sim-vol-1", "Chronicles", ingestBlob("sim-d", 43), volume(1))
	add("sim-vol-2", "Chronicles", ingestBlob("sim-e", 44), volume(2))
	// Two books that are the same bytes are already reported exactly, so
	// saying it again here would only make this report look unreliable.
	sameBytes := ingestBlob("sim-same", 45)
	add("sim-exact-1", "Emile", sameBytes, herbert)
	add("sim-exact-2", "Émile", sameBytes, herbert)

	groups, err := s.ListSimilarBooks(ctx, owner.ID, library.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("similar groups = %+v, want exactly the two builds", groups)
	}
	ids := map[string]bool{}
	for _, b := range groups[0].Books {
		ids[b.ID] = true
	}
	if len(ids) != 2 || !ids["sim-a"] || !ids["sim-b"] {
		t.Fatalf("group members = %v", ids)
	}
	if groups[0].Title != "dune" {
		t.Fatalf("group title = %q, want the folded form", groups[0].Title)
	}

	// Trashing one member resolves the group, exactly as it does for the
	// exact report: a book the librarian deleted is not a question.
	if _, err := s.TrashCatalogBook(
		ctx, owner.ID, "sim-b", now, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if groups, err := s.ListSimilarBooks(ctx, owner.ID, library.ID, 100); err != nil ||
		len(groups) != 0 {
		t.Fatalf("after trashing: %+v %v", groups, err)
	}

	if _, err := s.ListSimilarBooks(ctx, outsider.ID, library.ID, 100); err != store.ErrNotFound {
		t.Fatalf("outsider read another library's duplicates: %v", err)
	}
	for _, limit := range []int{0, -1, 501} {
		if _, err := s.ListSimilarBooks(
			ctx, owner.ID, library.ID, limit); err != store.ErrInvalidTransition {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}
