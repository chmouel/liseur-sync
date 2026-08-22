package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Series renames (ADR-0020); see the SQLite copy for why the scanned
// name is left alone and whose view a collision is checked against.

func (s *Store) SetSeriesName(
	ctx context.Context, userID, seriesID string, scope store.SeriesSource,
	name string, at time.Time,
) error {
	if _, err := s.CatalogEntityByID(ctx, userID, seriesID, store.EntitySeries); err != nil {
		return err
	}
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return err
	}
	if userID == "" {
		return fmt.Errorf("%w: a series rename needs a writer", store.ErrInvalidInput)
	}
	name, normalized, err := seriesRenameName(name)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := seriesExistsTx(ctx, tx, seriesID); err != nil {
		return err
	}
	var holder string
	err = tx.QueryRowContext(ctx, q(
		seriesNamesOnlyCTE+`
		SELECT series_id FROM series_names
		 WHERE normalized_name = ? AND series_id <> ? LIMIT 1`),
		append(seriesNamesArgs(renameViewer(scope, scopeUser)),
			normalized, seriesID)...).Scan(&holder)
	switch {
	case err == nil:
		return fmt.Errorf("%w: %q already names another series", store.ErrConflict, name)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO series_name_overrides
		     (series_id, scope_user, name, normalized_name, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (series_id, scope_user) DO UPDATE SET
		     name = excluded.name,
		     normalized_name = excluded.normalized_name,
		     updated_at = excluded.updated_at,
		     updated_by = excluded.updated_by`),
		seriesID, scopeUser, name, normalized, at.UTC(), userID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClearSeriesName(
	ctx context.Context, userID, seriesID string, scope store.SeriesSource,
) error {
	if _, err := s.CatalogEntityByID(ctx, userID, seriesID, store.EntitySeries); err != nil {
		return err
	}
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := seriesExistsTx(ctx, tx, seriesID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM series_name_overrides
		  WHERE series_id = ? AND scope_user = ?`),
		seriesID, scopeUser); err != nil {
		return err
	}
	return tx.Commit()
}

func seriesExistsTx(ctx context.Context, tx *sql.Tx, seriesID string) error {
	var one int
	err := tx.QueryRowContext(ctx, q(
		`SELECT 1 FROM series WHERE id = ?`), seriesID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// seriesRenameName validates a name at the store edge and returns it
// with its key.
func seriesRenameName(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if len(name) > store.MaxSeriesNameBytes {
		return "", "", fmt.Errorf("%w: series name is too long", store.ErrInvalidInput)
	}
	normalized := metadata.NormalizeName(name)
	if normalized == "" {
		return "", "", fmt.Errorf("%w: a series name cannot be empty",
			store.ErrInvalidInput)
	}
	return name, normalized, nil
}

// renameViewer is whose view a collision is checked against.
func renameViewer(scope store.SeriesSource, scopeUser string) string {
	if scope == store.SeriesSourceShared {
		return store.NoReaderScope
	}
	return scopeUser
}
