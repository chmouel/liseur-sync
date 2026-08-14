package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// globalTokenLookup is the bearer path's lookup, which both backends
// implement outside the store interface (see auth.Service).
type globalTokenLookup interface {
	TokenByHashGlobal(ctx context.Context, sha256 string) (store.Token, error)
}

// testDisabledUser covers ADR-0013 phase 6. The server has several ways
// to arrive authenticated, and a check in all but one of them is a
// feature that does not exist — so the refusal lives in the credential
// lookups themselves, and this asserts it once per lookup, on both
// backends.
func testDisabledUser(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "casey")
	other := MkUser(t, s, "dana")
	now := time.Now().UTC().Truncate(time.Second)
	tokens, ok := s.(globalTokenLookup)
	if !ok {
		t.Fatal("this backend has no global token lookup")
	}

	// One credential of every kind.
	if err := s.CreateAuthSession(ctx, store.AuthSession{
		ID: "sess-1", UserID: u.ID, SHA256: "sess-sha", Kind: "web",
		CSRFHash: "csrf", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, store.Token{
		ID: "tok-1", UserID: u.ID, DeviceID: "d1", Name: "reader",
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
		ID: "kop-1", UserID: u.ID, TokenSHA256: "kop-sha", Label: "clara",
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
	// The same kinds for an account that stays enabled, so that a
	// clause matching too much shows up as a failure here.
	if err := s.CreateAuthSession(ctx, store.AuthSession{
		ID: "sess-2", UserID: other.ID, SHA256: "sess-sha-2", Kind: "web",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, store.Token{
		ID: "tok-2", UserID: other.ID, DeviceID: "d2", Name: "reader",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "tok-sha-2", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Every lookup works while the account is enabled.
	if _, err := s.AuthSessionByHash(ctx, "sess-sha"); err != nil {
		t.Fatalf("enabled session: %v", err)
	}
	if _, err := tokens.TokenByHashGlobal(ctx, "tok-sha"); err != nil {
		t.Fatalf("enabled token: %v", err)
	}
	if _, err := s.KosyncDeviceByKey(ctx, "kosync-sha"); err != nil {
		t.Fatalf("enabled kosync device: %v", err)
	}
	if _, err := s.KopluginDeviceByToken(ctx, "kop-sha"); err != nil {
		t.Fatalf("enabled koplugin device: %v", err)
	}

	if err := s.SetUserDisabled(ctx, u.ID, true, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled() || got.DisabledAt == nil {
		t.Fatalf("account is not disabled: %+v", got)
	}

	// Each credential now behaves as if it did not exist.
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"auth session", func() error { _, err := s.AuthSessionByHash(ctx, "sess-sha"); return err }},
		{"token", func() error { _, err := tokens.TokenByHashGlobal(ctx, "tok-sha"); return err }},
		{"kosync device", func() error { _, err := s.KosyncDeviceByKey(ctx, "kosync-sha"); return err }},
		{"koplugin device", func() error { _, err := s.KopluginDeviceByToken(ctx, "kop-sha"); return err }},
		{"pairing code", func() error { _, err := s.RedeemPairingCode(ctx, "pair-sha", now); return err }},
	} {
		if err := tc.call(); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s for a disabled account: want ErrNotFound, got %v", tc.name, err)
		}
	}

	// An enabled account is untouched by all of it.
	if _, err := s.AuthSessionByHash(ctx, "sess-sha-2"); err != nil {
		t.Fatalf("another account's session broke: %v", err)
	}
	if _, err := tokens.TokenByHashGlobal(ctx, "tok-sha-2"); err != nil {
		t.Fatalf("another account's token broke: %v", err)
	}

	// The account's own sessions were revoked in the same transaction
	// as the flag, so an open tab stops working now rather than at
	// cookie expiry — and stays stopped after re-enabling.
	if err := s.SetUserDisabled(ctx, u.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthSessionByHash(ctx, "sess-sha"); err != nil {
		// The row is reachable again; it is the revocation that must
		// have survived, which the session's own timestamp shows.
		t.Fatalf("re-enabled account: session lookup %v", err)
	}
	sess, err := s.AuthSessionByHash(ctx, "sess-sha")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RevokedAt == nil {
		t.Fatal("disabling did not revoke the account's sessions")
	}
	// Everything else resumes, because none of it was revoked.
	if _, err := tokens.TokenByHashGlobal(ctx, "tok-sha"); err != nil {
		t.Fatalf("re-enabled token: %v", err)
	}
	if _, err := s.KosyncDeviceByKey(ctx, "kosync-sha"); err != nil {
		t.Fatalf("re-enabled kosync device: %v", err)
	}
	if _, err := s.KopluginDeviceByToken(ctx, "kop-sha"); err != nil {
		t.Fatalf("re-enabled koplugin device: %v", err)
	}
	if _, err := s.RedeemPairingCode(ctx, "pair-sha", now); err != nil {
		t.Fatalf("re-enabled pairing code: %v", err)
	}

	// The instance cannot be left with nobody who can turn anything
	// back on.
	if err := s.SetUserAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserDisabled(ctx, u.ID, true, now); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("disabling the last admin: want ErrLastAdmin, got %v", err)
	}
	if err := s.SetUserAdmin(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserDisabled(ctx, u.ID, true, now); err != nil {
		t.Fatalf("disabling an admin with another one left: %v", err)
	}

	// Missing accounts, and a no-op that is not an error.
	if err := s.SetUserDisabled(ctx, "nobody", true, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabling a missing account: want ErrNotFound, got %v", err)
	}
	if err := s.SetUserDisabled(ctx, u.ID, true, now); err != nil {
		t.Fatalf("disabling twice: %v", err)
	}
}
