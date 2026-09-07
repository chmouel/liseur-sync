package webui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

// Run the real migrations once, then give each fixture its own database file.
// Closing the template checkpoints its WAL before the main file is copied.
var testDatabase = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "webui-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	st, err := sqlite.Open(path)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// NewTestStore is exported only to the external webui test package. Catalog,
// users, sessions and migration state remain private to each fixture.
func NewTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	data, err := testDatabase()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "t.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
