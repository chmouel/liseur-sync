package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const (
	testOAuthRedirect  = "https://client.example/cb"
	testOAuthResource  = "https://sync.example/mcp"
	testOAuthChallenge = "challenge"
)

// testOAuth covers the assistant endpoint's credentials (ADR-0069): a
// code is spent once and a replay revokes what it produced, a refresh
// token rotates and its reuse revokes the grant, and nothing survives a
// disabled account, a revoked grant or a deleted client.
func testOAuth(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "oauth")
	other := MkUser(t, s, "oauth-other")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)

	client := store.OAuthClient{
		ID: "https://client.example/meta.json", Kind: store.OAuthClientMetadata,
		Name: "Example", CreatedAt: now,
	}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOAuthClient(ctx, client); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate client: %v", err)
	}
	registered := store.OAuthClient{
		ID: "lsc_custom", Kind: store.OAuthClientRegistered, Name: "Custom",
		RedirectURIs: []string{"https://a.example/cb", "http://127.0.0.1/cb"},
		SecretSHA256: "secret-hash", CreatedAt: now.Add(time.Second),
	}
	if err := s.CreateOAuthClient(ctx, registered); err != nil {
		t.Fatal(err)
	}
	got, err := s.OAuthClient(ctx, "lsc_custom")
	if err != nil || len(got.RedirectURIs) != 2 || got.RedirectURIs[1] != "http://127.0.0.1/cb" ||
		got.SecretSHA256 != "secret-hash" {
		t.Fatalf("registered client round trip: %+v %v", got, err)
	}
	if list, err := s.ListOAuthClients(ctx); err != nil || len(list) != 2 {
		t.Fatalf("clients: %+v %v", list, err)
	}
	if _, err := s.OAuthClient(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown client: %v", err)
	}

	code := func(sha, user string) {
		t.Helper()
		if err := s.CreateOAuthCode(ctx, store.OAuthCode{
			SHA256: sha, UserID: user, ClientID: client.ID, ClientName: "Example",
			RedirectURI: testOAuthRedirect, CodeChallenge: testOAuthChallenge,
			Resource: testOAuthResource, Scopes: []string{"mcp:library", "mcp:text"},
			CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	tokens := func(prefix string, at time.Time) []store.OAuthToken {
		return []store.OAuthToken{
			{SHA256: prefix + "-access", Kind: store.OAuthTokenAccess, CreatedAt: at, ExpiresAt: at.Add(time.Hour)},
			{SHA256: prefix + "-refresh", Kind: store.OAuthTokenRefresh, CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)},
		}
	}
	// exchange is what the token endpoint does: one call that matches
	// the request against the code, spends the code and stores the
	// grant it yields.
	exchange := func(sha, grantID, prefix string, at time.Time) (store.OAuthGrant, error) {
		return s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{
			SHA256: sha, ClientID: client.ID, RedirectURI: testOAuthRedirect,
			CodeChallenge: testOAuthChallenge, Resource: testOAuthResource,
			GrantID: grantID, Tokens: tokens(prefix, at),
		}, at)
	}

	code("code-1", u.ID)
	g, err := exchange("code-1", "grant-1", "g1", now)
	if err != nil || g.UserID != u.ID || g.ID != "grant-1" || g.ClientID != client.ID ||
		g.ClientName != "Example" || g.Resource != testOAuthResource || len(g.Scopes) != 2 {
		t.Fatalf("exchange: %+v %v", g, err)
	}
	a, err := s.OAuthAccess(ctx, "g1-access", now.Add(time.Minute))
	if err != nil || a.UserID != u.ID || a.GrantID != "grant-1" || a.Resource != testOAuthResource ||
		len(a.Scopes) != 2 {
		t.Fatalf("access: %+v %v", a, err)
	}
	if _, err := s.OAuthAccess(ctx, "g1-refresh", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refresh token must not authenticate: %v", err)
	}
	if _, err := s.OAuthAccess(ctx, "g1-access", now.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired access token: %v", err)
	}

	// Replaying the code revokes the grant it produced.
	if _, err := exchange("code-1", "grant-1b", "g1b", now.Add(20*time.Second)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("code replay: %v", err)
	}
	if _, err := s.OAuthAccess(ctx, "g1-access", now.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a replayed code must revoke its grant: %v", err)
	}

	code("code-expired", u.ID)
	if _, err := exchange("code-expired", "grant-expired", "ge", now.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired code: %v", err)
	}
	if _, err := exchange("code-unknown", "grant-unknown", "gu", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown code: %v", err)
	}

	// Refresh rotation.
	code("code-2", u.ID)
	if _, err := exchange("code-2", "grant-2", "g2", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "g2-refresh", "lsc_custom", now, tokens("g2b", now)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refresh by another client: %v", err)
	}
	later := now.Add(2 * time.Hour)
	g, err = s.RotateOAuthRefresh(ctx, "g2-refresh", client.ID, later, tokens("g2b", later))
	if err != nil || g.ID != "grant-2" || g.UserID != u.ID || len(g.Scopes) != 2 {
		t.Fatalf("rotate: %+v %v", g, err)
	}
	if _, err := s.OAuthAccess(ctx, "g2b-access", later); err != nil {
		t.Fatalf("rotated access token: %v", err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "g2-access", client.ID, later, tokens("x", later)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an access token must not refresh: %v", err)
	}
	// Reusing the spent refresh token revokes the whole grant.
	if _, err := s.RotateOAuthRefresh(ctx, "g2-refresh", client.ID, later, tokens("g2c", later)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("refresh reuse: %v", err)
	}
	if _, err := s.OAuthAccess(ctx, "g2b-access", later); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refresh reuse must revoke the grant: %v", err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "g2b-refresh", client.ID, later, tokens("g2d", later)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a revoked grant must not refresh: %v", err)
	}

	// Listing, revocation and touch are bounded to the account.
	code("code-3", u.ID)
	if _, err := exchange("code-3", "grant-3", "g3", now); err != nil {
		t.Fatal(err)
	}
	code("code-o", other.ID)
	if _, err := exchange("code-o", "grant-o", "go", now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeOAuthGrant(ctx, other.ID, "grant-3", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoking another account's grant: %v", err)
	}
	mine, err := s.ListOAuthGrants(ctx, u.ID)
	if err != nil || len(mine) != 3 {
		t.Fatalf("own grants: %+v %v", mine, err)
	}
	for _, g := range mine {
		if g.UserID != u.ID || g.UserName != u.Name {
			t.Fatalf("listed a foreign grant: %+v", g)
		}
	}
	if all, err := s.ListOAuthGrants(ctx, ""); err != nil || len(all) != 4 {
		t.Fatalf("all grants: %d %v", len(all), err)
	}
	if err := s.TouchOAuthGrant(ctx, "grant-3", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchOAuthGrant(ctx, "grant-3", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, g := range mustGrants(t, s, u.ID) {
		if g.ID == "grant-3" && (g.LastUsedAt == nil || !g.LastUsedAt.Equal(now.Add(time.Minute))) {
			t.Fatalf("touch is throttled: %+v", g.LastUsedAt)
		}
	}

	// A disabled account's token stops at once; enabling it again
	// restores a grant nobody revoked.
	if err := s.SetUserDisabled(ctx, other.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(ctx, "go-access", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled account: %v", err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "go-refresh", client.ID, now, tokens("go2", now)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled account refresh: %v", err)
	}

	// Revoking every credential includes OAuth grants.
	if err := s.RevokeAllUserCredentials(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(ctx, "g3-access", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke-all left an OAuth token live: %v", err)
	}

	// Housekeeping purges expired tokens, then the grants left empty.
	if err := s.Housekeep(ctx, now.Add(store.TokenPurgeGrace+31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if all, err := s.ListOAuthGrants(ctx, ""); err != nil || len(all) != 0 {
		t.Fatalf("housekeeping left grants: %+v %v", all, err)
	}

	// Deleting a client removes everything issued to it.
	code("code-4", u.ID)
	if _, err := exchange("code-4", "grant-4", "g4", now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOAuthClient(ctx, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(ctx, "g4-access", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted client's token: %v", err)
	}
	if err := s.DeleteOAuthClient(ctx, client.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
}

// testOAuthRevokeAllSpendsCodes proves that revoking every credential
// spends an authorization code that was approved but never redeemed. A
// code left alive is a grant in waiting: redeeming it after the
// operator was told everything was gone mints a fresh, unrevoked grant.
func testOAuthRevokeAllSpendsCodes(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "oauth-spend")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	client := mkOAuthTestClient(t, s, now)
	mkOAuthTestCode(t, s, client, "code-pending", u.ID, now)

	if err := s.RevokeAllUserCredentials(ctx, u.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if g, err := testExchangeCode(ctx, s, client, "code-pending", "grant-pending", "pending", now.Add(2*time.Second)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a code left over revoke-all was redeemed: %+v %v", g, err)
	}
	if grants, err := s.ListOAuthGrants(ctx, u.ID); err != nil || len(grants) != 0 {
		t.Fatalf("redeeming a spent code left a grant behind: %+v %v", grants, err)
	}
	if _, err := s.OAuthAccess(ctx, "pending-access", now.Add(2*time.Second)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a code left over revoke-all minted a live token")
	}
}

// testOAuthCodeExchangeIsOneTransaction proves that spending a code and
// storing the grant it yields cannot be split. A request that does not
// match the code cannot spend it; a replay either finds the committed
// grant and revokes it, or loses the conditional update and revokes the
// grant the winner committed. Neither may leave a live grant behind.
func testOAuthCodeExchangeIsOneTransaction(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "oauth-exchange")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	client := mkOAuthTestClient(t, s, now)

	// A request that differs from the code it presents spends nothing,
	// so the code still works for the client it was issued to.
	mkOAuthTestCode(t, s, client, "code-mismatch", u.ID, now)
	mismatches := map[string]func(*store.OAuthCodeRedemption){
		"client":    func(r *store.OAuthCodeRedemption) { r.ClientID = "lsc_somebody_else" },
		"redirect":  func(r *store.OAuthCodeRedemption) { r.RedirectURI = "https://client.example/other" },
		"challenge": func(r *store.OAuthCodeRedemption) { r.CodeChallenge = "not-the-challenge" },
		"resource":  func(r *store.OAuthCodeRedemption) { r.Resource = "https://other.example/mcp" },
	}
	for name, mutate := range mismatches {
		r := testRedemption(client, "code-mismatch", "grant-"+name, name, now)
		mutate(&r)
		if g, err := s.ExchangeOAuthCode(ctx, r, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s mismatch redeemed a code: %+v %v", name, g, err)
		}
		if _, err := s.OAuthAccess(ctx, name+"-access", now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s mismatch minted a live token", name)
		}
	}
	if g, err := testExchangeCode(ctx, s, client, "code-mismatch", "grant-mismatch", "ok", now); err != nil || g.ID != "grant-mismatch" {
		t.Fatalf("a mismatched request spent the code: %+v %v", g, err)
	}
	mkOAuthTestCode(t, s, client, "code-no-redirect", u.ID, now)
	withoutRedirect := testRedemption(client, "code-no-redirect", "grant-no-redirect", "no-redirect", now)
	withoutRedirect.RedirectURI = ""
	if g, err := s.ExchangeOAuthCode(ctx, withoutRedirect, now); err != nil || g.ID != "grant-no-redirect" {
		t.Fatalf("exchange without redirect URI: %+v %v", g, err)
	}

	// A replay after a completed exchange revokes the grant.
	mkOAuthTestCode(t, s, client, "code-replay", u.ID, now)
	if _, err := testExchangeCode(ctx, s, client, "code-replay", "grant-replay", "r1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(ctx, "r1-access", now); err != nil {
		t.Fatalf("grant from a code: %v", err)
	}
	for name, mutate := range mismatches {
		r := testRedemption(client, "code-replay", "grant-replay-"+name, "replay-"+name, now)
		mutate(&r)
		if _, err := s.ExchangeOAuthCode(ctx, r, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s mismatch replay: %v", name, err)
		}
		if _, err := s.OAuthAccess(ctx, "r1-access", now); err != nil {
			t.Fatalf("%s mismatch revoked the grant: %v", name, err)
		}
	}
	if _, err := testExchangeCode(ctx, s, client, "code-replay", "grant-replay-2", "r2", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("code replay: %v", err)
	}
	if _, err := s.OAuthAccess(ctx, "r1-access", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a replayed code left its grant live")
	}

	// A code spent with no grant recorded against it — the state a
	// replay used to find in the gap between the two transactions —
	// redeems to nothing. Revoking every credential spends codes.
	u2 := MkUser(t, s, "oauth-exchange-spent")
	mkOAuthTestCode(t, s, client, "code-spent", u2.ID, now)
	if err := s.RevokeAllUserCredentials(ctx, u2.ID, now); err != nil {
		t.Fatal(err)
	}
	if g, err := testExchangeCode(ctx, s, client, "code-spent", "grant-spent", "spent", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a spent code with no grant still redeemed: %+v %v", g, err)
	}

	// Concurrent redemptions of one code: one wins, and losing revokes
	// the grant the winner may already have committed.
	u3 := MkUser(t, s, "oauth-exchange-race")
	mkOAuthTestCode(t, s, client, "code-race", u3.ID, now)
	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losses int
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("race%d", i)
			g, err := testExchangeCode(ctx, s, client, "code-race", "grant-"+name, name, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && g.UserID == u3.ID:
				wins++
			case errors.Is(err, store.ErrConflict):
				losses++
			default:
				t.Errorf("racing redemption %d: %+v %v", i, g, err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || losses != racers-1 {
		t.Fatalf("racing redemptions: %d won, %d lost", wins, losses)
	}
	var raced []store.OAuthGrant
	for _, g := range mustGrants(t, s, u3.ID) {
		if strings.HasPrefix(g.ID, "grant-race") {
			raced = append(raced, g)
		}
	}
	if len(raced) != 1 || raced[0].RevokedAt == nil {
		t.Fatalf("a racing redemption left a live grant: %+v", raced)
	}
	for i := range racers {
		if _, err := s.OAuthAccess(ctx, fmt.Sprintf("race%d-access", i), now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("racing redemption %d left a live token", i)
		}
	}
}

// testOAuthRefreshReuseDuringRotation proves that a refresh token
// presented twice revokes the grant even when the second presentation
// loses the race for the rotation: the winner has already stored fresh
// tokens for the grant, and reuse means the token leaked.
func testOAuthRefreshReuseDuringRotation(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "oauth-rotate")
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	client := mkOAuthTestClient(t, s, now)
	mkOAuthTestCode(t, s, client, "code-rotate", u.ID, now)
	if _, err := testExchangeCode(ctx, s, client, "code-rotate", "grant-rotate", "rot", now); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losses int
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens := []store.OAuthToken{
				{SHA256: fmt.Sprintf("win%d-access", i), Kind: store.OAuthTokenAccess, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
				{SHA256: fmt.Sprintf("win%d-refresh", i), Kind: store.OAuthTokenRefresh, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
			}
			g, err := s.RotateOAuthRefresh(ctx, "rot-refresh", client.ID, now, tokens)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && g.ID == "grant-rotate":
				wins++
			case errors.Is(err, store.ErrConflict):
				losses++
			default:
				t.Errorf("racing rotation %d: %+v %v", i, g, err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || losses != racers-1 {
		t.Fatalf("racing rotations: %d won, %d lost", wins, losses)
	}
	grants := mustGrants(t, s, u.ID)
	if len(grants) != 1 || grants[0].RevokedAt == nil {
		t.Fatalf("reuse during rotation left the grant live: %+v", grants)
	}
	for i := range racers {
		if _, err := s.OAuthAccess(ctx, fmt.Sprintf("win%d-access", i), now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("rotation %d left a live token", i)
		}
	}
}

func mkOAuthTestClient(t *testing.T, s store.Store, now time.Time) store.OAuthClient {
	t.Helper()
	c := store.OAuthClient{
		ID: "https://client.example/meta.json", Kind: store.OAuthClientMetadata,
		Name: "Example", CreatedAt: now,
	}
	if err := s.CreateOAuthClient(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func mkOAuthTestCode(t *testing.T, s store.Store, client store.OAuthClient, sha, user string, now time.Time) {
	t.Helper()
	if err := s.CreateOAuthCode(context.Background(), store.OAuthCode{
		SHA256: sha, UserID: user, ClientID: client.ID, ClientName: client.Name,
		RedirectURI: testOAuthRedirect, CodeChallenge: testOAuthChallenge,
		Resource: testOAuthResource, Scopes: []string{"mcp:library", "mcp:text"},
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
}

func testRedemption(client store.OAuthClient, sha, grantID, prefix string, at time.Time) store.OAuthCodeRedemption {
	return store.OAuthCodeRedemption{
		SHA256: sha, ClientID: client.ID, RedirectURI: testOAuthRedirect,
		CodeChallenge: testOAuthChallenge, Resource: testOAuthResource,
		GrantID: grantID,
		Tokens: []store.OAuthToken{
			{SHA256: prefix + "-access", Kind: store.OAuthTokenAccess, CreatedAt: at, ExpiresAt: at.Add(time.Hour)},
			{SHA256: prefix + "-refresh", Kind: store.OAuthTokenRefresh, CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)},
		},
	}
}

func testExchangeCode(ctx context.Context, s store.Store, client store.OAuthClient, sha, grantID, prefix string, at time.Time) (store.OAuthGrant, error) {
	return s.ExchangeOAuthCode(ctx, testRedemption(client, sha, grantID, prefix, at), at)
}

func mustGrants(t *testing.T, s store.Store, userID string) []store.OAuthGrant {
	t.Helper()
	gs, err := s.ListOAuthGrants(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return gs
}
