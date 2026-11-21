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
	code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientName: client.Name,
		RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource",
		Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateOAuthCode(ctx, code); err != nil {
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
