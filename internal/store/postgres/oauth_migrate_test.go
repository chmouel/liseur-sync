package postgres

import (
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestOAuthClientGenerationUpgrade(t *testing.T) {
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
	ctx := t.Context()
	now := time.Now().UTC()
	through, ok := migrationsThrough("oauthClientGenerations")
	if !ok {
		t.Fatal("missing migration boundary")
	}
	for i, migration := range through {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, q(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`), i+1, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateUser(ctx, store.User{ID: "u", Name: "u", Argon2Hash: "x", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, q(`INSERT INTO oauth_clients (id, kind, name, created_at) VALUES ('client', 'metadata', 'client', ?)`), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, q(`INSERT INTO oauth_grants (id, user_id, client_id, client_name, scopes, resource, created_at) VALUES ('grant', 'u', 'client', 'client', 'mcp:text', 'resource', ?)`), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, q(`INSERT INTO oauth_tokens (sha256, grant_id, kind, created_at, expires_at) VALUES ('token', 'grant', 'access', ?, ?)`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := s.OAuthClient(ctx, "client")
	if err != nil || client.Generation == "" {
		t.Fatalf("client after upgrade: %+v %v", client, err)
	}
	if _, err := s.OAuthAccess(ctx, "token", now); err != nil {
		t.Fatalf("access after upgrade: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := s.OAuthClient(ctx, "client")
	if err != nil || again.Generation != client.Generation {
		t.Fatalf("generation changed: %+v %v", again, err)
	}
}
