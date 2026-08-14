package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func newAuthStore(t *testing.T) store.Store {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func addAuthUser(t *testing.T, st store.Store, id string) {
	t.Helper()
	err := st.CreateUser(t.Context(), store.User{
		ID:         id,
		Name:       id,
		Argon2Hash: "x",
		Timezone:   "UTC",
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestIsAdminReadsTheAccount pins ADR-0013's central change: admin is a
// property of the account, not of a credential. A user with no token at
// all is an admin once the flag is set, and stops being one when it is
// cleared.
func TestIsAdminReadsTheAccount(t *testing.T) {
	st := newAuthStore(t)
	svc := NewService(st)
	addAuthUser(t, st, "u1")
	addAuthUser(t, st, "u2")

	if ok, err := svc.IsAdmin(t.Context(), "u1"); err != nil || ok {
		t.Fatalf("fresh account: want false, got %v err=%v", ok, err)
	}
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.IsAdmin(t.Context(), "u1"); err != nil || !ok {
		t.Fatalf("after grant: want true, got %v err=%v", ok, err)
	}
	if ok, err := svc.IsAdmin(t.Context(), "u2"); err != nil || ok {
		t.Fatalf("other account: want false, got %v err=%v", ok, err)
	}
	if ok, err := svc.IsAdmin(t.Context(), "missing"); err != nil || ok {
		t.Fatalf("missing account: want false and no error, got %v err=%v", ok, err)
	}

	// u2 is promoted first so the demotion is not blocked by the
	// last-admin guard.
	if err := st.SetUserAdmin(t.Context(), "u2", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAdmin(t.Context(), "u1", false); err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.IsAdmin(t.Context(), "u1"); err != nil || ok {
		t.Fatalf("after revoke: want false, got %v err=%v", ok, err)
	}
}

// TestAdminTokenRejectedOnNonAdminAccount is the second fence described
// in ADR-0013: demotion revokes admin-scoped tokens in the same
// transaction, but a token that somehow survives must still not
// authenticate, because ScopeAdmin implies every other scope.
func TestAdminTokenRejectedOnNonAdminAccount(t *testing.T) {
	st := newAuthStore(t)
	svc := NewService(st)
	addAuthUser(t, st, "u1")
	addAuthUser(t, st, "u2")
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAdmin(t.Context(), "u2", true); err != nil {
		t.Fatal(err)
	}

	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	tok := store.Token{
		ID:        "t1",
		UserID:    "u1",
		Name:      "laptop",
		SHA256:    HashSecret(secret),
		Scopes:    store.ScopeSet{store.ScopeAdmin},
		DeviceID:  "d1",
		CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateToken(t.Context(), secret); err != nil {
		t.Fatalf("admin token on an admin account: %v", err)
	}

	if err := st.SetUserAdmin(t.Context(), "u1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateToken(t.Context(), secret); err == nil {
		t.Fatal("admin-scoped token authenticated after its owner was demoted")
	}
}

// TestAdminScopeNotMintableWithoutTheRole covers the early, friendly
// check in CheckScopeGrant. The authoritative refusal lives in the
// store's CreateToken transaction; this one only produces the better
// error.
func TestAdminScopeNotMintableWithoutTheRole(t *testing.T) {
	st := newAuthStore(t)
	svc := NewService(st)
	addAuthUser(t, st, "u1")

	err := svc.CheckScopeGrant(t.Context(), "u1", store.ScopeSet{store.ScopeAdmin})
	if err == nil {
		t.Fatal("want a refusal for a non-admin account")
	}
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckScopeGrant(t.Context(), "u1", store.ScopeSet{store.ScopeAdmin}); err != nil {
		t.Fatalf("admin account: %v", err)
	}
}

// TestLoginRefusesADisabledAccount pins the one explicit check of
// ADR-0013 phase 6: every other way in resolves through a credential
// lookup that joins against users, but a login starts from UserByName,
// which must keep returning disabled accounts so the panel can render
// them.
func TestLoginRefusesADisabledAccount(t *testing.T) {
	st := newAuthStore(t)
	hash, err := HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(t.Context(), store.User{
		ID: "u1", Name: "casey", Argon2Hash: hash, Timezone: "UTC",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)

	if _, err := svc.Login(t.Context(), "casey", "hunter2hunter"); err != nil {
		t.Fatalf("enabled login: %v", err)
	}
	if err := st.SetUserDisabled(t.Context(), "u1", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(t.Context(), "casey", "hunter2hunter"); err == nil {
		t.Fatal("a disabled account signed in")
	} else if err.Error() != "invalid credentials" {
		// The refusal must not say more than a wrong password does, or
		// it becomes a way to enumerate which accounts were stopped.
		t.Fatalf("disabled login says %q", err)
	}
	if err := st.SetUserDisabled(t.Context(), "u1", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(t.Context(), "casey", "hunter2hunter"); err != nil {
		t.Fatalf("re-enabled login: %v", err)
	}
}
