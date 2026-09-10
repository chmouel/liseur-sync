package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testUserCredentialOperations covers the two admin operations that
// have to be all-or-nothing (ADR-0013): a password change that also
// revokes sessions, and the "revoke everything" that has to include the
// pairing code nobody thinks of.
func testUserCredentialOperations(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "creds")
	other := MkUser(t, s, "creds-other")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)

	mkSession := func(id, kind string) store.AuthSession {
		a := store.AuthSession{
			ID: id, UserID: u.ID, SHA256: "sha-" + id, Kind: kind,
			CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		}
		if err := s.CreateAuthSession(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	kept := mkSession("sess-kept", "web")
	mkSession("sess-gone", "web")
	mkSession("sess-login", "login")
	// Another account's session is never touched by either operation.
	if err := s.CreateAuthSession(ctx, store.AuthSession{
		ID: "sess-other", UserID: other.ID, SHA256: "sha-other", Kind: "web",
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetUserPassword(ctx, u.ID, "new-hash", kept.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserByID(ctx, u.ID); got.Argon2Hash != "new-hash" {
		t.Fatalf("password not written: %q", got.Argon2Hash)
	}
	if a, err := s.AuthSessionByHash(ctx, "sha-sess-kept"); err != nil || a.RevokedAt != nil {
		t.Fatalf("the caller's own session was revoked: %+v %v", a, err)
	}
	for _, sha := range []string{"sha-sess-gone", "sha-sess-login"} {
		a, err := s.AuthSessionByHash(ctx, sha)
		if err != nil || a.RevokedAt == nil {
			t.Fatalf("%s should be revoked: %+v %v", sha, a, err)
		}
	}
	if a, err := s.AuthSessionByHash(ctx, "sha-other"); err != nil || a.RevokedAt != nil {
		t.Fatalf("another account's session was revoked: %+v %v", a, err)
	}
	if err := s.SetUserPassword(ctx, "nobody", "h", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}

	// Now every other kind of credential, and the sweep that takes them.
	if err := s.CreateToken(ctx, store.Token{
		ID: "tok", UserID: u.ID, DeviceID: "d", Name: "boox",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "tok-sha", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateKosyncDevice(ctx, store.KosyncDevice{
		UserID: u.ID, DeviceSlot: "slot-1", KeySHA256: "kosync-sha", Label: "kobo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateKopluginDevice(ctx, store.KopluginDevice{
		ID: "kop-1", UserID: u.ID, TokenSHA256: "kop-sha", Label: "kobo",
		DeviceID: "dev-1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePairingCode(ctx, store.PairingCode{
		ID: "pair-1", UserID: u.ID, CodeSHA256: "pair-sha",
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.RevokeAllUserCredentials(ctx, u.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if toks, err := s.ListTokens(ctx, u.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, tok := range toks {
			if tok.RevokedAt == nil {
				t.Fatalf("token survived: %+v", tok)
			}
		}
	}
	if devs, err := s.ListKosyncDevices(ctx, u.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, d := range devs {
			if d.RevokedAt == nil {
				t.Fatalf("kosync slot survived: %+v", d)
			}
		}
	}
	if devs, err := s.ListKopluginDevices(ctx, u.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, d := range devs {
			if d.RevokedAt == nil {
				t.Fatalf("koplugin capability survived: %+v", d)
			}
		}
	}
	if a, err := s.AuthSessionByHash(ctx, "sha-sess-kept"); err != nil || a.RevokedAt == nil {
		t.Fatalf("the spared session survived a full revocation: %+v %v", a, err)
	}
	// The pairing code is the one an operator forgets: unspent, it mints
	// a fresh kosync slot minutes after being told everything was gone.
	if _, err := s.RedeemPairingCode(ctx, "pair-sha", now.Add(2*time.Hour)); err == nil {
		t.Fatal("an unredeemed pairing code survived a full revocation")
	}
	if a, err := s.AuthSessionByHash(ctx, "sha-other"); err != nil || a.RevokedAt != nil {
		t.Fatalf("another account's session was revoked: %+v %v", a, err)
	}
	if err := s.RevokeAllUserCredentials(ctx, "nobody", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}
}

// testListUsersPage covers the cursor the admin user list pages with.
func testListUsersPage(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	for _, name := range []string{"anna", "bruno", "cleo", "dara"} {
		MkUser(t, s, name)
	}

	first, err := s.ListUsersPage(ctx, "", 2)
	if err != nil || len(first) != 2 ||
		first[0].Name != "anna" || first[1].Name != "bruno" {
		t.Fatalf("first page: %+v %v", names(first), err)
	}
	second, err := s.ListUsersPage(ctx, first[1].Name, 3)
	if err != nil || len(second) != 2 ||
		second[0].Name != "cleo" || second[1].Name != "dara" {
		t.Fatalf("second page: %+v %v", names(second), err)
	}
	last, err := s.ListUsersPage(ctx, "dara", 2)
	if err != nil || len(last) != 0 {
		t.Fatalf("past the end: %+v %v", names(last), err)
	}
	if got, err := s.ListUsersPage(ctx, "", 0); err != nil || len(got) != 0 {
		t.Fatalf("zero limit: %+v %v", names(got), err)
	}
}

func names(us []store.User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

// testExtendAuthSession covers the sliding web session: a session in
// use moves its own expiry out, and the two states that must never be
// revived by a request that arrives at the wrong moment.
func testExtendAuthSession(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "sliding")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)

	mk := func(id string, expires time.Time) {
		if err := s.CreateAuthSession(ctx, store.AuthSession{
			ID: id, UserID: u.ID, SHA256: "sha-" + id, Kind: "web",
			CreatedAt: now, ExpiresAt: expires,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("live", now.Add(24*time.Hour))
	mk("revoked", now.Add(24*time.Hour))
	mk("lapsed", now.Add(-time.Hour))

	want := now.Add(180 * 24 * time.Hour)
	if err := s.ExtendAuthSession(ctx, u.ID, "live", want, want, now); err != nil {
		t.Fatal(err)
	}
	a, err := s.AuthSessionByHash(ctx, "sha-live")
	if err != nil || !a.ExpiresAt.Equal(want) {
		t.Fatalf("expiry not moved: %v %v", a.ExpiresAt, err)
	}
	// Backwards is a no-op, so lowering the configured window cannot
	// silently shorten a session that was issued under a longer one.
	if err := s.ExtendAuthSession(ctx, u.ID, "live", now.Add(time.Hour), now.Add(time.Hour), now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("backwards: want ErrNotFound, got %v", err)
	}
	if a, err := s.AuthSessionByHash(ctx, "sha-live"); err != nil || !a.ExpiresAt.Equal(want) {
		t.Fatalf("expiry moved backwards: %v %v", a.ExpiresAt, err)
	}
	// The throttle is part of the conditional update, so concurrent
	// requests that observed the old expiry cannot all extend it.
	if err := s.ExtendAuthSession(ctx, u.ID, "live", want.Add(24*time.Hour), want, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("throttled renewal: want ErrNotFound, got %v", err)
	}

	if err := s.RevokeAuthSession(ctx, u.ID, "revoked"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"revoked", "lapsed", "nosuch"} {
		if err := s.ExtendAuthSession(ctx, u.ID, id, want, want, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: want ErrNotFound, got %v", id, err)
		}
	}
	// Another account cannot extend a session it does not own.
	other := MkUser(t, s, "sliding-other")
	if err := s.ExtendAuthSession(ctx, other.ID, "live", want.Add(time.Hour), want.Add(time.Hour), now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-account: want ErrNotFound, got %v", err)
	}
}
