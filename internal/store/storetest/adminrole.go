package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testAdminRole covers ADR-0013's account-property model: the flag
// moves only through SetUserAdmin, demotion takes the account's
// admin-scoped tokens with it, and the instance cannot be left with no
// administrator.
func testAdminRole(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	bob := MkUser(t, s, "bob")

	if got, _ := s.UserByID(ctx, alice.ID); got.IsAdmin || !got.Enabled() {
		t.Fatalf("new user should be a non-admin, enabled account: %+v", got)
	}

	// The admin scope is refused to an account that is not an admin,
	// and refused inside the write rather than by a caller's check.
	adminTok := store.Token{
		ID: "t-admin", UserID: alice.ID, DeviceID: "d1", Name: "cli",
		Scopes: store.ScopeSet{store.ScopeAdmin}, SHA256: "h-admin",
		CreatedAt: time.Now(),
	}
	if err := s.CreateToken(ctx, adminTok); !errors.Is(err, store.ErrAdminGrantRequiresAdmin) {
		t.Fatalf("admin token for a non-admin: want ErrAdminGrantRequiresAdmin, got %v", err)
	}

	if err := s.SetUserAdmin(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserByID(ctx, alice.ID); !got.IsAdmin {
		t.Fatal("alice should be an admin")
	}
	// Idempotent, and it does not trip the last-admin guard.
	if err := s.SetUserAdmin(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserAdmin(ctx, "nobody", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}

	if err := s.CreateToken(ctx, adminTok); err != nil {
		t.Fatalf("admin token for an admin: %v", err)
	}
	// A token that only gains the admin scope later is checked too.
	plain := store.Token{
		ID: "t-plain", UserID: bob.ID, DeviceID: "d2", Name: "boox",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "h-plain",
		CreatedAt: time.Now(),
	}
	if err := s.CreateToken(ctx, plain); err != nil {
		t.Fatal(err)
	}
	err := s.UpdateTokenScopes(ctx, bob.ID, plain.ID, store.ScopeSet{store.ScopeAdmin})
	if !errors.Is(err, store.ErrAdminGrantRequiresAdmin) {
		t.Fatalf("scope update to admin for a non-admin: want ErrAdminGrantRequiresAdmin, got %v", err)
	}

	// The last enabled admin cannot be demoted.
	if err := s.SetUserAdmin(ctx, alice.ID, false); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demoting the last admin: want ErrLastAdmin, got %v", err)
	}
	if got, _ := s.UserByID(ctx, alice.ID); !got.IsAdmin {
		t.Fatal("a refused demotion must leave the flag in place")
	}

	// With a second admin, demotion works and takes the admin-scoped
	// token with it — ScopeAdmin implies every other scope, so a
	// surviving token would keep the authority the role just lost.
	if err := s.SetUserAdmin(ctx, bob.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserAdmin(ctx, alice.ID, false); err != nil {
		t.Fatal(err)
	}
	toks, err := s.ListTokens(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		if tok.ID == adminTok.ID && tok.RevokedAt == nil {
			t.Fatal("demotion left an unrevoked admin-scoped token")
		}
	}
}

// testConcurrentAdminDemotion is the race the account-property model
// exists to close: two demotions of the two remaining administrators,
// each of which would see the other still in place if the guard were a
// read followed by a write.
func testConcurrentAdminDemotion(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	bob := MkUser(t, s, "bob")
	for _, id := range []string{alice.ID, bob.ID} {
		if err := s.SetUserAdmin(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i, id := range []string{alice.ID, bob.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.SetUserAdmin(ctx, id, false)
		}()
	}
	close(start)
	wg.Wait()

	var refused int
	for _, err := range errs {
		if errors.Is(err, store.ErrLastAdmin) {
			refused++
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if refused != 1 {
		t.Fatalf("want exactly one refusal, got %d", refused)
	}
	var admins int
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.IsAdmin {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("want exactly one admin left, got %d", admins)
	}
}

// testConcurrentAdminTokenMint runs a mint against a demotion: whichever
// order they land in, no usable admin-scoped token may belong to a
// non-admin account afterwards.
func testConcurrentAdminTokenMint(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "alice")
	keeper := MkUser(t, s, "keeper")
	for _, id := range []string{alice.ID, keeper.ID} {
		if err := s.SetUserAdmin(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var mintErr, demoteErr error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		mintErr = s.CreateToken(ctx, store.Token{
			ID: "t-race", UserID: alice.ID, DeviceID: "d1", Name: "race",
			Scopes: store.ScopeSet{store.ScopeAdmin}, SHA256: "h-race",
			CreatedAt: time.Now(),
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		demoteErr = s.SetUserAdmin(ctx, alice.ID, false)
	}()
	close(start)
	wg.Wait()

	if demoteErr != nil {
		t.Fatalf("demotion: %v", demoteErr)
	}
	if mintErr != nil && !errors.Is(mintErr, store.ErrAdminGrantRequiresAdmin) {
		t.Fatalf("mint: unexpected error %v", mintErr)
	}
	u, err := s.UserByID(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Fatal("alice should have been demoted")
	}
	toks, err := s.ListTokens(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		if tok.Scopes.Contains(store.ScopeAdmin) && tok.RevokedAt == nil {
			t.Fatalf("a non-admin account kept a live admin-scoped token: %+v", tok)
		}
	}
}
