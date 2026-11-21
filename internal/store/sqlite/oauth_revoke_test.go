package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestOAuthConsentSessionExpiresWhileWaiting(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "consent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := storetest.MkUser(t, s, "oauth-expiry")
	now := time.Now().UTC()
	client := store.OAuthClient{ID: "client", Kind: store.OAuthClientRegistered, Name: "client", CreatedAt: now}
	if err := s.CreateOAuthClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Generation = storetest.MkOAuthClientGeneration(t, s, client.ID)
	expires := time.Now().UTC().Add(500 * time.Millisecond)
	if err := s.CreateAuthSession(ctx, store.AuthSession{ID: "session", UserID: u.ID, SHA256: "session-hash", Kind: "web", CreatedAt: now, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	code := store.OAuthCode{SHA256: "code", UserID: u.ID, ClientID: client.ID, ClientGeneration: client.Generation, ClientName: client.Name, RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", Resource: "resource", Scopes: []string{"mcp:library"}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(started); done <- s.CreateOAuthCode(ctx, code, "session") }()
	<-started
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
