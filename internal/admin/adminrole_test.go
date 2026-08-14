package admin

import (
	"strings"
	"testing"
)

// TestGrantAndRevokeAdmin covers ADR-0013's CLI entry point: the first
// administrator is made out of band, with no credential handed over,
// and the last one cannot be demoted into an instance nobody can
// administer.
func TestGrantAndRevokeAdmin(t *testing.T) {
	st := newAdminStore(t)
	alice := addUser(t, st, "alice")
	bob := addUser(t, st, "bob")

	if err := setAdminCmd(t.Context(), st, []string{"alice"}, true); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin {
		t.Fatal("alice should be an administrator")
	}

	err = setAdminCmd(t.Context(), st, []string{"alice"}, false)
	if err == nil || !strings.Contains(err.Error(), "last enabled administrator") {
		t.Fatalf("want a last-admin refusal, got %v", err)
	}

	if err := setAdminCmd(t.Context(), st, []string{"bob"}, true); err != nil {
		t.Fatal(err)
	}
	if err := setAdminCmd(t.Context(), st, []string{"alice"}, false); err != nil {
		t.Fatal(err)
	}
	got, err = st.UserByID(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsAdmin {
		t.Fatal("alice should have been demoted")
	}
	if got, err = st.UserByID(t.Context(), bob.ID); err != nil || !got.IsAdmin {
		t.Fatalf("bob should still be an administrator: %v err=%v", got.IsAdmin, err)
	}

	if err := setAdminCmd(t.Context(), st, []string{"nobody"}, true); err == nil {
		t.Fatal("want an error for an unknown user")
	}
	if err := setAdminCmd(t.Context(), st, nil, true); err == nil {
		t.Fatal("want a usage error with no arguments")
	}
}

// TestMintAdminTokenNeedsTheRole is the other half: an admin-scoped
// token is a credential for a role the account must already hold, and
// the refusal has to say how to fix it.
func TestMintAdminTokenNeedsTheRole(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "alice")

	err := mintToken(t.Context(), st, []string{"-scope", "admin", "alice", "laptop"})
	if err == nil || !strings.Contains(err.Error(), "grant-admin") {
		t.Fatalf("want a refusal pointing at grant-admin, got %v", err)
	}

	if err := setAdminCmd(t.Context(), st, []string{"alice"}, true); err != nil {
		t.Fatal(err)
	}
	if err := mintToken(t.Context(), st, []string{"-scope", "admin", "alice", "laptop"}); err != nil {
		t.Fatalf("admin account: %v", err)
	}
}
