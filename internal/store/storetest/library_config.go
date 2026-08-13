package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testLibraryConfig covers the configuration document a library carries:
// who may write it, that it reads back byte for byte, and that it cannot be
// reached across the ACL.
func testLibraryConfig(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "config-owner")
	manager := MkUser(t, s, "config-manager")
	reader := MkUser(t, s, "config-reader")
	outsider := MkUser(t, s, "config-outsider")
	now := time.Now().UTC().Truncate(time.Second)

	library := store.Library{
		ID: "lib-config", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Configured", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	other := store.Library{
		ID: "lib-config-other", OwnerUserID: outsider.ID, QuotaUserID: outsider.ID,
		Kind: store.LibraryManaged, Name: "Untouched", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, manager.ID, store.LibraryRoleManage, now); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, library.ID, reader.ID, store.LibraryRoleRead, now); err != nil {
		t.Fatal(err)
	}

	document := []byte(`{"path_patterns":["series/author-title"],"keep":1}`)
	updated := now.Add(time.Minute)
	if err := s.SetLibraryConfig(ctx, owner.ID, library.ID, document, updated); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryByID(ctx, reader.ID, library.ID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Library.ConfigJSON) != string(document) {
		t.Fatalf("config read back as %q, want %q", got.Library.ConfigJSON, document)
	}
	if !got.Library.UpdatedAt.Equal(updated) {
		t.Fatalf("updated_at is %s, want %s", got.Library.UpdatedAt, updated)
	}

	// A manager may write it; a reader may not, and a failed write leaves
	// the document alone rather than blanking it.
	managed := []byte(`{"path_patterns":[]}`)
	if err := s.SetLibraryConfig(ctx, manager.ID, library.ID, managed, updated); err != nil {
		t.Fatal(err)
	}
	for name, actor := range map[string]string{
		"reader":   reader.ID,
		"outsider": outsider.ID,
	} {
		if err := s.SetLibraryConfig(
			ctx, actor, library.ID, []byte(`{"path_patterns":["author/title"]}`), updated,
		); err != store.ErrNotFound {
			t.Fatalf("%s wrote library config: %v", name, err)
		}
	}
	if err := s.SetLibraryConfig(
		ctx, owner.ID, "lib-missing", managed, updated); err != store.ErrNotFound {
		t.Fatalf("unknown library: want ErrNotFound, got %v", err)
	}
	got, err = s.LibraryByID(ctx, owner.ID, library.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Library.ConfigJSON) != string(managed) {
		t.Fatalf("config is %q after refused writes, want %q",
			got.Library.ConfigJSON, managed)
	}

	// A write names one library. The owner of another library must not see
	// their own document change because someone edited this one.
	untouched, err := s.LibraryByID(ctx, outsider.ID, other.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Library.ConfigJSON != nil {
		t.Fatalf("unrelated library config changed to %q", untouched.Library.ConfigJSON)
	}

	// Clearing is a write of nothing, not a refusal to write.
	if err := s.SetLibraryConfig(ctx, owner.ID, library.ID, nil, updated); err != nil {
		t.Fatal(err)
	}
	got, err = s.LibraryByID(ctx, owner.ID, library.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Library.ConfigJSON != nil {
		t.Fatalf("config is %q after clearing, want nil", got.Library.ConfigJSON)
	}
}
