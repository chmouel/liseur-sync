package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testFolders covers folder CRUD: the unique root path, cursor paging
// by name then id, and DeleteFolder's cascade over every catalog row
// that hung off it.
func testFolders(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.CreateFolder(ctx, store.Folder{
		ID: "folders-a", Name: "Same Root", RootPath: "/srv/dup",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFolder(ctx, store.Folder{
		ID: "folders-b", Name: "Also Same Root", RootPath: "/srv/dup",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate root path: want ErrConflict, got %v", err)
	}
	if err := s.CreateFolder(ctx, store.Folder{
		ID: "folders-bad-kind", Name: "Bad Kind", RootPath: "/srv/bad",
		Kind: store.FolderKind("nonsense"), CreatedAt: now, UpdatedAt: now,
	}); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("invalid folder kind: want ErrInvalidInput, got %v", err)
	}

	got, err := s.FolderByID(ctx, "", "folders-a")
	if err != nil || got.RootPath != "/srv/dup" {
		t.Fatalf("FolderByID: %+v %v", got, err)
	}
	if _, err := s.FolderByID(ctx, "", "no-such-folder"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing folder: want ErrNotFound, got %v", err)
	}

	for i, name := range []string{"zzz", "mmm", "aaa"} {
		id := fmt.Sprintf("folders-page-%d", i)
		if err := s.CreateFolder(ctx, store.Folder{
			ID: id, Name: name, RootPath: "/srv/page/" + id,
			Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	cursor := ""
	for {
		page, err := s.ListFolders(ctx, "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, f := range page {
			names = append(names, f.Name)
		}
		cursor = store.FolderCursor(page[len(page)-1])
		if len(page) < 2 {
			break
		}
	}
	want := []string{"Same Root", "aaa", "mmm", "zzz"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("folder paging: got %v want %v", names, want)
	}

	obs := store.ObservedBook{
		RelativePath: "book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-delete-cascade", Title: "Cascade Me",
	}
	doReconcile(t, s, "folders-a", []store.ObservedBook{obs}, true, now)
	bookID := knownByPath(t, s, "folders-a")["book.epub"].ID

	if err := s.DeleteFolder(ctx, "folders-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, "folders-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, "", bookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("book survived its folder's deletion: %v", err)
	}
}

// testCatalogListingsPageAndIsolate covers cursor determinism in both
// directions and that one folder's listing never leaks another's books.
// A missing book leaks in neither direction: it is hidden from the
// listing, because the server would refuse to serve it, but its row
// survives so a reader's work mapping still has something to point at.
func testCatalogListingsPageAndIsolate(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "catalog-page", store.FolderPlain)
	other := MkFolder(t, s, "catalog-page-other", store.FolderPlain)
	base := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)

	// Each pass observes everything catalogued so far, so the books get
	// distinct creation times without any of them going missing on the
	// way: a complete pass that does not observe a book marks it missing.
	titles := []string{"c", "a", "b", "later", "last"}
	var present []store.ObservedBook
	for i, title := range titles {
		present = append(present, store.ObservedBook{
			RelativePath: title + ".epub", SizeBytes: 1, MTime: base,
			ContentSHA256: "sha-" + title, Title: title,
		})
		doReconcile(t, s, folder.ID, present, true,
			base.Add(time.Duration(i)*time.Second))
	}
	// One book that gets marked missing. It must drop out of the listing
	// without dropping out of the catalog.
	missingObs := store.ObservedBook{
		RelativePath: "temporarily-away.epub", SizeBytes: 1, MTime: base,
		ContentSHA256: "sha-away", Title: "Away",
	}
	withAway := append(append([]store.ObservedBook{}, present...), missingObs)
	doReconcile(t, s, folder.ID, withAway, true, base.Add(10*time.Second))
	awayID := knownByPath(t, s, folder.ID)["temporarily-away.epub"].ID
	if awayID == "" {
		t.Fatal("the book that is about to go missing was never catalogued")
	}
	doReconcile(t, s, folder.ID, present, true, base.Add(11*time.Second))

	// A book in the other folder must never appear in this folder's
	// listing, however it pages.
	doReconcile(t, s, other.ID, []store.ObservedBook{{
		RelativePath: "elsewhere.epub", SizeBytes: 1, MTime: base,
		ContentSHA256: "sha-elsewhere", Title: "Elsewhere",
	}}, true, base)

	var got []string
	var cursor *store.CatalogBookCursor
	for {
		page, err := s.ListCatalogBooks(ctx, "", folder.ID, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("page exceeded limit: %+v", page)
		}
		for _, book := range page {
			if book.FolderID != folder.ID {
				t.Fatalf("listing leaked a book from another folder: %+v", book)
			}
			if book.Status == store.BookMissing {
				t.Fatalf("listing offered a book the server will not serve: %+v", book)
			}
			got = append(got, book.Title)
		}
		last := page[len(page)-1]
		cursor = &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		if len(page) < 2 {
			break
		}
	}
	want := []string{"c", "a", "b", "later", "last"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("paged books: got %v want %v", got, want)
	}

	// Hidden, not deleted: the row is still there to be read by id, so a
	// reader's work mapping still has something to point at.
	away, err := s.CatalogBookByID(ctx, "", awayID)
	if err != nil {
		t.Fatalf("a missing book was deleted rather than hidden: %v", err)
	}
	if away.Status != store.BookMissing {
		t.Fatalf("away book status: got %q want %q", away.Status, store.BookMissing)
	}

	// Newest first must be the same set in the opposite order.
	var recent []string
	cursor = nil
	for {
		page, err := s.ListRecentCatalogBooks(ctx, "", folder.ID, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, book := range page {
			recent = append(recent, book.Title)
		}
		last := page[len(page)-1]
		cursor = &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		if len(page) < 2 {
			break
		}
	}
	reversed := make([]string, len(want))
	for i, v := range want {
		reversed[len(want)-1-i] = v
	}
	if fmt.Sprint(recent) != fmt.Sprint(reversed) {
		t.Fatalf("recent books: got %v want %v", recent, reversed)
	}

	if _, err := s.CatalogBookByID(ctx, "", "no-such-book"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing book id: want ErrNotFound, got %v", err)
	}
}

// testAvailableBookMediaTypes covers the batch read a feed uses to
// advertise what it actually holds: distinct types, active books only,
// scoped to the one folder asked about.
func testAvailableBookMediaTypes(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "media-types", store.FolderPlain)
	other := MkFolder(t, s, "media-types-other", store.FolderPlain)
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-a",
			Title: "A", MediaType: "application/epub+zip"},
		{RelativePath: "b.pdf", SizeBytes: 1, MTime: now, ContentSHA256: "sha-b",
			Title: "B", MediaType: "application/pdf"},
		{RelativePath: "gone.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-gone",
			Title: "Gone", MediaType: "application/epub+zip"},
	}, true, now)
	// Only "gone.epub" disappears, so only its type would be missed if
	// the other epub in the folder were not there too.
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{RelativePath: "a.epub", SizeBytes: 1, MTime: now, Unchanged: true},
		{RelativePath: "b.pdf", SizeBytes: 1, MTime: now, Unchanged: true},
	}, true, now.Add(time.Hour))

	doReconcile(t, s, other.ID, []store.ObservedBook{
		{RelativePath: "c.cbz", SizeBytes: 1, MTime: now, ContentSHA256: "sha-c",
			Title: "C", MediaType: "application/vnd.comicbook+zip"},
	}, true, now)

	got, err := s.AvailableBookMediaTypes(ctx, "", folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"application/epub+zip", "application/pdf"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("media types: got %v want %v", got, want)
	}
}

// testCatalogAuthorsForBooks covers the identity-backfill read: order
// comes from the book, a role other than author is not credited as one,
// and an outsider's book id resolves to nothing rather than an error.
func testCatalogAuthorsForBooks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "authors", store.FolderPlain)
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{
			RelativePath: "pair.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-pair",
			Title: "Pair",
			// Billed second first, to prove the order comes from the book.
			Contributors: []store.ObservedContributor{
				{Name: "William Gibson", Role: store.ContributorRoleAuthor, Position: 1},
				{Name: "Bruce Sterling", Role: store.ContributorRoleAuthor, Position: 2},
			},
		},
		{
			RelativePath: "translated.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-translated",
			Title: "Translated",
			Contributors: []store.ObservedContributor{
				{Name: "Anthea Bell", Role: "translator", Position: 1},
			},
		},
		{
			RelativePath: "anonymous.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-anon",
			Title: "Anonymous",
		},
	}, true, now)

	known := knownByPath(t, s, folder.ID)
	ids := []string{
		known["pair.epub"].ID, known["translated.epub"].ID,
		known["anonymous.epub"].ID, "no-such-book",
	}
	got, err := s.CatalogAuthorsForBooks(ctx, "", ids)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"William Gibson", "Bruce Sterling"}; fmt.Sprint(got[known["pair.epub"].ID]) != fmt.Sprint(want) {
		t.Fatalf("co-authors: got %v want %v", got[known["pair.epub"].ID], want)
	}
	if len(got[known["translated.epub"].ID]) != 0 {
		t.Fatalf("a translator was credited as an author: %v", got[known["translated.epub"].ID])
	}
	if len(got[known["anonymous.epub"].ID]) != 0 {
		t.Fatalf("a book nobody is credited with got a name: %v", got[known["anonymous.epub"].ID])
	}
	if _, ok := got["no-such-book"]; ok {
		t.Fatalf("an unknown book id resolved to something: %v", got["no-such-book"])
	}

	empty, err := s.CatalogAuthorsForBooks(ctx, "", nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty request: %v %v", empty, err)
	}
}

// testCatalogBookRelationsForBooks covers the batched read every
// catalog payload is drawn from (ADR-0015): a whole page's contributors
// and series in one round trip each, keyed by book id.
func testCatalogBookRelationsForBooks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "relations", store.FolderPlain)
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{
			RelativePath: "book-one.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-one",
			Title: "Book One",
			Contributors: []store.ObservedContributor{
				{Name: "Ann Leckie", Role: store.ContributorRoleAuthor, Position: 1},
			},
			Series: []store.ObservedSeries{{Name: "Imperial Radch", Position: Ptr(1.0)}},
		},
		{
			RelativePath: "book-two.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-two",
			Title:  "Book Two",
			Series: []store.ObservedSeries{{Name: "Imperial Radch", Position: Ptr(2.0)}},
		},
		{
			RelativePath: "standalone.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-standalone",
			Title: "Standalone",
		},
	}, true, now)

	known := knownByPath(t, s, folder.ID)
	oneID, twoID := known["book-one.epub"].ID, known["book-two.epub"].ID
	standaloneID := known["standalone.epub"].ID

	relations, err := s.CatalogBookRelationsForBooks(ctx, anyReader, []string{oneID, twoID, standaloneID})
	if err != nil {
		t.Fatal(err)
	}
	if len(relations.Contributors[oneID]) != 1 || relations.Contributors[oneID][0].Name != "Ann Leckie" {
		t.Fatalf("book one contributors: %+v", relations.Contributors[oneID])
	}
	if len(relations.Contributors[standaloneID]) != 0 {
		t.Fatalf("standalone book has contributors: %+v", relations.Contributors[standaloneID])
	}
	if len(relations.Series[oneID]) != 1 || relations.Series[oneID][0].Name != "Imperial Radch" {
		t.Fatalf("book one series: %+v", relations.Series[oneID])
	}
	if relations.Series[oneID][0].SeriesID != relations.Series[twoID][0].SeriesID {
		t.Fatalf("same series name resolved to two entities: %+v vs %+v",
			relations.Series[oneID][0], relations.Series[twoID][0])
	}
	if *relations.Series[twoID][0].Position != 2.0 {
		t.Fatalf("book two series position: %+v", relations.Series[twoID][0])
	}

	empty, err := s.CatalogBookRelationsForBooks(ctx, anyReader, nil)
	if err != nil || len(empty.Contributors) != 0 || len(empty.Series) != 0 {
		t.Fatalf("empty request: %+v %v", empty, err)
	}
}

// testCatalogSeriesVolumesForBooks pins the Android shelf rule: a book
// is filed under its first effective series, and expanding that seed
// returns the whole active pile across every folder the reader may see.
func testCatalogSeriesVolumesForBooks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	first := MkFolder(t, s, "series-piles-one", store.FolderPlain)
	second := MkFolder(t, s, "series-piles-two", store.FolderPlain)
	reader := MkUser(t, s, "series-piles-reader")
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)

	doReconcile(t, s, first.ID, []store.ObservedBook{
		{
			RelativePath: "one.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "pile-one", Title: "One",
			Series: []store.ObservedSeries{
				{Name: "Zeta", Position: Ptr(1.0)},
				{Name: "Alpha", Position: Ptr(10.0)},
			},
		},
		{
			RelativePath: "two.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "pile-two", Title: "Two",
			Series: []store.ObservedSeries{{Name: "Alpha", Position: Ptr(2.0)}},
		},
		{
			RelativePath: "single.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "pile-single", Title: "Single",
			Series: []store.ObservedSeries{{Name: "Beta", Position: Ptr(1.0)}},
		},
	}, true, now)
	doReconcile(t, s, second.ID, []store.ObservedBook{{
		RelativePath: "elsewhere.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "pile-elsewhere", Title: "Elsewhere",
		Series: []store.ObservedSeries{{Name: "Alpha", Position: Ptr(3.0)}},
	}}, true, now)

	known := knownByPath(t, s, first.ID)
	one, two, single := known["one.epub"].ID, known["two.epub"].ID,
		known["single.epub"].ID
	volumes, err := s.CatalogSeriesVolumesForBooks(
		ctx, reader.ID, first.ID, []string{one})
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 3 {
		t.Fatalf("Alpha pile: got %+v", volumes)
	}
	if volumes[0].SeriesName != "Alpha" || volumes[0].BookID != two ||
		volumes[1].BookID != knownByPath(t, s, second.ID)["elsewhere.epub"].ID ||
		volumes[2].BookID != one {
		t.Fatalf("primary series or volume order: %+v", volumes)
	}
	if volumes[0].Position == nil || *volumes[0].Position != 2 ||
		volumes[1].Position == nil || *volumes[1].Position != 3 ||
		volumes[2].Position == nil || *volumes[2].Position != 10 {
		t.Fatalf("positions were not preserved: %+v", volumes)
	}
	if err := s.UnassignUserFolder(ctx, reader.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	volumes, err = s.CatalogSeriesVolumesForBooks(
		ctx, reader.ID, first.ID, []string{one})
	if err != nil || len(volumes) != 2 {
		t.Fatalf("ungranted folder leaked into pile: %+v %v", volumes, err)
	}
	if err := s.AssignUserFolder(ctx, reader.ID, second.ID); err != nil {
		t.Fatal(err)
	}

	// A one-book series is returned so presentation can leave it as a
	// single.
	volumes, err = s.CatalogSeriesVolumesForBooks(
		ctx, reader.ID, first.ID, []string{single})
	if err != nil || len(volumes) != 1 || volumes[0].BookID != single {
		t.Fatalf("single-volume series: %+v %v", volumes, err)
	}

	// Personal claims are resolved before grouping and never change the
	// shelf another reader sees.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, one,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Mine", Position: Ptr(4.0)}},
		store.SeriesClaimMutation{At: now.Add(time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	volumes, err = s.CatalogSeriesVolumesForBooks(
		ctx, reader.ID, first.ID, []string{one})
	if err != nil || len(volumes) != 1 || volumes[0].SeriesName != "Mine" {
		t.Fatalf("personal primary series: %+v %v", volumes, err)
	}
	volumes, err = s.CatalogSeriesVolumesForBooks(
		ctx, anyReader, first.ID, []string{one})
	if err != nil || len(volumes) != 3 || volumes[0].SeriesName != "Alpha" {
		t.Fatalf("personal claim leaked: %+v %v", volumes, err)
	}

	empty, err := s.CatalogSeriesVolumesForBooks(ctx, reader.ID, first.ID, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty request: %+v %v", empty, err)
	}
}

// testCatalogSeriesSourceIsPerReader covers who a claim belongs to. The
// shared scope is stored under the empty user id, which is also the user
// id of a caller who has not signed in — so a reader with no account must
// not be handed the shared claim as though it were their own personal one.
func testCatalogSeriesSourceIsPerReader(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "claim-scope", store.FolderPlain)
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	admin := MkUser(t, s, "claim-scope-admin")
	reader := MkUser(t, s, "claim-scope-reader")

	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-claim-scope", Title: "Book",
	}}, true, now)
	bookID := knownByPath(t, s, folder.ID)["book.epub"].ID

	if _, err := s.SetBookSeriesOverride(ctx, admin.ID, bookID,
		store.SeriesSourceShared,
		[]store.SeriesClaimItem{{Name: "Corrected", Position: Ptr(1.0)}},
		store.SeriesClaimMutation{At: now},
	); err != nil {
		t.Fatal(err)
	}

	// Signed out: the shared claim is in force, but it is nobody's own.
	relations, err := s.CatalogBookRelationsForBooks(ctx, anyReader, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	if got := relations.SeriesSource[bookID]; got != store.SeriesSourceShared {
		t.Fatalf("series source for a signed-out reader: %q", got)
	}
	if relations.SeriesClaimUpdatedAt[bookID] == nil {
		t.Fatal("the shared claim reported no revision")
	}

	// A reader with no claim of their own sees the same shared answer.
	relations, err = s.CatalogBookRelationsForBooks(ctx, reader.ID, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	if got := relations.SeriesSource[bookID]; got != store.SeriesSourceShared {
		t.Fatalf("series source before a personal claim: %q", got)
	}

	// Once they disagree, the claim is theirs and says so.
	if _, err := s.SetBookSeriesOverride(ctx, reader.ID, bookID,
		store.SeriesSourcePersonal,
		[]store.SeriesClaimItem{{Name: "Mine", Position: Ptr(2.0)}},
		store.SeriesClaimMutation{At: now.Add(time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	relations, err = s.CatalogBookRelationsForBooks(ctx, reader.ID, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	if got := relations.SeriesSource[bookID]; got != store.SeriesSourcePersonal {
		t.Fatalf("series source after a personal claim: %q", got)
	}
	// And the reader next door is untouched by it.
	relations, err = s.CatalogBookRelationsForBooks(ctx, anyReader, []string{bookID})
	if err != nil {
		t.Fatal(err)
	}
	if got := relations.SeriesSource[bookID]; got != store.SeriesSourceShared {
		t.Fatalf("another reader saw a personal claim: %q", got)
	}
}

// testUserBookWorkIsPerUser covers ADR-0017's privacy boundary: the
// catalog book is shared by every account, but the link to a reader's
// own work is never visible to another account, and resolving is an
// idempotent first-write-wins upsert rather than a reassignment.
func testUserBookWorkIsPerUser(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "shared-book", store.FolderPlain)
	now := time.Date(2026, time.August, 15, 10, 0, 0, 0, time.UTC)
	alice := MkUser(t, s, "shared-book-alice")
	bob := MkUser(t, s, "shared-book-bob")

	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "shared.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-shared", Title: "Shared",
	}}, true, now)
	bookID := knownByPath(t, s, folder.ID)["shared.epub"].ID

	// Both accounts can read the one shared catalog row.
	if _, err := s.CatalogBookByID(ctx, "", bookID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UserBookWork(ctx, alice.ID, bookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("mapping exists before it is resolved: %v", err)
	}

	aliceWork := store.Work{ID: "shared-book-alice-work", UserID: alice.ID, Title: "Shared", CreatedAt: now}
	result, err := s.ResolveCatalogBookWork(ctx, alice.ID, bookID, aliceWork,
		[]store.Edition{{UserID: alice.ID, SHA256: "shared-book-alice-sha", WorkID: aliceWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "shared-book-alice-sha"}},
		false, now)
	if err != nil || !result.Created || result.WorkID != aliceWork.ID || result.Confidence != "high" {
		t.Fatalf("resolve: %+v %v", result, err)
	}
	mapping, err := s.UserBookWork(ctx, alice.ID, bookID)
	if err != nil || mapping.WorkID != aliceWork.ID {
		t.Fatalf("mapping not recorded: %+v %v", mapping, err)
	}
	// A second, unrelated proposed work must not steal the mapping once
	// the same identifier resolves again: the first resolution wins.
	again, err := s.ResolveCatalogBookWork(ctx, alice.ID, bookID,
		store.Work{ID: "shared-book-alice-unused", UserID: alice.ID, CreatedAt: now}, nil,
		[]store.Identifier{{Kind: "sha256", Value: "shared-book-alice-sha"}},
		false, now)
	if err != nil || again.Created || again.WorkID != aliceWork.ID {
		t.Fatalf("resolve is not idempotent: %+v %v", again, err)
	}

	bobWork := store.Work{ID: "shared-book-bob-work", UserID: bob.ID, Title: "Shared", CreatedAt: now}
	bobResult, err := s.ResolveCatalogBookWork(ctx, bob.ID, bookID, bobWork,
		[]store.Edition{{UserID: bob.ID, SHA256: "shared-book-bob-sha", WorkID: bobWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "shared-book-bob-sha"}},
		false, now)
	if err != nil || !bobResult.Created || bobResult.WorkID != bobWork.ID {
		t.Fatalf("bob resolve: %+v %v", bobResult, err)
	}
	if bobResult.WorkID == result.WorkID {
		t.Fatalf("two users' mappings for the same book collapsed to one work")
	}

	if _, err := s.UserBookWork(ctx, bob.ID, bookID); err != nil {
		t.Fatalf("bob cannot read his own mapping: %v", err)
	}
	aliceIDs, err := s.WorkBookIDs(ctx, alice.ID, aliceWork.ID)
	if err != nil || fmt.Sprint(aliceIDs) != fmt.Sprint([]string{bookID}) {
		t.Fatalf("WorkBookIDs: %v %v", aliceIDs, err)
	}
	bobIDs, err := s.WorkBookIDs(ctx, bob.ID, aliceWork.ID)
	if err != nil || len(bobIDs) != 0 {
		t.Fatalf("bob sees books mapped to alice's work: %v %v", bobIDs, err)
	}

	if _, err := s.ResolveCatalogBookWork(ctx, alice.ID, "no-such-book", aliceWork,
		nil, nil, false, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("resolving a nonexistent book: want ErrNotFound, got %v", err)
	}
}
