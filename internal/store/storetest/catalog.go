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
