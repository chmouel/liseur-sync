package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// TestStore runs the shared backend suite against PostgreSQL. Skipped
// unless LISEUR_PG_TEST_DSN is set (point it at a throwaway database —
// the suite drops and recreates everything).
func TestStore(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	open := func(t *testing.T) store.Store {
		t.Helper()
		s, err := Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		reset(t, s)
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	storetest.Run(t, open)
}

// reset drops all tables so each test starts from an empty schema.
func reset(t *testing.T, s *Store) {
	t.Helper()
	tables := []string{
		"book_languages", "book_tags", "tags",
		"book_contributors", "contributors",
		"book_series_override_items", "book_series_overrides",
		"book_series", "series",
		"book_identifiers", "user_book_works",
		"books", "folders",
		"session_supersessions", "session_tombstones", "session_rollups", "sessions", "ops", "aliases", "editions",
		"works", "seq_counters", "compaction_state", "kosync_devices",
		"koplugin_devices", "pairing_codes", "invites", "auth_sessions",
		"token_scopes", "tokens", "users", "schema_migrations",
	}
	for _, tbl := range tables {
		if _, err := s.db.ExecContext(context.Background(),
			`DROP TABLE IF EXISTS `+tbl+` CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
}
