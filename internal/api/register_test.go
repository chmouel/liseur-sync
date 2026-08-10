package api

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestRegisterWithInvite(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	// Admin creates an invite.
	hash, _ := auth.HashPassword("hunter2hunter")
	admin := store.User{ID: "admin1", Name: "root", Argon2Hash: hash, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	code := "test-invite-code"
	id, _ := auth.NewSecret()
	if err := st.CreateInvite(ctx, store.Invite{
		ID: id, CodeSHA256: auth.HashSecret(code), CreatedBy: admin.ID,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Register with the invite.
	resp, out := post(t, ts.URL+"/v1/register", "", map[string]string{
		"invite": code, "username": "newbie", "password": "long-enough-password",
	})
	if resp != 201 {
		t.Fatalf("register: %d %v", resp, out)
	}

	// Invite is single-use.
	resp, _ = post(t, ts.URL+"/v1/register", "", map[string]string{
		"invite": code, "username": "other", "password": "long-enough-password",
	})
	if resp != 403 {
		t.Fatalf("invite reuse: want 403, got %d", resp)
	}

	// Bad invite.
	resp, _ = post(t, ts.URL+"/v1/register", "", map[string]string{
		"invite": "bogus", "username": "x", "password": "long-enough-password",
	})
	if resp != 403 {
		t.Fatalf("bad invite: want 403, got %d", resp)
	}

	// Short password rejected.
	resp, _ = post(t, ts.URL+"/v1/register", "", map[string]string{
		"invite": code, "username": "x", "password": "short",
	})
	if resp != 400 {
		t.Fatalf("short password: want 400, got %d", resp)
	}
}
