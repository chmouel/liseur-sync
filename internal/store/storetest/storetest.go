// Package storetest runs the shared store test suite against any
// backend. Both SQLite and PostgreSQL must pass identical behavior.
package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/infer"
	"github.com/chmouel/liseur-sync/internal/store"
)

// OpenFunc returns a migrated, empty store for one test.
type OpenFunc func(t *testing.T) store.Store

// anyReader stands for "nobody in particular" in the catalog reads that
// resolve series claims for a user (ADR-0018). A test that is not about
// claims passes it and sees exactly what the folder said.
const anyReader = ""

// Run executes the full suite.
func Run(t *testing.T, open OpenFunc) {
	t.Run("Users", func(t *testing.T) { testUsers(t, open) })
	t.Run("AdminRole", func(t *testing.T) { testAdminRole(t, open) })
	t.Run("ConcurrentAdminDemotion", func(t *testing.T) {
		testConcurrentAdminDemotion(t, open)
	})
	t.Run("ConcurrentAdminTokenMint", func(t *testing.T) {
		testConcurrentAdminTokenMint(t, open)
	})
	t.Run("CreateFirstAdmin", func(t *testing.T) { testCreateFirstAdmin(t, open) })
	t.Run("ConcurrentFirstAdmin", func(t *testing.T) {
		testConcurrentFirstAdmin(t, open)
	})
	t.Run("AdminCounts", func(t *testing.T) { testAdminCounts(t, open) })
	t.Run("UserCredentialOperations", func(t *testing.T) {
		testUserCredentialOperations(t, open)
	})
	t.Run("ListUsersPage", func(t *testing.T) { testListUsersPage(t, open) })
	t.Run("DisabledUser", func(t *testing.T) { testDisabledUser(t, open) })
	t.Run("Tokens", func(t *testing.T) { testTokens(t, open) })
	t.Run("Folders", func(t *testing.T) { testFolders(t, open) })
	t.Run("CatalogListingsPageAndIsolate", func(t *testing.T) {
		testCatalogListingsPageAndIsolate(t, open)
	})
	t.Run("AvailableBookMediaTypes", func(t *testing.T) {
		testAvailableBookMediaTypes(t, open)
	})
	t.Run("CatalogAuthorsForBooks", func(t *testing.T) {
		testCatalogAuthorsForBooks(t, open)
	})
	t.Run("CatalogBookRelationsForBooks", func(t *testing.T) {
		testCatalogBookRelationsForBooks(t, open)
	})
	t.Run("CatalogSeriesSourceIsPerReader", func(t *testing.T) {
		testCatalogSeriesSourceIsPerReader(t, open)
	})
	t.Run("UserBookWorkIsPerUser", func(t *testing.T) {
		testUserBookWorkIsPerUser(t, open)
	})
	t.Run("CatalogEntityListing", func(t *testing.T) {
		testCatalogEntityListing(t, open)
	})
	t.Run("ListBooksByEntitySeriesOrder", func(t *testing.T) {
		testListBooksByEntitySeriesOrder(t, open)
	})
	t.Run("EntitiesFoldAcrossFolders", func(t *testing.T) {
		testEntitiesFoldAcrossFolders(t, open)
	})
	t.Run("EntityOrphansAreCollected", func(t *testing.T) {
		testEntityOrphansAreCollected(t, open)
	})
	t.Run("EntityGCKeepsWhatAScanStillNames", func(t *testing.T) {
		testEntityGCKeepsWhatAScanStillNames(t, open)
	})
	t.Run("SeriesRenameLayers", func(t *testing.T) {
		testSeriesRenameLayers(t, open)
	})
	t.Run("SeriesRenameSurvivesAScan", func(t *testing.T) {
		testSeriesRenameSurvivesAScan(t, open)
	})
	t.Run("SeriesRenameRefusals", func(t *testing.T) {
		testSeriesRenameRefusals(t, open)
	})
	t.Run("SeriesRenamePagesOnTheNameShown", func(t *testing.T) {
		testSeriesRenamePagesOnTheNameShown(t, open)
	})
	t.Run("SeriesMergeSurvivesAScan", func(t *testing.T) {
		testSeriesMergeSurvivesAScan(t, open)
	})
	t.Run("SeriesMergeCarriesClaims", func(t *testing.T) {
		testSeriesMergeCarriesClaims(t, open)
	})
	t.Run("SeriesUnbindRestoresFromDisk", func(t *testing.T) {
		testSeriesUnbindRestoresFromDisk(t, open)
	})
	t.Run("SeriesSplitSurvivesAScan", func(t *testing.T) {
		testSeriesSplitSurvivesAScan(t, open)
	})
	t.Run("SeriesSplitTakesAbsorbedNames", func(t *testing.T) {
		testSeriesSplitTakesAbsorbedNames(t, open)
	})
	t.Run("SeriesMergeRefusals", func(t *testing.T) {
		testSeriesMergeRefusals(t, open)
	})
	t.Run("SeriesSplitOfOneFolderIsARename", func(t *testing.T) {
		testSeriesSplitOfOneFolderIsARename(t, open)
	})
	t.Run("SeriesBindingsDieWithTheirFolder", func(t *testing.T) {
		testSeriesBindingsDieWithTheirFolder(t, open)
	})
	t.Run("SeriesRenameDiesWithItsSeries", func(t *testing.T) {
		testSeriesRenameDiesWithItsSeries(t, open)
	})
	t.Run("SeriesClaimLayers", func(t *testing.T) {
		testSeriesClaimLayers(t, open)
	})
	t.Run("SeriesClaimEmptyMeansNoSeries", func(t *testing.T) {
		testSeriesClaimEmptyMeansNoSeries(t, open)
	})
	t.Run("SeriesClaimRevisionPrecondition", func(t *testing.T) {
		testSeriesClaimRevisionPrecondition(t, open)
	})
	t.Run("SeriesClaimRevisionIsMillisecondPrecise", func(t *testing.T) {
		testSeriesClaimRevisionIsMillisecondPrecise(t, open)
	})
	t.Run("SeriesClaimSurvivesReconcile", func(t *testing.T) {
		testSeriesClaimSurvivesReconcile(t, open)
	})
	t.Run("SeriesClaimFollowsIdentity", func(t *testing.T) {
		testSeriesClaimFollowsIdentity(t, open)
	})
	t.Run("SeriesClaimOrdersAndPages", func(t *testing.T) {
		testSeriesClaimOrdersAndPages(t, open)
	})
	t.Run("SeriesReorderKeepsOtherMemberships", func(t *testing.T) {
		testSeriesReorderKeepsOtherMemberships(t, open)
	})
	t.Run("SeriesClaimRefusals", func(t *testing.T) {
		testSeriesClaimRefusals(t, open)
	})
	t.Run("SearchFindsBooksByEverythingTheySay", func(t *testing.T) {
		testSearchFindsBooksByEverythingTheySay(t, open)
	})
	t.Run("SearchFollowsTheCatalogItIndexes", func(t *testing.T) {
		testSearchFollowsTheCatalogItIndexes(t, open)
	})
	t.Run("SearchFacetsDescribeTheAnswer", func(t *testing.T) {
		testSearchFacetsDescribeTheAnswer(t, open)
	})
	t.Run("SearchIsScopedAndBounded", func(t *testing.T) {
		testSearchIsScopedAndBounded(t, open)
	})
	t.Run("ReconcileIdempotency", func(t *testing.T) {
		testReconcileIdempotency(t, open)
	})
	t.Run("ReconcileMissingAndReturning", func(t *testing.T) {
		testReconcileMissingAndReturning(t, open)
	})
	t.Run("ReconcileIncompletePassMarksNothingMissing", func(t *testing.T) {
		testReconcileIncompletePassMarksNothingMissing(t, open)
	})
	t.Run("ReconcileZeroObservationPassMarksNothingMissing", func(t *testing.T) {
		testReconcileZeroObservationPassMarksNothingMissing(t, open)
	})
	t.Run("ReconcileReplacementDropsReadingMapping", func(t *testing.T) {
		testReconcileReplacementDropsReadingMapping(t, open)
	})
	t.Run("ReconcileUnchangedKeepsMetadata", func(t *testing.T) {
		testReconcileUnchangedKeepsMetadata(t, open)
	})
	t.Run("ReconcileCalibrePathMoveKeepsIdentity", func(t *testing.T) {
		testReconcileCalibrePathMoveKeepsIdentity(t, open)
	})
	t.Run("ReconcileCalibrePathSwap", func(t *testing.T) {
		testReconcileCalibrePathSwap(t, open)
	})
	t.Run("ReconcileCalibrePurgesDeletedBooks", func(t *testing.T) {
		testReconcileCalibrePurgesDeletedBooks(t, open)
	})
	t.Run("ReconcileCalibreUnservableBookIsKept", func(t *testing.T) {
		testReconcileCalibreUnservableBookIsKept(t, open)
	})
	t.Run("ReconcileCalibreIncompletePassPurgesNothing", func(t *testing.T) {
		testReconcileCalibreIncompletePassPurgesNothing(t, open)
	})
	t.Run("ReconcileCalibreCollectsExistingEmptyWorks", func(t *testing.T) {
		testReconcileCalibreCollectsExistingEmptyWorks(t, open)
	})
	t.Run("ReconcilePlainFolderNeverPurges", func(t *testing.T) {
		testReconcilePlainFolderNeverPurges(t, open)
	})
	t.Run("DeleteWork", func(t *testing.T) { testDeleteWork(t, open) })
	t.Run("DeleteWorkRefusesAMappedWork", func(t *testing.T) {
		testDeleteWorkRefusesAMappedWork(t, open)
	})
	t.Run("DeleteWorkIsPerUser", func(t *testing.T) {
		testDeleteWorkIsPerUser(t, open)
	})
	t.Run("DeleteMissingBook", func(t *testing.T) {
		testDeleteMissingBook(t, open)
	})
	t.Run("DeleteCatalogBook", func(t *testing.T) {
		testDeleteCatalogBook(t, open)
	})
	t.Run("DeleteCatalogBookForgetsOneReader", func(t *testing.T) {
		testDeleteCatalogBookForgetsOneReader(t, open)
	})
	t.Run("DeleteCatalogBookKeepsReadingASecondCopyHolds", func(t *testing.T) {
		testDeleteCatalogBookKeepsReadingASecondCopyHolds(t, open)
	})
	t.Run("ReconcileCalibreDigestChangeFollowsReader", func(t *testing.T) {
		testReconcileCalibreDigestChangeFollowsReader(t, open)
	})
	t.Run("ReconcileDigestCollisionChangesNothing", func(t *testing.T) {
		testReconcileDigestCollisionChangesNothing(t, open)
	})
	t.Run("ReconcilePartialDigestCollisionChangesNothing", func(t *testing.T) {
		testReconcilePartialDigestCollisionChangesNothing(t, open)
	})
	t.Run("ResolveAliases", func(t *testing.T) { testResolveAliases(t, open) })
	t.Run("AtomicWorkResolution", func(t *testing.T) { testAtomicWorkResolution(t, open) })
	t.Run("AppendOpsIdempotencyAndConflict", func(t *testing.T) { testAppendOps(t, open) })
	t.Run("ChangesPaginationAndHeads", func(t *testing.T) { testChangesAndHeads(t, open) })
	t.Run("SplitAndMerge", func(t *testing.T) { testSplitAndMerge(t, open) })
	t.Run("SessionsAppendOnly", func(t *testing.T) { testSessionsAppendOnly(t, open) })
	t.Run("SessionRollups", func(t *testing.T) { testSessionRollups(t, open) })
	t.Run("Housekeeping", func(t *testing.T) { testHousekeeping(t, open) })
	t.Run("ConcurrentAppendGapFreeSeq", func(t *testing.T) { testConcurrentAppend(t, open) })
	t.Run("PairingCodeSingleUse", func(t *testing.T) { testPairingRedeem(t, open) })
	t.Run("KopluginSupersession", func(t *testing.T) { testKopluginUpsert(t, open) })
	t.Run("LegacyAliasWritesAtomic", func(t *testing.T) { testLegacyAliasWritesAtomic(t, open) })
	t.Run("InferredSessionIdentity", func(t *testing.T) { testInferredSessionIdentity(t, open) })
}

func Ptr[T any](v T) *T { return &v }

func MkUser(t *testing.T, s store.Store, name string) store.User {
	t.Helper()
	u := store.User{
		ID:              "u-" + name,
		Name:            name,
		Argon2Hash:      "x",
		Timezone:        "Europe/Paris",
		KosyncEnabled:   true,
		KopluginEnabled: true,
		CreatedAt:       time.Now(),
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func MkWork(t *testing.T, s store.Store, u store.User, id, sha string) store.Work {
	t.Helper()
	w := store.Work{ID: id, UserID: u.ID, Title: "A Memory Called Empire", Author: "Martine", CreatedAt: time.Now()}
	e := &store.Edition{UserID: u.ID, SHA256: sha, WorkID: id, PageCount: Ptr(int64(462))}
	ids := []store.Identifier{
		{Kind: "sha256", Value: sha},
		{Kind: "partial-md5", Value: "md5-" + id},
		{Kind: "dc", Value: "urn:isbn:9780316419568-" + id},
	}
	if err := s.CreateWork(context.Background(), w, e, ids); err != nil {
		t.Fatal(err)
	}
	return w
}

func testUsers(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "alice")

	got, err := s.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || got.Timezone != "Europe/Paris" || !got.KosyncEnabled {
		t.Fatalf("bad user: %+v", got)
	}
	if _, err := s.UserByName(ctx, "nobody"); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.CreateUser(ctx, u); err != store.ErrConflict {
		t.Fatalf("duplicate user: want ErrConflict, got %v", err)
	}

	// Settings update.
	if err := s.UpdateUserSettings(ctx, u.ID, "Asia/Tokyo", false, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserByID(ctx, u.ID)
	if got.Timezone != "Asia/Tokyo" || got.KosyncEnabled || !got.KopluginEnabled {
		t.Fatalf("settings not saved: %+v", got)
	}
}

func testTokens(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "bob")
	tok := store.Token{
		ID: "t1", UserID: u.ID, DeviceID: "d-boox", Name: "Boox Palma",
		Scopes: store.ScopeSet{store.ScopeLibraryRead, store.ScopeSync, store.ScopeLibraryRead},
		SHA256: "deadbeef", CreatedAt: time.Now(),
	}

	if err := s.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.TokenByHash(ctx, u.ID, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "d-boox" || got.Scopes.String() != "sync,library-read" {
		t.Fatalf("bad token: %+v", got)
	}
	if _, err := s.TokenByHash(ctx, "u-other", "deadbeef"); err != store.ErrNotFound {
		t.Fatalf("cross-user token read: want ErrNotFound, got %v", err)
	}
	if err := s.UpdateTokenScopes(ctx, "u-other", "t1",
		store.ScopeSet{store.ScopeReadInsights}); err != store.ErrNotFound {
		t.Fatalf("cross-user token update: want ErrNotFound, got %v", err)
	}
	if err := s.UpdateTokenScopes(ctx, u.ID, "t1",
		store.ScopeSet{store.ScopeLibraryRead, store.ScopeReadInsights}); err != nil {
		t.Fatal(err)
	}
	got, err = s.TokenByHash(ctx, u.ID, "deadbeef")
	if err != nil || got.DeviceID != "d-boox" ||
		got.Scopes.String() != "read-insights,library-read" {
		t.Fatalf("scope update changed identity or scopes: %+v %v", got, err)
	}
	listed, err := s.ListTokens(ctx, u.ID)
	if err != nil || len(listed) != 1 || listed[0].Scopes.String() != "read-insights,library-read" {
		t.Fatalf("listed tokens: %+v %v", listed, err)
	}
	if err := s.RevokeToken(ctx, u.ID, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx, u.ID, "t1"); err != store.ErrNotFound {
		t.Fatalf("double revoke: want ErrNotFound, got %v", err)
	}

	// Deleting is not revoking. A revoked row stays as a record; a
	// deleted one is gone, which is what the reader's hourly credentials
	// need so the table does not grow for as long as someone reads.
	second := tok
	second.ID, second.SHA256 = "t2", "feedface"
	if err := s.CreateToken(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteToken(ctx, "u-other", "t1"); err != store.ErrNotFound {
		t.Fatalf("cross-user token delete: want ErrNotFound, got %v", err)
	}
	if err := s.DeleteToken(ctx, u.ID, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteToken(ctx, u.ID, "t1"); err != store.ErrNotFound {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
	listed, err = s.ListTokens(ctx, u.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != "t2" {
		t.Fatalf("delete took a sibling with it: %+v %v", listed, err)
	}
	if _, err := s.TokenByHash(ctx, u.ID, "deadbeef"); err != store.ErrNotFound {
		t.Fatalf("deleted token still authenticates: %v", err)
	}
}

func testResolveAliases(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "carol")
	MkWork(t, s, u, "w1", "abc123")

	got, err := s.ResolveAliases(context.Background(), u.ID, []store.Identifier{
		{Kind: "partial-md5", Value: "md5-w1"},
		{Kind: "dc", Value: "urn:isbn:nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["partial-md5:md5-w1"] != "w1" {
		t.Fatalf("want w1, got %v", got)
	}
	if _, ok := got["dc:urn:isbn:nope"]; ok {
		t.Fatalf("unknown alias should be absent: %v", got)
	}
}

func testAtomicWorkResolution(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "resolver")
	ids := []store.Identifier{
		{Kind: "sha256", Value: "same-sha"},
		{Kind: "partial-md5", Value: "same-md5"},
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
			workID := fmt.Sprintf("candidate-%02d", i)
			result, err := s.ResolveWork(ctx, u.ID,
				store.Work{
					ID: workID, UserID: u.ID, Title: "Concurrent",
					CreatedAt: time.Now(),
				},
				[]store.Edition{{UserID: u.ID, SHA256: "same-sha", WorkID: workID}},
				ids, false)
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
		t.Fatalf("concurrent resolve: %v", err)
	}

	var resolved string
	created := 0
	for result := range results {
		if len(result.ConflictingWorkIDs) != 0 || result.Confidence != "high" {
			t.Fatalf("unexpected resolution: %+v", result)
		}
		if resolved == "" {
			resolved = result.WorkID
		} else if result.WorkID != resolved {
			t.Fatalf("split resolution: %q != %q", result.WorkID, resolved)
		}
		if result.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("want exactly one creation, got %d", created)
	}
	works, err := s.ListWorks(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Work.ID != resolved {
		t.Fatalf("want one resolved work %q, got %+v", resolved, works)
	}
	got, err := s.ResolveAliases(ctx, u.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got[id.Kind+":"+id.Value] != resolved {
			t.Fatalf("alias %s:%s did not converge: %v", id.Kind, id.Value, got)
		}
	}
	edition, err := s.EditionBySHA(ctx, u.ID, "same-sha")
	if err != nil {
		t.Fatal(err)
	}
	if edition.WorkID != resolved {
		t.Fatalf("edition mapped to %q, want %q", edition.WorkID, resolved)
	}

	conflictUser := MkUser(t, s, "resolver-conflict")
	MkWork(t, s, conflictUser, "wa", "sha-a")
	MkWork(t, s, conflictUser, "wb", "sha-b")
	conflictIDs := []store.Identifier{
		{Kind: "sha256", Value: "sha-a"},
		{Kind: "sha256", Value: "sha-b"},
		{Kind: "source", Value: "catalog:new"},
	}
	conflict, err := s.ResolveWork(ctx, conflictUser.ID,
		store.Work{ID: "wc", UserID: conflictUser.ID, CreatedAt: time.Now()},
		nil, conflictIDs, false)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(conflict.ConflictingWorkIDs) != "[wa wb]" {
		t.Fatalf("want ordered conflict [wa wb], got %+v", conflict)
	}
	got, err = s.ResolveAliases(ctx, conflictUser.ID,
		[]store.Identifier{{Kind: "source", Value: "catalog:new"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("conflict partially registered alias: %v", got)
	}
	works, err = s.ListWorks(ctx, conflictUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("conflict partially created a work: %+v", works)
	}

	editionUser := MkUser(t, s, "resolver-edition")
	base := store.Work{
		ID: "base", UserID: editionUser.ID, Title: "Base", CreatedAt: time.Now(),
	}
	if err := s.CreateWork(ctx, base, nil,
		[]store.Identifier{{Kind: "source", Value: "catalog:base"}}); err != nil {
		t.Fatal(err)
	}
	editionResult, err := s.ResolveWork(ctx, editionUser.ID,
		store.Work{ID: "unused", UserID: editionUser.ID, CreatedAt: time.Now()},
		[]store.Edition{{UserID: editionUser.ID, SHA256: "new-edition", WorkID: "unused"}},
		[]store.Identifier{
			{Kind: "sha256", Value: "new-edition"},
			{Kind: "source", Value: "catalog:base"},
		}, false)
	if err != nil {
		t.Fatal(err)
	}
	if editionResult.WorkID != base.ID || editionResult.Created {
		t.Fatalf("new edition did not resolve onto base: %+v", editionResult)
	}
	edition, err = s.EditionBySHA(ctx, editionUser.ID, "new-edition")
	if err != nil {
		t.Fatal(err)
	}
	if edition.WorkID != base.ID {
		t.Fatalf("new edition mapped to %q, want %q", edition.WorkID, base.ID)
	}

	multiUser := MkUser(t, s, "resolver-multi-edition")
	multiIDs := []store.Identifier{
		{Kind: "sha256", Value: "multi-a"},
		{Kind: "sha256", Value: "multi-b"},
		{Kind: "source", Value: "catalog:multi"},
	}
	multiResult, err := s.ResolveWork(ctx, multiUser.ID,
		store.Work{ID: "multi", UserID: multiUser.ID, CreatedAt: time.Now()},
		[]store.Edition{
			{UserID: multiUser.ID, SHA256: "multi-a", WorkID: "multi"},
			{UserID: multiUser.ID, SHA256: "multi-b", WorkID: "multi"},
		},
		multiIDs, false)
	if err != nil {
		t.Fatal(err)
	}
	if !multiResult.Created || multiResult.WorkID != "multi" {
		t.Fatalf("multiple editions did not create one work: %+v", multiResult)
	}
	for _, sha := range []string{"multi-a", "multi-b"} {
		edition, err = s.EditionBySHA(ctx, multiUser.ID, sha)
		if err != nil {
			t.Fatal(err)
		}
		if edition.WorkID != "multi" {
			t.Fatalf("edition %q mapped to %q", sha, edition.WorkID)
		}
	}

	fuzzyUser := MkUser(t, s, "resolver-fuzzy")
	fuzzy := store.Work{
		ID: "fuzzy", UserID: fuzzyUser.ID, Title: "Fuzzy", CreatedAt: time.Now(),
	}
	if err := s.CreateWork(ctx, fuzzy, nil,
		[]store.Identifier{{Kind: "ta", Value: "fuzzy|author"}}); err != nil {
		t.Fatal(err)
	}
	fuzzyIDs := []store.Identifier{
		{Kind: "sha256", Value: "fuzzy-sha"},
		{Kind: "ta", Value: "fuzzy|author"},
	}
	fuzzyResult, err := s.ResolveWork(ctx, fuzzyUser.ID,
		store.Work{ID: "unused-fuzzy", UserID: fuzzyUser.ID, CreatedAt: time.Now()},
		[]store.Edition{{UserID: fuzzyUser.ID, SHA256: "fuzzy-sha", WorkID: "unused-fuzzy"}},
		fuzzyIDs, false)
	if err != nil {
		t.Fatal(err)
	}
	if fuzzyResult.WorkID != fuzzy.ID || fuzzyResult.Confidence != "low" {
		t.Fatalf("want low-confidence fuzzy result, got %+v", fuzzyResult)
	}
	if _, err := s.EditionBySHA(ctx, fuzzyUser.ID, "fuzzy-sha"); err != store.ErrNotFound {
		t.Fatalf("unconfirmed fuzzy match promoted edition: %v", err)
	}
	got, err = s.ResolveAliases(ctx, fuzzyUser.ID,
		[]store.Identifier{{Kind: "sha256", Value: "fuzzy-sha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unconfirmed fuzzy match promoted alias: %v", got)
	}
	fuzzyResult, err = s.ResolveWork(ctx, fuzzyUser.ID,
		store.Work{ID: "unused-confirmed", UserID: fuzzyUser.ID, CreatedAt: time.Now()},
		[]store.Edition{{UserID: fuzzyUser.ID, SHA256: "fuzzy-sha", WorkID: "unused-confirmed"}},
		fuzzyIDs, true)
	if err != nil {
		t.Fatal(err)
	}
	if fuzzyResult.WorkID != fuzzy.ID || fuzzyResult.Confidence != "high" {
		t.Fatalf("want confirmed high-confidence result, got %+v", fuzzyResult)
	}
	edition, err = s.EditionBySHA(ctx, fuzzyUser.ID, "fuzzy-sha")
	if err != nil {
		t.Fatal(err)
	}
	if edition.WorkID != fuzzy.ID {
		t.Fatalf("confirmed edition mapped to %q, want %q", edition.WorkID, fuzzy.ID)
	}

	pendingUser := MkUser(t, s, "resolver-pending")
	start = make(chan struct{})
	pendingIDs := make(chan string, workers)
	createdFlags := make(chan bool, workers)
	errs = make(chan error, workers)
	wg = sync.WaitGroup{}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			workID, wasCreated, err := s.CreatePendingWork(ctx, pendingUser.ID, "pending-md5")
			if err != nil {
				errs <- err
				return
			}
			pendingIDs <- workID
			createdFlags <- wasCreated
		}()
	}
	close(start)
	wg.Wait()
	close(pendingIDs)
	close(createdFlags)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent pending resolve: %v", err)
	}
	resolved = ""
	for workID := range pendingIDs {
		if resolved == "" {
			resolved = workID
		} else if workID != resolved {
			t.Fatalf("split pending resolution: %q != %q", workID, resolved)
		}
	}
	created = 0
	for wasCreated := range createdFlags {
		if wasCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("want one pending work creation, got %d", created)
	}
}

func testAppendOps(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "dave")
	w := MkWork(t, s, u, "w1", "abc123")

	op := store.Op{
		OpID: "018e6f1a-0000-7000-8000-000000000001", WorkID: w.ID,
		EditionSHA: Ptr("abc123"), ClientTS: time.Now(), Progression: 0.41,
		Origin: store.OriginNative,
	}
	res, err := s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "applied" || res[0].Seq != 1 {
		t.Fatalf("want applied seq=1, got %+v", res[0])
	}

	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "duplicate" || res[0].Seq != 1 {
		t.Fatalf("want duplicate seq=1, got %+v", res[0])
	}

	op2 := op
	op2.Progression = 0.99
	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op2})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "conflict" {
		t.Fatalf("want conflict, got %+v", res[0])
	}

	op3 := op
	op3.OpID = "018e6f1a-0000-7000-8000-000000000002"
	op4 := op
	op4.OpID = "018e6f1a-0000-7000-8000-000000000003"
	res, err = s.AppendOps(ctx, u.ID, "d-ereader", []store.Op{op3, op4})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Seq != 2 || res[1].Seq != 3 {
		t.Fatalf("want seq 2,3 got %+v", res)
	}
}

func testChangesAndHeads(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "erin")
	w := MkWork(t, s, u, "w1", "abc123")

	var ops []store.Op
	for i := 0; i < 5; i++ {
		ops = append(ops, store.Op{
			OpID:        "op-" + string(rune('a'+i)),
			WorkID:      w.ID,
			EditionSHA:  Ptr("abc123"),
			ClientTS:    time.Now(),
			Progression: float64(i) / 10,
			Origin:      store.OriginNative,
		})
	}
	if _, err := s.AppendOps(ctx, u.ID, "d1", ops); err != nil {
		t.Fatal(err)
	}

	page, err := s.Changes(ctx, u.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) != 2 || !page.HasMore || page.HighWater != 5 || page.ResyncNeeded {
		t.Fatalf("bad page: %+v", page)
	}
	page, err = s.Changes(ctx, u.ID, page.Ops[len(page.Ops)-1].Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) != 3 || page.HasMore {
		t.Fatalf("bad page2: %+v", page)
	}

	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if heads.SnapshotSeq != 5 || len(heads.Ops) != 1 || heads.Ops[0].Seq != 5 {
		t.Fatalf("bad heads: %+v", heads)
	}
}

func testSplitAndMerge(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "fred")
	w := MkWork(t, s, u, "w1", "abc123")

	op := store.Op{
		OpID: "op-split-1", WorkID: w.ID, EditionSHA: Ptr("abc123"),
		ClientTS: time.Now(), Progression: 0.5, Origin: store.OriginNative,
	}
	if _, err := s.AppendOps(ctx, u.ID, "d1", []store.Op{op}); err != nil {
		t.Fatal(err)
	}

	newWork := store.Work{ID: "w2", UserID: u.ID, Title: "split", CreatedAt: time.Now()}
	err := s.SplitWork(ctx, u.ID, w.ID, "abc123",
		[]store.Identifier{{Kind: "dc", Value: "urn:isbn:9780316419568-w1"}},
		newWork)
	if err != nil {
		t.Fatal(err)
	}

	pos, err := s.Positions(ctx, u.ID, "w2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0].OpID != "op-split-1" {
		t.Fatalf("op not moved: %+v", pos)
	}
	pos, err = s.Positions(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 0 {
		t.Fatalf("op still on source: %+v", pos)
	}
	got, _ := s.ResolveAliases(ctx, u.ID, []store.Identifier{
		{Kind: "sha256", Value: "abc123"}, {Kind: "partial-md5", Value: "md5-w1"},
	})
	if got["sha256:abc123"] != "w2" || got["partial-md5:md5-w1"] != w.ID {
		t.Fatalf("bad alias split: %v", got)
	}

	if err := s.SplitWork(ctx, u.ID, "w2", "abc123",
		[]store.Identifier{{Kind: "sha256", Value: "other-edition"}},
		store.Work{ID: "w3", UserID: u.ID, CreatedAt: time.Now()}); err != store.ErrConflict {
		t.Fatalf("moving another edition's sha alias: want conflict, got %v", err)
	}

	if err := s.MergeWorks(ctx, u.ID, "w2", w.ID); err != nil {
		t.Fatal(err)
	}
	pos, err = s.Positions(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 {
		t.Fatalf("merge lost op: %+v", pos)
	}
	if _, err := s.WorkByID(ctx, u.ID, "w2"); err != store.ErrNotFound {
		t.Fatalf("source work should be gone: %v", err)
	}
}

func testSessionsAppendOnly(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "gwen")
	w := MkWork(t, s, u, "w1", "abc123")

	ses := store.Session{
		SessionID: "s1", WorkID: w.ID, EditionSHA: Ptr("abc123"), DeviceID: "d1",
		StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
		StartProg: 0.4, EndProg: 0.45, Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	ses2 := ses
	ses2.EndProg = 0.9
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses2}); err != store.ErrIDMismatch {
		t.Fatalf("want ErrIDMismatch, got %v", err)
	}
}

func testSessionRollups(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "rollup")
	other := MkUser(t, s, "rollup-other")
	w := MkWork(t, s, u, "w1", "rollup-sha")
	old := time.Now().Add(-200 * 24 * time.Hour)
	ses := store.Session{
		SessionID: "old-s1", WorkID: w.ID, EditionSHA: Ptr("rollup-sha"), DeviceID: "d1",
		StartedAt: old, EndedAt: old.Add(time.Hour), StartProg: 0.1, EndProg: 0.2,
		Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	ended, err := s.SessionsEndedBefore(ctx, u.ID, time.Now().Add(-180*24*time.Hour))
	if err != nil || len(ended) != 1 {
		t.Fatalf("sessions for rollup: %d %v", len(ended), err)
	}
	day := old.In(time.FixedZone("test", 3600)).Format("2006-01-02")
	ru := store.SessionRollup{
		UserID: u.ID, WorkID: w.ID, Day: day,
		ActiveSeconds: 3600, Pages: 46.2, ProgDelta: 0.1, SessionCount: 1,
	}
	if err := s.ApplyRollups(ctx, u.ID, []store.SessionRollup{ru}, ended); err != nil {
		t.Fatal(err)
	}
	raw, err := s.SessionsInRange(ctx, u.ID, old.Add(-time.Hour), old.Add(2*time.Hour))
	if err != nil || len(raw) != 0 {
		t.Fatalf("raw session retained: %d %v", len(raw), err)
	}
	got, err := s.RollupsInRange(ctx, u.ID, day, day)
	if err != nil || len(got) != 1 || got[0].SessionCount != 1 || got[0].ActiveSeconds != 3600 {
		t.Fatalf("rollup: %+v %v", got, err)
	}
	if cross, err := s.RollupsInRange(ctx, other.ID, day, day); err != nil || len(cross) != 0 {
		t.Fatalf("cross-user rollup read: %+v %v", cross, err)
	}
	// The compact tombstone preserves append idempotency after deletion.
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatalf("archived duplicate: %v", err)
	}
	changed := ses
	changed.EndProg = 0.3
	if err := s.AppendSessions(ctx, u.ID, []store.Session{changed}); err != store.ErrIDMismatch {
		t.Fatalf("archived mismatch: want ErrIDMismatch, got %v", err)
	}
	MkWork(t, s, u, "rollup-target", "rollup-target-sha")
	if err := s.SplitWork(ctx, u.ID, w.ID, "rollup-sha", nil,
		store.Work{ID: "rollup-split", UserID: u.ID, CreatedAt: time.Now()}); err != store.ErrConflict {
		t.Fatalf("split with compacted history: want conflict, got %v", err)
	}
	if err := s.MergeWorks(ctx, u.ID, w.ID, "rollup-target"); err != store.ErrConflict {
		t.Fatalf("merge with compacted history: want conflict, got %v", err)
	}

	staleUser := MkUser(t, s, "rollup-stale")
	staleWork := MkWork(t, s, staleUser, "stale-w1", "stale-sha")
	staleSession := store.Session{
		SessionID: "stale-session", WorkID: staleWork.ID, EditionSHA: Ptr("stale-sha"), DeviceID: "d1",
		StartedAt: old, EndedAt: old.Add(time.Hour), StartProg: 0.1, EndProg: 0.2,
		Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, staleUser.ID, []store.Session{staleSession}); err != nil {
		t.Fatal(err)
	}
	staleSnapshot, err := s.SessionsEndedBefore(ctx, staleUser.ID, time.Now())
	if err != nil || len(staleSnapshot) != 1 {
		t.Fatalf("stale snapshot: %+v %v", staleSnapshot, err)
	}
	if err := s.SplitWork(ctx, staleUser.ID, staleWork.ID, "stale-sha", nil,
		store.Work{ID: "stale-w2", UserID: staleUser.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	staleRollup := store.SessionRollup{
		UserID: staleUser.ID, WorkID: staleWork.ID, Day: day,
		ActiveSeconds: 3600, Pages: 1, ProgDelta: 0.1, SessionCount: 1,
	}
	if err := s.ApplyRollups(ctx, staleUser.ID, []store.SessionRollup{staleRollup}, staleSnapshot); err != store.ErrConflict {
		t.Fatalf("stale rollup snapshot: want conflict, got %v", err)
	}
	if got, err := s.SessionsForWork(ctx, staleUser.ID, "stale-w2", 10); err != nil || len(got) != 1 {
		t.Fatalf("stale rollup deleted moved session: %+v %v", got, err)
	}
}

func testHousekeeping(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cleanup")
	now := time.Now()
	expired := now.Add(-store.TokenPurgeGrace - time.Hour)
	if err := s.CreateToken(ctx, store.Token{
		ID: "expired-token", UserID: u.ID, DeviceID: "d1", Name: "old",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "expired-token-hash", CreatedAt: expired,
		ExpiresAt: &expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession(ctx, store.AuthSession{
		ID: "expired-auth", UserID: u.ID, SHA256: "expired-auth-hash", Kind: "web",
		CreatedAt: expired, ExpiresAt: expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePairingCode(ctx, store.PairingCode{
		ID: "expired-pair", UserID: u.ID, CodeSHA256: "expired-pair-hash", ExpiresAt: expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Housekeep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TokenByHash(ctx, u.ID, "expired-token-hash"); err != store.ErrNotFound {
		t.Fatalf("expired token retained: %v", err)
	}
	if _, err := s.AuthSessionByHash(ctx, "expired-auth-hash"); err != store.ErrNotFound {
		t.Fatalf("expired auth session retained: %v", err)
	}
	if _, err := s.RedeemPairingCode(ctx, "expired-pair-hash", now); err != store.ErrNotFound {
		t.Fatalf("expired pairing code retained: %v", err)
	}
}

func testConcurrentAppend(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "prop")
	w := MkWork(t, s, u, "w1", "abc123")

	const devices = 8
	const opsPerDevice = 25

	var wg sync.WaitGroup
	errs := make(chan error, devices)
	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			dev := fmt.Sprintf("dev-%d", d)
			for b := 0; b < opsPerDevice; b += 5 {
				var batch []store.Op
				for i := 0; i < 5 && b+i < opsPerDevice; i++ {
					n := b + i
					batch = append(batch, store.Op{
						OpID:        fmt.Sprintf("%s-op-%d", dev, n),
						WorkID:      w.ID,
						EditionSHA:  Ptr("abc123"),
						ClientTS:    time.Now(),
						Progression: float64(n) / 100,
						Origin:      store.OriginNative,
					})
				}
				res, err := s.AppendOps(ctx, u.ID, dev, batch)
				if err != nil {
					errs <- err
					return
				}
				for _, r := range res {
					if r.Status != "applied" {
						errs <- fmt.Errorf("%s: op %s status %s", dev, r.OpID, r.Status)
						return
					}
				}
			}
		}(d)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	page, err := s.Changes(ctx, u.ID, 0, devices*opsPerDevice+10)
	if err != nil {
		t.Fatal(err)
	}
	want := devices * opsPerDevice
	if len(page.Ops) != want {
		t.Fatalf("want %d ops, got %d", want, len(page.Ops))
	}
	for i, o := range page.Ops {
		if o.Seq != int64(i+1) {
			t.Fatalf("gap at position %d: seq=%d", i, o.Seq)
		}
	}
}

func testPairingRedeem(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "henry")

	pc := store.PairingCode{
		ID: "p1", UserID: u.ID, CodeSHA256: "hash1",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := s.CreatePairingCode(ctx, pc); err != nil {
		t.Fatal(err)
	}
	got, err := s.RedeemPairingCode(ctx, "hash1", time.Now())
	if err != nil || got.UserID != u.ID {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	// Single use.
	if _, err := s.RedeemPairingCode(ctx, "hash1", time.Now()); err != store.ErrConflict {
		t.Fatalf("double redeem: want ErrConflict, got %v", err)
	}
	// Expired.
	pc2 := store.PairingCode{
		ID: "p2", UserID: u.ID, CodeSHA256: "hash2",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := s.CreatePairingCode(ctx, pc2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairingCode(ctx, "hash2", time.Now()); err != store.ErrConflict {
		t.Fatalf("expired redeem: want ErrConflict, got %v", err)
	}
}

func testKopluginUpsert(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "ida")
	w := MkWork(t, s, u, "w1", "abc123")

	key := "d1|md5x|42|1754800000"
	mk := func(sessID string, dur time.Duration) store.Session {
		return store.Session{
			SessionID: sessID, WorkID: w.ID, DeviceID: "koplugin:kobo",
			StartedAt: time.Unix(1754800000, 0), EndedAt: time.Unix(1754800000, 0).Add(dur),
			StartProg: 41.0 / 300, EndProg: 42.0 / 300,
			Origin: store.OriginKoplugin, SourceKey: Ptr(key),
		}
	}

	st, err := s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "inserted" {
		t.Fatalf("insert: %q %v", st, err)
	}
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "duplicate" {
		t.Fatalf("dup: %q %v", st, err)
	}
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v2", 20*time.Minute))
	if err != nil || st != "superseded" {
		t.Fatalf("supersede: %q %v", st, err)
	}
	// Re-upload of the first revision is a duplicate, not a new supersession.
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "duplicate" {
		t.Fatalf("old revision re-upload: %q %v", st, err)
	}
}

func testLegacyAliasWritesAtomic(t *testing.T, open OpenFunc) {
	t.Run("KosyncSplit", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		u := MkUser(t, s, "kosync-split")
		w := MkWork(t, s, u, "w1", "sha1")
		alias := "md5-w1"
		op := store.Op{
			OpID: "kosync-split-op", ClientTS: time.Now(), Progression: 0.4,
			Origin: store.OriginKosync, OriginAlias: Ptr("partial-md5:" + alias),
		}
		runConcurrent(t,
			func() error {
				result, err := s.AppendKosyncOp(ctx, u.ID, alias, "kosync:kobo", op)
				if err == nil && result.Status == "conflict" {
					return fmt.Errorf("append conflict: %+v", result)
				}
				return err
			},
			func() error {
				return s.SplitWork(ctx, u.ID, w.ID, "sha1",
					[]store.Identifier{{Kind: "partial-md5", Value: alias}},
					store.Work{ID: "w2", UserID: u.ID, CreatedAt: time.Now()})
			})
		assertOpFollowsAlias(t, s, u.ID, alias, op.OpID)
	})

	t.Run("KosyncMerge", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		u := MkUser(t, s, "kosync-merge")
		MkWork(t, s, u, "w1", "sha1")
		MkWork(t, s, u, "w2", "sha2")
		alias := "md5-w1"
		op := store.Op{
			OpID: "kosync-merge-op", ClientTS: time.Now(), Progression: 0.4,
			Origin: store.OriginKosync, OriginAlias: Ptr("partial-md5:" + alias),
		}
		runConcurrent(t,
			func() error {
				result, err := s.AppendKosyncOp(ctx, u.ID, alias, "kosync:kobo", op)
				if err == nil && result.Status == "conflict" {
					return fmt.Errorf("append conflict: %+v", result)
				}
				return err
			},
			func() error { return s.MergeWorks(ctx, u.ID, "w1", "w2") })
		assertOpFollowsAlias(t, s, u.ID, alias, op.OpID)
	})

	t.Run("KopluginSplit", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		u := MkUser(t, s, "koplugin-split")
		w := MkWork(t, s, u, "w1", "sha1")
		alias := "md5-w1"
		ses := legacySession("koplugin-split-session", alias)
		runConcurrent(t,
			func() error {
				_, err := s.UpsertKopluginSessionByAlias(ctx, u.ID, alias, ses)
				return err
			},
			func() error {
				return s.SplitWork(ctx, u.ID, w.ID, "sha1",
					[]store.Identifier{{Kind: "partial-md5", Value: alias}},
					store.Work{ID: "w2", UserID: u.ID, CreatedAt: time.Now()})
			})
		assertSessionFollowsAlias(t, s, u.ID, alias, ses.SessionID)
	})

	t.Run("KopluginMerge", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		u := MkUser(t, s, "koplugin-merge")
		MkWork(t, s, u, "w1", "sha1")
		MkWork(t, s, u, "w2", "sha2")
		alias := "md5-w1"
		ses := legacySession("koplugin-merge-session", alias)
		runConcurrent(t,
			func() error {
				_, err := s.UpsertKopluginSessionByAlias(ctx, u.ID, alias, ses)
				return err
			},
			func() error { return s.MergeWorks(ctx, u.ID, "w1", "w2") })
		assertSessionFollowsAlias(t, s, u.ID, alias, ses.SessionID)
	})

	t.Run("NativeSessionMerge", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		u := MkUser(t, s, "native-session-merge")
		MkWork(t, s, u, "w1", "sha1")
		MkWork(t, s, u, "w2", "sha2")
		ses := store.Session{
			SessionID: "native-merge-session", WorkID: "w1", DeviceID: "phone",
			StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
			StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
		}
		start := make(chan struct{})
		appendResult := make(chan error, 1)
		mergeResult := make(chan error, 1)
		go func() {
			<-start
			appendResult <- s.AppendSessions(ctx, u.ID, []store.Session{ses})
		}()
		go func() {
			<-start
			mergeResult <- s.MergeWorks(ctx, u.ID, "w1", "w2")
		}()
		close(start)
		appendErr := <-appendResult
		if err := <-mergeResult; err != nil {
			t.Fatal(err)
		}
		if appendErr == nil {
			sessions, err := s.SessionsForWork(ctx, u.ID, "w2", 10)
			if err != nil || len(sessions) != 1 || sessions[0].SessionID != ses.SessionID {
				t.Fatalf("accepted native session lost during merge: %+v %v", sessions, err)
			}
		}
	})
}

func testInferredSessionIdentity(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "inferred")
	w := MkWork(t, s, u, "w1", "sha1")
	alias := "md5-w1"
	for i, progression := range []float64{0.4, 0.5} {
		op := store.Op{
			OpID: fmt.Sprintf("inferred-op-%d", i), ClientTS: time.Now(), Progression: progression,
			Origin: store.OriginKosync, OriginAlias: Ptr("partial-md5:" + alias),
		}
		if _, err := s.AppendKosyncOp(ctx, u.ID, alias, "kosync:kobo", op); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.PendingInferenceOps(ctx, u.ID)
	if err != nil || len(before) != 2 {
		t.Fatalf("ops before split: %+v %v", before, err)
	}
	first := infer.Materialize(u.ID, before)
	if first.OriginAlias == nil || *first.OriginAlias != "partial-md5:"+alias {
		t.Fatalf("inferred session lost origin alias: %+v", first)
	}

	if err := s.SplitWork(ctx, u.ID, w.ID, "sha1",
		[]store.Identifier{{Kind: "partial-md5", Value: alias}},
		store.Work{ID: "w2", UserID: u.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendInferredSession(ctx, u.ID,
		store.InferredSessionGroup{Session: first, Ops: before}); err != store.ErrConflict {
		t.Fatalf("stale inferred snapshot: want conflict, got %v", err)
	}
	if _, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterSplit, err := s.PendingInferenceOps(ctx, u.ID)
	if err != nil || len(afterSplit) != 2 {
		t.Fatalf("ops after split: %+v %v", afterSplit, err)
	}
	for _, op := range afterSplit {
		if op.WorkID != "w2" {
			t.Fatalf("split did not move pending inference op: %+v", op)
		}
	}
	second := infer.Materialize(u.ID, afterSplit)
	if err := s.AppendInferredSession(ctx, u.ID,
		store.InferredSessionGroup{Session: second, Ops: afterSplit}); err != nil {
		t.Fatalf("materialize after split: %v", err)
	}
	if pending, err := s.PendingInferenceOps(ctx, u.ID); err != nil || len(pending) != 0 {
		t.Fatalf("materialized ops remained pending: %+v %v", pending, err)
	}
	sessions, err := s.SessionsForWork(ctx, u.ID, "w2", 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("split duplicated inferred session: %+v %v", sessions, err)
	}

	MkWork(t, s, u, "w3", "sha3")
	if err := s.MergeWorks(ctx, u.ID, "w2", "w3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingInferenceOps(ctx, u.ID); err != nil || len(pending) != 0 {
		t.Fatalf("compacted materialized ops became pending: %+v %v", pending, err)
	}
	sessions, err = s.SessionsForWork(ctx, u.ID, "w3", 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("merge duplicated inferred session: %+v %v", sessions, err)
	}

	day := sessions[0].EndedAt.UTC().Format("2006-01-02")
	rollup := store.SessionRollup{
		UserID: u.ID, WorkID: "w3", Day: day,
		ActiveSeconds: 1, ProgDelta: 0.1, SessionCount: 1,
	}
	if err := s.ApplyRollups(ctx, u.ID, []store.SessionRollup{rollup}, sessions); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingInferenceOps(ctx, u.ID); err != nil || len(pending) != 0 {
		t.Fatalf("rolled-up materialized ops became pending: %+v %v", pending, err)
	}
	rollups, err := s.RollupsForWork(ctx, u.ID, "w3")
	if err != nil || len(rollups) != 1 || rollups[0].SessionCount != 1 {
		t.Fatalf("materialize/compact/rollup duplicated totals: %+v %v", rollups, err)
	}

	legacyUser := MkUser(t, s, "inferred-legacy")
	legacyWork := MkWork(t, s, legacyUser, "legacy-w1", "legacy-sha")
	legacySession := store.Session{
		SessionID: "legacy-inferred", WorkID: legacyWork.ID, DeviceID: "kosync:kobo",
		StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
		StartProg: 0.1, EndProg: 0.2, Origin: store.OriginInferred,
	}
	if err := s.AppendSessions(ctx, legacyUser.ID, []store.Session{legacySession}); err != nil {
		t.Fatal(err)
	}
	if err := s.SplitWork(ctx, legacyUser.ID, legacyWork.ID, "legacy-sha", nil,
		store.Work{ID: "legacy-w2", UserID: legacyUser.ID, CreatedAt: time.Now()}); err != store.ErrConflict {
		t.Fatalf("split with unpartitionable inferred history: want conflict, got %v", err)
	}
}

func runConcurrent(t *testing.T, first, second func() error) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, fn := range []func() error{first, second} {
		go func(fn func() error) {
			<-start
			errs <- fn()
		}(fn)
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func legacySession(id, alias string) store.Session {
	started := time.Unix(1754800000, 0)
	return store.Session{
		SessionID: id, DeviceID: "koplugin:kobo",
		StartedAt: started, EndedAt: started.Add(15 * time.Minute),
		StartProg: 0.4, EndProg: 0.5, Origin: store.OriginKoplugin,
		OriginAlias: Ptr("partial-md5:" + alias), SourceKey: Ptr(id + "-source"),
	}
}

func assertOpFollowsAlias(t *testing.T, s store.Store, userID, alias, opID string) {
	t.Helper()
	workID, err := s.WorkIDByAlias(t.Context(), userID, "partial-md5", alias)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := s.Positions(t.Context(), userID, workID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.OpID == opID {
			return
		}
	}
	t.Fatalf("op %q did not follow alias to work %q: %+v", opID, workID, ops)
}

func assertSessionFollowsAlias(t *testing.T, s store.Store, userID, alias, sessionID string) {
	t.Helper()
	workID, err := s.WorkIDByAlias(t.Context(), userID, "partial-md5", alias)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := s.SessionsForWork(t.Context(), userID, workID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.SessionID == sessionID {
			return
		}
	}
	t.Fatalf("session %q did not follow alias to work %q: %+v", sessionID, workID, sessions)
}
