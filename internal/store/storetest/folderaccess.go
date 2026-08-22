package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testFolderAccess(t *testing.T, open OpenFunc) {
	t.Helper()
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	reader := store.User{
		ID: "grant-reader", Name: "grant-reader", Argon2Hash: "x",
		Timezone: "UTC", CreatedAt: now,
	}
	admin := store.User{
		ID: "grant-admin", Name: "grant-admin", Argon2Hash: "x",
		Timezone: "UTC", IsAdmin: true, CreatedAt: now,
	}
	for _, user := range []store.User{reader, admin} {
		if err := s.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	folders := []store.Folder{
		{ID: "grant-a", Name: "A", RootPath: "/a", Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now},
		{ID: "grant-b", Name: "B", RootPath: "/b", Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now},
	}
	for _, folder := range folders {
		if err := s.CreateFolder(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}
	for _, userID := range []string{reader.ID, admin.ID} {
		got, err := s.ListFolders(ctx, userID, "", 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("new account %s folders = %+v, %v", userID, got, err)
		}
	}
	if got, err := s.ListFolders(ctx, "", "", 10); err != nil || len(got) != 2 {
		t.Fatalf("trusted folder list = %+v, %v", got, err)
	}

	if err := s.AssignUserFolder(ctx, reader.ID, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUserFolder(ctx, reader.ID, folders[0].ID); err != nil {
		t.Fatalf("duplicate assignment: %v", err)
	}
	if _, err := s.FolderByID(ctx, reader.ID, folders[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ungranted folder lookup = %v", err)
	}

	observed := func(path, sha, title, author string) store.ObservedBook {
		return store.ObservedBook{
			RelativePath: path, SizeBytes: 1, MTime: now,
			ContentSHA256: sha, OriginalFilename: path,
			MediaType: "application/epub+zip", Title: title,
			Contributors: []store.ObservedContributor{{Name: author, Role: store.ContributorRoleAuthor}},
			Tags:         []string{"Visible Tag"},
			Series:       []store.ObservedSeries{{Name: "Visible Series"}},
		}
	}
	if _, err := s.ReconcileFolder(ctx, folders[0].ID,
		[]store.ObservedBook{observed("a.epub", "sha-a", "A", "Author Z")}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileFolder(ctx, folders[1].ID,
		[]store.ObservedBook{observed("b.epub", "sha-b", "B", "Author A")}, true, now); err != nil {
		t.Fatal(err)
	}
	aBooks, err := s.ListCatalogBooks(ctx, reader.ID, folders[0].ID, nil, 10)
	if err != nil || len(aBooks) != 1 {
		t.Fatalf("granted books = %+v, %v", aBooks, err)
	}
	if _, err := s.ListCatalogBooks(ctx, reader.ID, folders[1].ID, nil, 10); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ungranted book list = %v", err)
	}
	if _, err := s.CatalogBookByDigest(ctx, reader.ID, "sha-b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden digest = %v", err)
	}
	result, err := s.SearchCatalogBooks(ctx, store.SearchQuery{
		UserID: reader.ID, FolderID: folders[0].ID, Text: "A", Limit: 10,
	})
	if err != nil || len(result.Books) != 1 {
		t.Fatalf("granted search = %+v, %v", result, err)
	}
	for _, kind := range []store.EntityKind{store.EntitySeries, store.EntityContributor, store.EntityTag} {
		entities, err := s.ListCatalogEntities(ctx, reader.ID, kind, "", 10)
		if err != nil || len(entities) != 1 || entities[0].BookCount != 1 {
			t.Fatalf("%s entities = %+v, %v", kind, entities, err)
		}
	}
	contributors, err := s.ListCatalogEntities(ctx, reader.ID, store.EntityContributor, "", 1)
	if err != nil || len(contributors) != 1 || contributors[0].Name != "Author Z" {
		t.Fatalf("hidden entity consumed the first page: %+v, %v", contributors, err)
	}
	allContributors, err := s.ListCatalogEntities(ctx, "", store.EntityContributor, "", 10)
	if err != nil || len(allContributors) != 2 {
		t.Fatalf("trusted contributors = %+v, %v", allContributors, err)
	}
	if _, err := s.CatalogEntityByID(ctx, reader.ID, allContributors[0].ID,
		store.EntityContributor); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden entity lookup = %v", err)
	}

	work := store.Work{ID: "grant-work", UserID: reader.ID, Title: "A", CreatedAt: now}
	resolved, err := s.ResolveCatalogBookWork(ctx, reader.ID, aBooks[0].ID, work, nil, nil, true, now)
	if err != nil || resolved.WorkID != work.ID {
		t.Fatalf("resolve = %+v, %v", resolved, err)
	}
	if _, err := s.AppendOps(ctx, reader.ID, "grant-device", []store.Op{{
		OpID: "grant-op", WorkID: work.ID, ClientTS: now,
		Progression: 0.5, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSessions(ctx, reader.ID, []store.Session{{
		SessionID: "grant-session", WorkID: work.ID, DeviceID: "grant-device",
		StartedAt: now.Add(-time.Minute), EndedAt: now,
		StartProg: 0.4, EndProg: 0.5, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnassignUserFolder(ctx, reader.ID, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBookWork(ctx, reader.ID, aBooks[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked mapping was visible: %v", err)
	}
	if _, err := s.WorkByID(ctx, reader.ID, work.ID); err != nil {
		t.Fatalf("revocation removed reading history: %v", err)
	}
	if positions, err := s.Positions(ctx, reader.ID, work.ID, 10); err != nil || len(positions) != 1 {
		t.Fatalf("revocation removed positions: %+v, %v", positions, err)
	}
	if sessions, err := s.SessionsForWork(ctx, reader.ID, work.ID, 10); err != nil || len(sessions) != 1 {
		t.Fatalf("revocation removed sessions: %+v, %v", sessions, err)
	}
	if workIDs, err := s.WorkIDsWithInsights(ctx, reader.ID); err != nil || len(workIDs) != 1 || workIDs[0] != work.ID {
		t.Fatalf("revocation removed statistics: %+v, %v", workIDs, err)
	}
	if err := s.AssignUserFolder(ctx, reader.ID, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	if link, err := s.UserBookWork(ctx, reader.ID, aBooks[0].ID); err != nil || link.WorkID != work.ID {
		t.Fatalf("restored mapping = %+v, %v", link, err)
	}

	if err := s.ReplaceUserFolders(ctx, reader.ID, []string{folders[0].ID, folders[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceUserFolders(ctx, reader.ID, []string{folders[1].ID, "missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid replacement = %v", err)
	}
	grants, err := s.ListUserFolders(ctx, reader.ID, "", 10)
	if err != nil || len(grants) != 1 || grants[0].ID != folders[0].ID {
		t.Fatalf("invalid replacement changed grants: %+v, %v", grants, err)
	}
	if err := s.ReplaceUserFolders(ctx, reader.ID, nil); err != nil {
		t.Fatal(err)
	}
	if grants, err := s.ListUserFolders(ctx, reader.ID, "", 10); err != nil || len(grants) != 0 {
		t.Fatalf("empty replacement = %+v, %v", grants, err)
	}
	if err := s.AssignUserFolder(ctx, reader.ID, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	if grants, err := s.ListUserFolders(ctx, reader.ID, "", 10); err != nil || len(grants) != 0 {
		t.Fatalf("folder cascade = %+v, %v", grants, err)
	}
}
