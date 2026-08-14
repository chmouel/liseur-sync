package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testAdminLibraries covers ADR-0013's named ACL bypass: the panel sees
// every library without holding a grant on any of them, pages in a
// stable order, and its writes land where the owner-facing methods
// would have refused.
func testAdminLibraries(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	bob := MkUser(t, s, "bob")
	admin := MkUser(t, s, "zoe-admin")
	now := time.Now().UTC().Truncate(time.Second)

	mk := func(id, owner, name string) store.Library {
		l := store.Library{
			ID: id, OwnerUserID: owner, QuotaUserID: owner,
			Kind: store.LibraryManaged, Name: name,
			ConfigJSON: []byte(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := s.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	// Two libraries share a name across owners: the case a name-only
	// cursor would skip.
	la := mk("lib-a", alice.ID, "Books")
	lb := mk("lib-b", bob.ID, "Books")
	lc := mk("lib-c", bob.ID, "Comics")

	// The whole instance, in one order, with no grant to the reader.
	all, err := s.AdminListLibraries(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("libraries = %d, want 3", len(all))
	}
	if all[0].ID != la.ID || all[1].ID != lb.ID || all[2].ID != lc.ID {
		t.Fatalf("order = %s %s %s", all[0].ID, all[1].ID, all[2].ID)
	}

	// Paging: one row at a time, no repeat and no skip.
	var seen []string
	cursor := ""
	for range 5 {
		page, err := s.AdminListLibraries(ctx, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, page[0].ID)
		cursor = store.LibraryCursor(page[0])
	}
	if len(seen) != 3 || seen[0] != la.ID || seen[1] != lb.ID || seen[2] != lc.ID {
		t.Fatalf("paged ids = %v", seen)
	}

	// Grants: the owner is not one, and a grant aimed at the owner is
	// refused rather than silently written.
	if grants, err := s.AdminLibraryGrants(ctx, lb.ID, 10); err != nil || len(grants) != 0 {
		t.Fatalf("fresh library grants = %v, %v", grants, err)
	}
	read := store.LibraryRoleRead
	manage := store.LibraryRoleManage
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, lb.ID, bob.ID, &read, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("granting the owner: want ErrNotFound, got %v", err)
	}
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, "nope", alice.ID, &read, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("granting on a missing library: want ErrNotFound, got %v", err)
	}
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, lb.ID, alice.ID, &read, now); err != nil {
		t.Fatal(err)
	}
	grants, err := s.AdminLibraryGrants(ctx, lb.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].UserID != alice.ID ||
		grants[0].UserName != "alice" || grants[0].Role != store.LibraryRoleRead {
		t.Fatalf("grants = %+v", grants)
	}

	// The grant is real: Alice reaches Bob's library at read, and the
	// admin still holds nothing.
	if _, err := s.LibraryByID(ctx, alice.ID, lb.ID, store.LibraryRoleRead); err != nil {
		t.Fatalf("granted read: %v", err)
	}
	if _, err := s.LibraryByID(ctx, admin.ID, lb.ID, store.LibraryRoleRead); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the admin must not gain access by administering: %v", err)
	}

	// Upgrading a grant replaces the role rather than adding a row.
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, lb.ID, alice.ID, &manage, now); err != nil {
		t.Fatal(err)
	}
	grants, _ = s.AdminLibraryGrants(ctx, lb.ID, 10)
	if len(grants) != 1 || grants[0].Role != store.LibraryRoleManage {
		t.Fatalf("upgraded grants = %+v", grants)
	}

	// What one account can reach, ownership included as manage.
	mine, err := s.AdminUserLibraries(ctx, alice.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("alice reaches %d libraries, want 2", len(mine))
	}
	for _, l := range mine {
		if l.Role != store.LibraryRoleManage {
			t.Fatalf("role for %s = %q", l.Library.ID, l.Role)
		}
	}
	if none, err := s.AdminUserLibraries(ctx, admin.ID, "", 10); err != nil || len(none) != 0 {
		t.Fatalf("the admin owns nothing: %v, %v", none, err)
	}

	// Revoking is the nil role, and it is idempotent only in the sense
	// that the second call says there was nothing there.
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, lb.ID, alice.ID, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminSetLibraryAccess(ctx, admin.ID, lb.ID, alice.ID, nil, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoking twice: want ErrNotFound, got %v", err)
	}
	if _, err := s.LibraryByID(ctx, alice.ID, lb.ID, store.LibraryRoleRead); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked access still reads: %v", err)
	}

	// Config, on a library the admin does not manage.
	later := now.Add(time.Minute)
	if err := s.AdminSetLibraryConfig(ctx, admin.ID, lc.ID, []byte(`{"layout":"author"}`), later); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryByID(ctx, bob.ID, lc.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Library.ConfigJSON) != `{"layout":"author"}` {
		t.Fatalf("config = %s", got.Library.ConfigJSON)
	}
	if !got.Library.UpdatedAt.Equal(later) {
		t.Fatalf("updated_at = %s, want %s", got.Library.UpdatedAt, later)
	}
	if err := s.AdminSetLibraryConfig(ctx, admin.ID, "nope", []byte(`{}`), later); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("config on a missing library: want ErrNotFound, got %v", err)
	}
}
