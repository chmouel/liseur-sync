package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testAtomicCatalogWorkResolution(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "catalog-concurrent")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib-concurrent", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Concurrent", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{
		ID: "book-concurrent", LibraryID: library.ID, Status: store.BookActive,
		Title: "Concurrent", TitleSource: store.MetadataEmbedded, CreatedAt: now,
	}
	if err := s.CreateCatalogBook(ctx, user.ID, book); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	start := make(chan struct{})
	results := make(chan store.WorkResolution, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			workID := fmt.Sprintf("catalog-candidate-%02d", i)
			result, err := s.ResolveCatalogBookWork(ctx, user.ID, book.ID,
				store.Work{ID: workID, UserID: user.ID, Title: book.Title, CreatedAt: now},
				[]store.Edition{{
					UserID: user.ID, SHA256: "catalog-concurrent-sha", WorkID: workID,
				}},
				[]store.Identifier{{Kind: "sha256", Value: "catalog-concurrent-sha"}},
				false, now)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent catalog resolve: %v", err)
	}

	var resolved string
	created := 0
	for result := range results {
		if len(result.ConflictingWorkIDs) != 0 || result.Confidence != "high" {
			t.Fatalf("unexpected catalog resolution: %+v", result)
		}
		if resolved == "" {
			resolved = result.WorkID
		} else if result.WorkID != resolved {
			t.Fatalf("split catalog resolution: %q != %q", result.WorkID, resolved)
		}
		if result.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("want exactly one catalog work creation, got %d", created)
	}
	mapping, err := s.UserBookWork(ctx, user.ID, book.ID)
	if err != nil || mapping.WorkID != resolved {
		t.Fatalf("catalog mapping: %+v %v", mapping, err)
	}
	works, err := s.ListWorks(ctx, user.ID)
	if err != nil || len(works) != 1 || works[0].Work.ID != resolved {
		t.Fatalf("catalog works: %+v %v", works, err)
	}
}

func testCatalogACLAndMapping(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "catalog-owner")
	reader := MkUser(t, s, "catalog-reader")
	manager := MkUser(t, s, "catalog-manager")
	outsider := MkUser(t, s, "catalog-outsider")
	now := time.Now().UTC()

	library := store.Library{
		ID: "lib-shared", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Shared EPUBs",
		ConfigJSON: []byte(`{"parser":"conservative"}`),
		CreatedAt:  now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLibrary(ctx, library); err != store.ErrConflict {
		t.Fatalf("duplicate library: want conflict, got %v", err)
	}
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "bad-watched", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryWatched, Name: "Missing root", CreatedAt: now,
	}); err == nil {
		t.Fatal("watched library without a root was accepted")
	}
	root := "/srv/books"
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-watched", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryWatched, Name: "Watched EPUBs", RootPath: &root, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-watched-duplicate", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryWatched, Name: "Same root", RootPath: &root, CreatedAt: now,
	}); err != store.ErrConflict {
		t.Fatalf("duplicate watched root: want conflict, got %v", err)
	}
	gotLibrary, err := s.LibraryByID(ctx, owner.ID, library.ID, store.LibraryRoleManage)
	if err != nil || gotLibrary.Role != store.LibraryRoleManage ||
		gotLibrary.Library.Name != library.Name {
		t.Fatalf("owner library: %+v %v", gotLibrary, err)
	}
	if _, err := s.LibraryByID(ctx, outsider.ID, library.ID, store.LibraryRoleRead); err != store.ErrNotFound {
		t.Fatalf("private library visible to outsider: %v", err)
	}
	if err := s.GrantLibraryAccess(ctx, owner.ID, library.ID, owner.ID, store.LibraryRoleRead, now); err != store.ErrNotFound {
		t.Fatalf("owner received redundant ACL row: %v", err)
	}

	if err := s.GrantLibraryAccess(ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(ctx, owner.ID, library.ID, manager.ID, store.LibraryRoleManage, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LibraryByID(ctx, reader.ID, library.ID, store.LibraryRoleRead); err != nil ||
		got.Role != store.LibraryRoleRead {
		t.Fatalf("reader access: %+v %v", got, err)
	}
	if _, err := s.LibraryByID(ctx, reader.ID, library.ID, store.LibraryRoleManage); err != store.ErrNotFound {
		t.Fatalf("reader received manage access: %v", err)
	}
	if libraries, err := s.ListLibraries(ctx, reader.ID, store.LibraryRoleRead); err != nil ||
		len(libraries) != 1 || libraries[0].Library.ID != library.ID {
		t.Fatalf("reader libraries: %+v %v", libraries, err)
	}
	if libraries, err := s.ListLibraries(ctx, outsider.ID, store.LibraryRoleRead); err != nil ||
		len(libraries) != 0 {
		t.Fatalf("outsider libraries: %+v %v", libraries, err)
	}

	book := store.CatalogBook{
		ID: "book-shared", LibraryID: library.ID, Status: store.BookActive,
		Title: "The Left Hand of Darkness", TitleSource: store.MetadataEmbedded,
		Subtitle: "A Novel", SubtitleSource: store.MetadataManual, SubtitleLocked: true,
		Publisher: "Ace", PublisherSource: store.MetadataEmbedded,
		PublishedDate: "1969", PublishedDateSource: store.MetadataEmbedded,
		RawMetadataJSON: []byte(`{"dc:identifier":"urn:isbn:9780441478125"}`),
		SetLocks:        store.MetadataSetLocks{Tags: true},
		CreatedAt:       now,
	}
	if err := s.CreateCatalogBook(ctx, reader.ID, book); err != store.ErrNotFound {
		t.Fatalf("reader created book: %v", err)
	}
	if err := s.CreateCatalogBook(ctx, manager.ID, book); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCatalogBook(ctx, manager.ID, book); err != store.ErrConflict {
		t.Fatalf("duplicate book: want conflict, got %v", err)
	}
	gotBook, err := s.CatalogBookByID(ctx, reader.ID, book.ID, store.LibraryRoleRead)
	if err != nil || gotBook.Title != book.Title || !gotBook.SubtitleLocked ||
		string(gotBook.RawMetadataJSON) != string(book.RawMetadataJSON) {
		t.Fatalf("reader book: %+v %v", gotBook, err)
	}
	if gotBook.Revision != 1 {
		t.Fatalf("new book revision: want 1, got %d", gotBook.Revision)
	}
	if gotBook.SetLocks != (store.MetadataSetLocks{Tags: true}) {
		t.Fatalf("new book set locks: %+v", gotBook.SetLocks)
	}
	if _, err := s.CatalogBookByID(ctx, reader.ID, book.ID, store.LibraryRoleManage); err != store.ErrNotFound {
		t.Fatalf("reader received book management: %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, outsider.ID, book.ID, store.LibraryRoleRead); err != store.ErrNotFound {
		t.Fatalf("book visible to outsider: %v", err)
	}
	if books, err := s.ListCatalogBooks(ctx, reader.ID, library.ID); err != nil ||
		len(books) != 1 || books[0].ID != book.ID {
		t.Fatalf("reader books: %+v %v", books, err)
	}

	ownerWork := store.Work{
		ID: "catalog-owner-work", UserID: owner.ID, Title: book.Title, CreatedAt: now,
	}
	ownerResult, err := s.ResolveCatalogBookWork(ctx, owner.ID, book.ID, ownerWork,
		[]store.Edition{{
			UserID: owner.ID, SHA256: "catalog-owner-sha", WorkID: ownerWork.ID,
		}},
		[]store.Identifier{{Kind: "sha256", Value: "catalog-owner-sha"}},
		false, now)
	if err != nil || !ownerResult.Created || ownerResult.WorkID != ownerWork.ID {
		t.Fatalf("owner catalog resolution: %+v %v", ownerResult, err)
	}
	readerWork := store.Work{
		ID: "catalog-reader-work", UserID: reader.ID, Title: book.Title, CreatedAt: now,
	}
	readerResult, err := s.ResolveCatalogBookWork(ctx, reader.ID, book.ID, readerWork,
		[]store.Edition{{
			UserID: reader.ID, SHA256: "catalog-reader-sha", WorkID: readerWork.ID,
		}},
		[]store.Identifier{{Kind: "sha256", Value: "catalog-reader-sha"}},
		false, now)
	if err != nil || !readerResult.Created || readerResult.WorkID != readerWork.ID {
		t.Fatalf("reader catalog resolution: %+v %v", readerResult, err)
	}
	otherReaderWork := MkWork(t, s, reader, "catalog-reader-work-2", "catalog-reader-sha-2")
	repeated, err := s.ResolveCatalogBookWork(ctx, reader.ID, book.ID,
		store.Work{ID: "unused-reader-work", UserID: reader.ID, CreatedAt: now},
		nil,
		[]store.Identifier{{Kind: "sha256", Value: "catalog-reader-sha"}},
		false, now)
	if err != nil || repeated.Created || repeated.WorkID != readerWork.ID {
		t.Fatalf("idempotent catalog resolution: %+v %v", repeated, err)
	}
	conflict, err := s.ResolveCatalogBookWork(ctx, reader.ID, book.ID,
		store.Work{ID: "unused-conflict-work", UserID: reader.ID, CreatedAt: now},
		nil,
		[]store.Identifier{{Kind: "sha256", Value: "catalog-reader-sha-2"}},
		false, now)
	if err != nil || len(conflict.ConflictingWorkIDs) != 2 {
		t.Fatalf("conflicting remap: %+v %v", conflict, err)
	}
	if _, err := s.ResolveCatalogBookWork(ctx, outsider.ID, book.ID,
		store.Work{ID: "outsider-work", UserID: outsider.ID, CreatedAt: now},
		nil, nil, false, now); err != store.ErrNotFound {
		t.Fatalf("outsider resolved private catalog book: %v", err)
	}
	ownerMapping, err := s.UserBookWork(ctx, owner.ID, book.ID)
	if err != nil || ownerMapping.WorkID != ownerWork.ID {
		t.Fatalf("owner mapping: %+v %v", ownerMapping, err)
	}
	readerMapping, err := s.UserBookWork(ctx, reader.ID, book.ID)
	if err != nil || readerMapping.WorkID != readerWork.ID ||
		readerMapping.WorkID == ownerMapping.WorkID {
		t.Fatalf("reader mapping: %+v %v", readerMapping, err)
	}
	stable, err := s.ResolveAliases(ctx, reader.ID,
		[]store.Identifier{{Kind: "source", Value: "liseur-sync:" + book.ID}})
	if err != nil || stable["source:liseur-sync:"+book.ID] != readerWork.ID {
		t.Fatalf("stable catalog alias: %v %v", stable, err)
	}

	fuzzyBook := book
	fuzzyBook.ID = "book-fuzzy"
	fuzzyBook.Title = "A Fuzzy Match"
	if err := s.CreateCatalogBook(ctx, manager.ID, fuzzyBook); err != nil {
		t.Fatal(err)
	}
	fuzzyWork := store.Work{
		ID: "catalog-fuzzy-work", UserID: reader.ID, Title: fuzzyBook.Title, CreatedAt: now,
	}
	if err := s.CreateWork(ctx, fuzzyWork, nil,
		[]store.Identifier{{Kind: "ta", Value: "a fuzzy match|author"}}); err != nil {
		t.Fatal(err)
	}
	fuzzyResult, err := s.ResolveCatalogBookWork(ctx, reader.ID, fuzzyBook.ID,
		store.Work{ID: "unused-fuzzy-work", UserID: reader.ID, CreatedAt: now},
		[]store.Edition{{
			UserID: reader.ID, SHA256: "catalog-fuzzy-sha", WorkID: "unused-fuzzy-work",
		}},
		[]store.Identifier{
			{Kind: "sha256", Value: "catalog-fuzzy-sha"},
			{Kind: "ta", Value: "a fuzzy match|author"},
		},
		false, now)
	if err != nil || fuzzyResult.WorkID != fuzzyWork.ID || fuzzyResult.Confidence != "low" {
		t.Fatalf("unconfirmed fuzzy catalog resolution: %+v %v", fuzzyResult, err)
	}
	if _, err := s.UserBookWork(ctx, reader.ID, fuzzyBook.ID); err != store.ErrNotFound {
		t.Fatalf("fuzzy match created mapping before confirmation: %v", err)
	}
	fuzzyAliases, err := s.ResolveAliases(ctx, reader.ID, []store.Identifier{
		{Kind: "sha256", Value: "catalog-fuzzy-sha"},
		{Kind: "source", Value: "liseur-sync:" + fuzzyBook.ID},
	})
	if err != nil || len(fuzzyAliases) != 0 {
		t.Fatalf("fuzzy match promoted strong aliases: %v %v", fuzzyAliases, err)
	}
	fuzzyResult, err = s.ResolveCatalogBookWork(ctx, reader.ID, fuzzyBook.ID,
		store.Work{ID: "unused-confirmed-work", UserID: reader.ID, CreatedAt: now},
		[]store.Edition{{
			UserID: reader.ID, SHA256: "catalog-fuzzy-sha", WorkID: "unused-confirmed-work",
		}},
		[]store.Identifier{
			{Kind: "sha256", Value: "catalog-fuzzy-sha"},
			{Kind: "ta", Value: "a fuzzy match|author"},
		},
		true, now)
	if err != nil || fuzzyResult.WorkID != fuzzyWork.ID || fuzzyResult.Confidence != "high" {
		t.Fatalf("confirmed fuzzy catalog resolution: %+v %v", fuzzyResult, err)
	}

	if err := s.RevokeLibraryAccess(ctx, manager.ID, library.ID, reader.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeLibraryAccess(ctx, owner.ID, library.ID, manager.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(ctx, manager.ID, library.ID, reader.ID, store.LibraryRoleRead, now); err != store.ErrNotFound {
		t.Fatalf("revoked manager changed ACL: %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, reader.ID, book.ID, store.LibraryRoleRead); err != store.ErrNotFound {
		t.Fatalf("revoked reader still sees book: %v", err)
	}
	if _, err := s.UserBookWork(ctx, reader.ID, book.ID); err != store.ErrNotFound {
		t.Fatalf("revoked reader still sees mapping: %v", err)
	}
	if _, err := s.ListCatalogBooks(ctx, reader.ID, library.ID); err != store.ErrNotFound {
		t.Fatalf("revoked reader still lists books: %v", err)
	}
	if err := s.GrantLibraryAccess(ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}

	if err := s.MergeWorks(ctx, reader.ID, readerWork.ID, otherReaderWork.ID); err != nil {
		t.Fatal(err)
	}
	readerMapping, err = s.UserBookWork(ctx, reader.ID, book.ID)
	if err != nil || readerMapping.WorkID != otherReaderWork.ID {
		t.Fatalf("merge left stale catalog mapping: %+v %v", readerMapping, err)
	}
}

func testCatalogBookMetadata(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "metadata-owner")
	reader := MkUser(t, s, "metadata-reader")
	outsider := MkUser(t, s, "metadata-outsider")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib-metadata", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Metadata", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{
		ID: "book-metadata", LibraryID: library.ID, Status: store.BookActive,
		Title: "Dune", TitleSource: store.MetadataEmbedded, CreatedAt: now,
	}
	if err := s.CreateCatalogBook(ctx, owner.ID, book); err != nil {
		t.Fatal(err)
	}

	empty, err := s.CatalogBookMetadata(ctx, reader.ID, book.ID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Book.Revision != 1 || len(empty.Tags) != 0 || len(empty.Series) != 0 ||
		len(empty.Contributors) != 0 || len(empty.Identifiers) != 0 ||
		len(empty.Languages) != 0 || len(empty.Genres) != 0 {
		t.Fatalf("new book metadata: %+v", empty)
	}
	if _, err := s.CatalogBookMetadata(
		ctx, outsider.ID, book.ID, store.LibraryRoleRead); err != store.ErrNotFound {
		t.Fatalf("outsider read metadata: %v", err)
	}

	position := 1.0
	resolved := empty
	resolved.Book.Title = "Dune"
	resolved.Book.Publisher = "Chilton"
	resolved.Book.PublisherSource = store.MetadataEmbedded
	resolved.Book.SetLocks.Genres = true
	resolved.Identifiers = []store.BookIdentifier{
		{Scheme: "isbn", Value: "9780441013593", Source: store.MetadataEmbedded},
	}
	resolved.Languages = []store.BookLanguage{
		{Language: "en", Source: store.MetadataEmbedded},
	}
	resolved.Tags = []store.BookTaxon{
		{ID: "tag-sf", Name: "Science Fiction",
			NormalizedName: "science fiction", Source: store.MetadataEmbedded},
		{ID: "tag-desert", Name: "Desert",
			NormalizedName: "desert", Source: store.MetadataEmbedded},
	}
	resolved.Series = []store.BookSeries{
		{SeriesID: "series-dune", Name: "Dune", NormalizedName: "dune",
			Position: &position, Source: store.MetadataFilename},
	}
	resolved.Contributors = []store.BookContributor{
		{ContributorID: "contrib-herbert", Name: "Frank Herbert",
			NormalizedName: "frank herbert", Role: "author",
			Source: store.MetadataEmbedded},
	}
	request := store.ApplyBookMetadataRequest{
		Metadata: resolved, ExpectedRevision: 1, UpdatedAt: now,
	}
	if err := store.ValidateApplyBookMetadata(request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyCatalogBookMetadata(
		ctx, reader.ID, request); err != store.ErrNotFound {
		t.Fatalf("reader applied metadata: %v", err)
	}
	if _, err := s.ApplyCatalogBookMetadata(
		ctx, outsider.ID, request); err != store.ErrNotFound {
		t.Fatalf("outsider applied metadata: %v", err)
	}
	applied, err := s.ApplyCatalogBookMetadata(ctx, owner.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Book.Revision != 2 || applied.Book.Publisher != "Chilton" ||
		!applied.Book.SetLocks.Genres {
		t.Fatalf("applied book: %+v", applied.Book)
	}
	// Rows come back in a deterministic order, not in assertion order.
	if len(applied.Tags) != 2 || applied.Tags[0].NormalizedName != "desert" ||
		applied.Tags[1].NormalizedName != "science fiction" {
		t.Fatalf("applied tags: %+v", applied.Tags)
	}
	if len(applied.Series) != 1 || applied.Series[0].Position == nil ||
		*applied.Series[0].Position != 1 ||
		applied.Series[0].Source != store.MetadataFilename {
		t.Fatalf("applied series: %+v", applied.Series)
	}
	if len(applied.Contributors) != 1 ||
		applied.Contributors[0].Name != "Frank Herbert" ||
		applied.Contributors[0].Role != "author" {
		t.Fatalf("applied contributors: %+v", applied.Contributors)
	}
	if len(applied.Identifiers) != 1 || applied.Identifiers[0].Value != "9780441013593" ||
		len(applied.Languages) != 1 || applied.Languages[0].Language != "en" {
		t.Fatalf("applied identifiers and languages: %+v", applied)
	}
	reread, err := s.CatalogBookMetadata(ctx, reader.ID, book.ID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if len(reread.Tags) != len(applied.Tags) || reread.Book.Revision != 2 {
		t.Fatalf("reader reread: %+v", reread)
	}

	// The same expected revision cannot be applied twice.
	if _, err := s.ApplyCatalogBookMetadata(
		ctx, owner.ID, request); err != store.ErrStaleRevision {
		t.Fatalf("stale apply: %v", err)
	}

	// A later apply asserting fewer rows removes what it omits, and reuses
	// the entity rows created by the first apply rather than duplicating
	// them under a new id.
	second := applied
	second.Tags = []store.BookTaxon{
		{ID: "tag-other", Name: "science fiction",
			NormalizedName: "science fiction", Source: store.MetadataManual,
			Locked: true},
	}
	second.Series = nil
	shrunk, err := s.ApplyCatalogBookMetadata(ctx, owner.ID, store.ApplyBookMetadataRequest{
		Metadata: second, ExpectedRevision: 2, UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if shrunk.Book.Revision != 3 || len(shrunk.Series) != 0 ||
		len(shrunk.Tags) != 1 || shrunk.Tags[0].ID != "tag-sf" ||
		shrunk.Tags[0].Name != "Science Fiction" || !shrunk.Tags[0].Locked ||
		shrunk.Tags[0].Source != store.MetadataManual {
		t.Fatalf("shrunk metadata: %+v", shrunk)
	}

	// A rejected apply leaves nothing behind.
	poisoned := shrunk
	poisoned.Tags = []store.BookTaxon{
		{ID: "tag-late", Name: "Late", NormalizedName: "late",
			Source: store.MetadataEmbedded},
	}
	if _, err := s.ApplyCatalogBookMetadata(ctx, owner.ID, store.ApplyBookMetadataRequest{
		Metadata: poisoned, ExpectedRevision: 1, UpdatedAt: now,
	}); err != store.ErrStaleRevision {
		t.Fatalf("second stale apply: %v", err)
	}
	after, err := s.CatalogBookMetadata(ctx, owner.ID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if after.Book.Revision != 3 || len(after.Tags) != 1 ||
		after.Tags[0].NormalizedName != "science fiction" {
		t.Fatalf("rolled back apply leaked: %+v", after)
	}
}

func testConcurrentCatalogMetadataApply(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "metadata-concurrent")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib-metadata-concurrent", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Concurrent metadata", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{
		ID: "book-metadata-concurrent", LibraryID: library.ID,
		Status: store.BookActive, Title: "Race", CreatedAt: now,
	}
	if err := s.CreateCatalogBook(ctx, owner.ID, book); err != nil {
		t.Fatal(err)
	}
	current, err := s.CatalogBookMetadata(ctx, owner.ID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	applied := make(chan store.BookMetadata, workers)
	stale := make(chan struct{}, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resolved := current
			resolved.Book.Title = fmt.Sprintf("Race %02d", i)
			resolved.Tags = []store.BookTaxon{{
				ID:             fmt.Sprintf("tag-race-%02d", i),
				Name:           "Race",
				NormalizedName: "race",
				Source:         store.MetadataEmbedded,
			}}
			<-start
			result, err := s.ApplyCatalogBookMetadata(ctx, owner.ID,
				store.ApplyBookMetadataRequest{
					Metadata: resolved, ExpectedRevision: 1, UpdatedAt: now,
				})
			switch {
			case err == store.ErrStaleRevision:
				stale <- struct{}{}
			case err != nil:
				errs <- err
			default:
				applied <- result
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(applied)
	close(stale)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent apply: %v", err)
	}
	if len(applied) != 1 || len(stale) != workers-1 {
		t.Fatalf("concurrent apply: %d applied, %d stale", len(applied), len(stale))
	}
	winner := <-applied
	if winner.Book.Revision != 2 || len(winner.Tags) != 1 {
		t.Fatalf("winning apply: %+v", winner)
	}
	final, err := s.CatalogBookMetadata(ctx, owner.ID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if final.Book.Revision != 2 || final.Book.Title != winner.Book.Title ||
		len(final.Tags) != 1 || final.Tags[0].ID != winner.Tags[0].ID {
		t.Fatalf("lost update: %+v want %+v", final, winner)
	}
}

// A candidate entity id is unique table-wide, so two rows offering the same
// id for different names must be rejected at the edge rather than reaching
// the backend as a driver-level constraint violation.
func testCatalogMetadataRejectsDuplicateEntityIDs(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "metadata-duplicate")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib-metadata-duplicate", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Duplicate", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{
		ID: "book-metadata-duplicate", LibraryID: library.ID,
		Status: store.BookActive, Title: "Duplicate", CreatedAt: now,
	}
	if err := s.CreateCatalogBook(ctx, owner.ID, book); err != nil {
		t.Fatal(err)
	}
	current, err := s.CatalogBookMetadata(ctx, owner.ID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	current.Tags = []store.BookTaxon{
		{ID: "tag-collide", Name: "First", NormalizedName: "first",
			Source: store.MetadataEmbedded},
		{ID: "tag-collide", Name: "Second", NormalizedName: "second",
			Source: store.MetadataEmbedded},
	}
	request := store.ApplyBookMetadataRequest{
		Metadata: current, ExpectedRevision: 1, UpdatedAt: now,
	}
	if err := store.ValidateApplyBookMetadata(request); err != store.ErrInvalidTransition {
		t.Fatalf("duplicate candidate id accepted: %v", err)
	}

	// An id already taken by another name is a conflict, never a raw
	// constraint error the handler edge cannot map.
	current.Tags = current.Tags[:1]
	applied, err := s.ApplyCatalogBookMetadata(ctx, owner.ID,
		store.ApplyBookMetadataRequest{
			Metadata: current, ExpectedRevision: 1, UpdatedAt: now,
		})
	if err != nil {
		t.Fatal(err)
	}
	applied.Tags = []store.BookTaxon{
		{ID: applied.Tags[0].ID, Name: "Second", NormalizedName: "second",
			Source: store.MetadataEmbedded},
	}
	if _, err := s.ApplyCatalogBookMetadata(ctx, owner.ID,
		store.ApplyBookMetadataRequest{
			Metadata: applied, ExpectedRevision: 2, UpdatedAt: now,
		}); err != store.ErrConflict {
		t.Fatalf("reused entity id: want conflict, got %v", err)
	}
	after, err := s.CatalogBookMetadata(ctx, owner.ID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if after.Book.Revision != 2 || len(after.Tags) != 1 ||
		after.Tags[0].NormalizedName != "first" {
		t.Fatalf("rejected apply leaked: %+v", after)
	}
}

// Two books in one library creating the same new entity names must resolve
// them in the same order: opposite orders deadlock PostgreSQL on the
// speculative index insertions it takes for ON CONFLICT. The window is
// narrow enough that this test does not reliably reproduce the deadlock
// without the canonical ordering, so it stands as a smoke test that
// concurrent entity creation converges on one row per name rather than as
// proof of the ordering itself.
func testConcurrentCatalogMetadataEntityCreation(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "metadata-entity-race")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib-entity-race", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Entity race", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	names := []string{"alpha", "beta", "gamma", "delta"}
	const books = 6
	current := make([]store.BookMetadata, books)
	for i := 0; i < books; i++ {
		id := fmt.Sprintf("book-entity-race-%02d", i)
		if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
			ID: id, LibraryID: library.ID, Status: store.BookActive,
			Title: id, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		metadata, err := s.CatalogBookMetadata(ctx, owner.ID, id, store.LibraryRoleManage)
		if err != nil {
			t.Fatal(err)
		}
		// Every book asserts the same names in a different rotation.
		for j := range names {
			name := names[(i+j)%len(names)]
			// Distinct candidate ids per book: only the store deciding
			// which one wins can make the books agree, so a per-name
			// duplicate row would be observable rather than impossible.
			metadata.Tags = append(metadata.Tags, store.BookTaxon{
				ID:             fmt.Sprintf("tag-%s-%d", name, i),
				Name:           name,
				NormalizedName: name,
				Source:         store.MetadataEmbedded,
			})
		}
		current[i] = metadata
	}

	start := make(chan struct{})
	errs := make(chan error, books)
	var wg sync.WaitGroup
	for i := 0; i < books; i++ {
		wg.Add(1)
		go func(metadata store.BookMetadata) {
			defer wg.Done()
			<-start
			if _, err := s.ApplyCatalogBookMetadata(ctx, owner.ID,
				store.ApplyBookMetadataRequest{
					Metadata: metadata, ExpectedRevision: 1, UpdatedAt: now,
				}); err != nil {
				errs <- err
			}
		}(current[i])
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent entity creation: %v", err)
	}
	winners := make(map[string]string, len(names))
	for i := 0; i < books; i++ {
		final, err := s.CatalogBookMetadata(
			ctx, owner.ID, current[i].Book.ID, store.LibraryRoleManage)
		if err != nil {
			t.Fatal(err)
		}
		if len(final.Tags) != len(names) {
			t.Fatalf("book %d tags: %+v", i, final.Tags)
		}
		// Every book converges on one entity row per name, whichever
		// book's candidate id happened to create it.
		for _, tag := range final.Tags {
			winner, seen := winners[tag.NormalizedName]
			if !seen {
				winners[tag.NormalizedName] = tag.ID
				continue
			}
			if winner != tag.ID {
				t.Fatalf("duplicate entity rows for %q: %q and %q",
					tag.NormalizedName, winner, tag.ID)
			}
		}
	}
}
