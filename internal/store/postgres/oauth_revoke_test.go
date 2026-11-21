package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestRevokeAllDuringOAuthExchange(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-revoke-race")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name,
		RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource",
		Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateOAuthCode(ctx, code, storetest.MkOAuthSession(t, s, u.ID, "code", now)); err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_codes SET used_at = $1, grant_id = 'grant' WHERE sha256 = 'code'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_grants (id, user_id, client_id, client_name, scopes, resource, created_at)
		VALUES ('grant', $1, $2, $2, 'mcp:library', 'resource', $3)`, u.ID, client.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens (sha256, grant_id, kind, created_at, expires_at)
		VALUES ('access', 'grant', 'access', $1, $2)`, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- s.RevokeAllUserCredentials(ctx, u.ID, now) }()
	for {
		var blocked bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND query LIKE '%UPDATE oauth_codes SET used_at%'
			AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("revocation did not wait for the exchange: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(ctx, "access", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("grant from concurrent exchange remained usable: %v", err)
	}
}

func TestDeleteClientDuringOAuthExchange(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-client-delete")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation,
		ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge",
		Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateOAuthCode(ctx, code, storetest.MkOAuthSession(t, s, u.ID, "code", now)); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM oauth_clients WHERE id = 'client' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{SHA256: code.SHA256, ClientID: client.ID,
			RedirectURI: code.RedirectURI, CodeChallenge: code.CodeChallenge, Resource: code.Resource, GrantID: "grant"}, now)
		done <- err
	}()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("exchange did not wait for the client: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id = 'client'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("exchange after deletion: %v", err)
	}
}

func TestDeleteClientDuringCredentialCleanup(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	for _, operation := range []string{"housekeeping", "revoke all"} {
		t.Run(operation, func(t *testing.T) {
			s, err := Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			reset(t, s)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			u := storetest.MkUser(t, s, "oauth-client-cleanup")
			now := time.Now().UTC()
			client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
			if err := s.CreateOAuthClient(ctx, client); err != nil {
				t.Fatal(err)
			}
			client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
			code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation,
				ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge",
				Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
			session := storetest.MkOAuthSession(t, s, u.ID, "cleanup", now)
			if err := s.CreateOAuthCode(ctx, code, session); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{SHA256: code.SHA256, ClientID: client.ID,
				RedirectURI: code.RedirectURI, CodeChallenge: code.CodeChallenge, Resource: code.Resource, GrantID: "grant",
				Tokens: []store.OAuthToken{{SHA256: "access", Kind: store.OAuthTokenAccess, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}},
			}, now); err != nil {
				t.Fatal(err)
			}
			code.SHA256 = "pending"
			if err := s.CreateOAuthCode(ctx, code, session); err != nil {
				t.Fatal(err)
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var pid int
			if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id = 'client'`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if operation == "housekeeping" {
					done <- s.Housekeep(ctx, now.Add(48*time.Hour))
				} else {
					done <- s.RevokeAllUserCredentials(ctx, u.ID, now)
				}
			}()
			for {
				var blocked bool
				if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock'
					AND query = 'LOCK TABLE oauth_clients IN SHARE ROW EXCLUSIVE MODE' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("cleanup did not wait for client deletion: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, err := s.OAuthAccess(ctx, "access", now); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("deleted client's access token remained usable: %v", err)
			}
		})
	}
}

func TestHousekeepDuringRevokeAll(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-cleanup-overlap")
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at, expires_at)
		VALUES ('token', $1, 'device', 'token', '', 'hash', $2, $3)`, u.ID, now.Add(-90*24*time.Hour), now.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	storetest.MkOAuthSession(t, s, u.ID, "cleanup-overlap", now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM tokens WHERE id = 'token' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	revokeDone := make(chan error, 1)
	go func() { revokeDone <- s.RevokeAllUserCredentials(ctx, u.ID, now) }()
	var revokePID int
	for revokePID == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND query LIKE '%UPDATE tokens SET revoked_at%' AND $1 = ANY(pg_blocking_pids(pid)) LIMIT 1), 0)`, pid).Scan(&revokePID); err != nil {
			t.Fatal(err)
		}
		if revokePID != 0 {
			break
		}
		select {
		case err := <-revokeDone:
			t.Fatalf("revocation did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	housekeepDone := make(chan error, 1)
	go func() { housekeepDone <- s.Housekeep(ctx, now.Add(48*time.Hour)) }()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND query = 'LOCK TABLE oauth_clients IN SHARE ROW EXCLUSIVE MODE' AND $1 = ANY(pg_blocking_pids(pid)))`, revokePID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-housekeepDone:
			t.Fatalf("housekeeping did not wait for revocation: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-revokeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-housekeepDone; err != nil {
		t.Fatal(err)
	}
}

func TestHousekeepDuringOAuthRefreshRotation(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-housekeep-race")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	if err := s.CreateOAuthCode(ctx, store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}, storetest.MkOAuthSession(t, s, u.ID, "code", now)); err != nil {
		t.Fatal(err)
	}
	tokens := func(prefix string, at time.Time) []store.OAuthToken {
		return []store.OAuthToken{
			{SHA256: prefix + "-access", Kind: store.OAuthTokenAccess, CreatedAt: at, ExpiresAt: at.Add(time.Hour)},
			{SHA256: prefix + "-refresh", Kind: store.OAuthTokenRefresh, CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)},
		}
	}
	if _, err := s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{SHA256: "code", ClientID: client.ID, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", GrantID: "grant", Tokens: tokens("old", now)}, now); err != nil {
		t.Fatal(err)
	}
	first := now.Add(29 * 24 * time.Hour)
	if _, err := s.RotateOAuthRefresh(ctx, "old-refresh", client.ID, "resource", first, tokens("current", first)); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT sha256 FROM oauth_tokens WHERE sha256 = 'current-refresh' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	rotationTime := now.Add(58 * 24 * time.Hour)
	rotationDone := make(chan error, 1)
	go func() {
		_, err := s.RotateOAuthRefresh(ctx, "current-refresh", client.ID, "resource", rotationTime, tokens("next", rotationTime))
		rotationDone <- err
	}()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%UPDATE oauth_tokens SET used_at%' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-rotationDone:
			t.Fatalf("rotation did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	housekeepTime := now.Add(60 * 24 * time.Hour)
	housekeepDone := make(chan error, 1)
	go func() { housekeepDone <- s.Housekeep(ctx, housekeepTime) }()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%SELECT g.id FROM oauth_grants g%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-housekeepDone:
			t.Fatalf("housekeeping did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-rotationDone; err != nil {
		t.Fatal(err)
	}
	if err := <-housekeepDone; err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "old-refresh", client.ID, "resource", housekeepTime, tokens("replay", housekeepTime)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("replay after concurrent housekeeping: %v", err)
	}
	if _, err := s.RotateOAuthRefresh(ctx, "next-refresh", client.ID, "resource", housekeepTime, tokens("again", housekeepTime)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("grant remained active: %v", err)
	}
}

func TestOAuthRefreshReuseAfterGrantLockWait(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-refresh-lock")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	if err := s.CreateOAuthCode(ctx, store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}, storetest.MkOAuthSession(t, s, u.ID, "code", now)); err != nil {
		t.Fatal(err)
	}
	tokens := []store.OAuthToken{
		{SHA256: "access", Kind: store.OAuthTokenAccess, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{SHA256: "refresh", Kind: store.OAuthTokenRefresh, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	if _, err := s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{SHA256: "code", ClientID: client.ID, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", GrantID: "grant", Tokens: tokens}, now); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM oauth_grants WHERE id = 'grant' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.RotateOAuthRefresh(ctx, "refresh", client.ID, "resource", now, nil)
		done <- err
	}()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE OF g%' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("refresh did not wait for grant lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_tokens SET used_at = $1 WHERE sha256 = 'refresh'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = $1 WHERE id = 'grant'`, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrConflict) {
		t.Fatalf("replayed refresh after lock wait: %v", err)
	}
}

func TestOAuthConsentAgainstConcurrentRevocation(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-consent-race")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	code := store.OAuthCode{SHA256: "after", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	firstSession := storetest.MkOAuthSession(t, s, u.ID, "first", now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = $1 WHERE id = $2`, now, firstSession); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.CreateOAuthCode(ctx, code, firstSession) }()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%SELECT expires_at FROM auth_sessions%' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("consent did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("consent after revocation: %v", err)
	}

	secondSession := storetest.MkOAuthSession(t, s, u.ID, "second", now)
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM auth_sessions WHERE id = $1 FOR UPDATE`, secondSession); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_codes (sha256,user_id,client_id,client_name,redirect_uri,code_challenge,resource,scopes,created_at,expires_at) VALUES ('before',$1,$2,$2,$3,'challenge','resource','mcp:library',$4,$5)`, u.ID, client.ID, code.RedirectURI, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	done = make(chan error, 1)
	go func() { done <- s.RevokeAllUserCredentials(ctx, u.ID, now) }()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%UPDATE auth_sessions SET revoked_at%' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("revocation did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, err = s.ExchangeOAuthCode(ctx, store.OAuthCodeRedemption{SHA256: "before", ClientID: client.ID, RedirectURI: code.RedirectURI, CodeChallenge: code.CodeChallenge, Resource: code.Resource, GrantID: "grant"}, now)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("code before revocation: %v", err)
	}
}

func TestOAuthConsentSessionExpiresWaitingForClient(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-consent-expiry")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	sessionID := "expiring-session"
	expires := time.Now().UTC().Add(2 * time.Second)
	if err := s.CreateAuthSession(ctx, store.AuthSession{ID: sessionID, UserID: u.ID, SHA256: "session-hash", Kind: "web", CreatedAt: now, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM oauth_clients WHERE id = 'client' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	done := make(chan error, 1)
	go func() { done <- s.CreateOAuthCode(ctx, code, sessionID) }()
	for {
		var blocked bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%SELECT 1 FROM oauth_clients%' AND $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("consent did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if wait := time.Until(expires.Add(20 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired consent: %v", err)
	}
}
