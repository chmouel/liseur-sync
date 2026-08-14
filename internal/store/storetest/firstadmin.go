package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCreateFirstAdmin covers the web UI's first-run setup: the very
// first account may be made an administrator by an unauthenticated
// caller, and only the very first.
func testCreateFirstAdmin(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()

	first := store.User{
		ID: "u-first", Name: "founder", Argon2Hash: "h", CreatedAt: time.Now(),
	}
	if err := s.CreateFirstAdmin(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin || !got.Enabled() {
		t.Fatalf("first account should be an enabled admin: %+v", got)
	}
	if got.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC", got.Timezone)
	}

	// Once anybody is here, setup is closed — including under a name
	// that is not taken, which is the case a plain unique-index guard
	// would let through.
	err = s.CreateFirstAdmin(ctx, store.User{
		ID: "u-second", Name: "interloper", Argon2Hash: "h", CreatedAt: time.Now(),
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second setup: want ErrConflict, got %v", err)
	}
	if _, err := s.UserByName(ctx, "interloper"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused setup must not leave an account: %v", err)
	}
}

// testConcurrentFirstAdmin proves the emptiness check and the insert
// are one atomic step: several people opening the setup page of a fresh
// instance at the same moment produce exactly one administrator.
func testConcurrentFirstAdmin(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.CreateFirstAdmin(ctx, store.User{
				ID:         "u-" + string(rune('a'+i)),
				Name:       "founder-" + string(rune('a'+i)),
				Argon2Hash: "h", CreatedAt: time.Now(),
			})
		}()
	}
	close(start)
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, store.ErrConflict):
		default:
			t.Fatalf("setup %d: unexpected error %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("winners = %d, want exactly 1", won)
	}
	ids, err := s.UserIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("accounts = %d, want 1", len(ids))
	}
}
