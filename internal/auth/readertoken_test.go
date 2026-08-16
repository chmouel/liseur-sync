package auth

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// readerTokens returns the user's reader tokens, newest first is not
// promised — callers here only count and inspect.
func readerTokens(t *testing.T, st store.Store, userID string) []store.Token {
	t.Helper()
	all, err := st.ListTokens(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Token
	for _, tok := range all {
		if tok.Name == ReaderTokenName {
			out = append(out, tok)
		}
	}
	return out
}

// TestReaderTokensDoNotPileUp is the point of the reap. The reader asks
// for a credential on every open and again every hour, so without this
// a person who reads gets a new row an hour, forever, each one shown to
// them as though it were a device they had registered.
func TestReaderTokensDoNotPileUp(t *testing.T) {
	st := newAuthStore(t)
	addAuthUser(t, st, "u1")
	svc := NewService(st)

	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }

	for range 5 {
		if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
			t.Fatal(err)
		}
		// Far enough for the previous one to have died of old age.
		now = now.Add(2 * ReaderTokenTTL)
	}

	got := readerTokens(t, st, "u1")
	if len(got) != 1 {
		t.Fatalf("five reader page opens left %d tokens, want 1", len(got))
	}
}

// TestReaderTokenReapSparesTheLiving keeps the two-tab loop impossible.
// A tab that is still reading holds a credential the server cannot
// re-issue — the secret exists only once — so taking it away would log
// that tab out, and it would mint again and take this one away in turn.
func TestReaderTokenReapSparesTheLiving(t *testing.T) {
	st := newAuthStore(t)
	addAuthUser(t, st, "u1")
	svc := NewService(st)

	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }

	_, first, err := svc.MintReaderToken(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
		t.Fatal(err)
	}

	got := readerTokens(t, st, "u1")
	if len(got) != 2 {
		t.Fatalf("a second tab reaped a live credential: %d tokens, want 2", len(got))
	}
	for _, tok := range got {
		if tok.ID == first.ID && tok.RevokedAt != nil {
			t.Fatal("the first tab's credential was revoked out from under it")
		}
	}
}

// TestReaderTokenKeepsTheDeviceAcrossAReap protects the op log. Heads
// are per work *and* device, so a browser that comes back after a night
// closed has to still be the same device — otherwise one person reading
// one book becomes two competing heads and "where did I stop" depends
// on which window asked.
func TestReaderTokenKeepsTheDeviceAcrossAReap(t *testing.T) {
	st := newAuthStore(t)
	addAuthUser(t, st, "u1")
	svc := NewService(st)

	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }

	_, first, err := svc.MintReaderToken(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(12 * time.Hour)
	_, second, err := svc.MintReaderToken(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceID != first.DeviceID {
		t.Fatalf("device id changed across a reap: %q then %q",
			first.DeviceID, second.DeviceID)
	}
}

// TestReaderTokenReapLeavesOtherTokensAlone: the reap is for credentials
// the server issued to itself. A token someone made for a Kobo is not
// one of those, even after it expires.
func TestReaderTokenReapLeavesOtherTokensAlone(t *testing.T) {
	st := newAuthStore(t)
	addAuthUser(t, st, "u1")
	svc := NewService(st)

	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }

	dead := now.Add(-time.Hour)
	if _, _, err := svc.MintToken(t.Context(), "u1", "Boox Palma",
		store.ScopeSet{store.ScopeSync}, &dead); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * ReaderTokenTTL)
	if _, _, err := svc.MintReaderToken(t.Context(), "u1"); err != nil {
		t.Fatal(err)
	}

	all, err := st.ListTokens(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	var kept bool
	for _, tok := range all {
		if tok.Name == "Boox Palma" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the reap deleted an expired token a person had made")
	}
}
