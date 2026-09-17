package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestPutUserSettingsWaitsForAccountLock pins the serialization the
// quota check depends on.
//
// Counting the stored settings inside a transaction does not stop two
// requests from both counting the same rows, both finding room for one
// more key, and both committing over the cap: under READ COMMITTED
// neither sees the other until it has already decided. The writer
// therefore takes the account's row first, and this holds that row from
// outside to prove a second writer waits for it rather than racing
// ahead. It also gives every writer for one account a single order to
// work in, so two overlapping multi-key upserts cannot take the same
// rows in opposite orders and deadlock.
func TestPutUserSettingsWaitsForAccountLock(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reset(t, s)

	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateUser(ctx, store.User{
		ID: "u1", Name: "alice", Argon2Hash: "x", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	holder, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(ctx,
		`SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, "u1"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.PutUserSettings(ctx, "u1", []store.UserSetting{
			{Key: "reader.font", Value: "literata", UpdatedAt: now},
		}, 256)
	}()

	select {
	case err := <-done:
		t.Fatalf("write did not wait for the account lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write failed once the lock was free: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("write never completed after the lock was released")
	}

	got, err := s.GetUserSettings(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != "literata" {
		t.Fatalf("setting not stored: %v", got)
	}
}
