package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestStore runs the shared backend suite.
func TestStore(t *testing.T) {
	storetest.Run(t, openStore)
}
