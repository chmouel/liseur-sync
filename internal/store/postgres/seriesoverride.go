package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Series claims (ADR-0018). See the SQLite copy for why the resolution
// is a read-time layer over `book_series` rather than a rewrite of it.

// effectiveSeriesCTE is the WITH prefix every series read is built on.
// The literals are cast explicitly because a bare string literal in a
// UNION arm is of unknown type to Postgres, and the column has to agree
// with the CASE it is unioned against.
const effectiveSeriesCTE = `
WITH eff_series AS (
    SELECT bs.folder_id, bs.book_id, bs.series_id, bs.position,
           'folder'::text AS source
      FROM book_series bs
     WHERE NOT EXISTS (
           SELECT 1 FROM book_series_overrides o
            WHERE o.book_id = bs.book_id
              AND o.scope_user IN ('', ?))
    UNION ALL
    SELECT i.folder_id, i.book_id, i.series_id, i.position,
           (CASE WHEN i.scope_user = '' THEN 'shared' ELSE 'personal' END)::text
      FROM book_series_override_items i
     WHERE i.scope_user = (
           CASE WHEN EXISTS (
                     SELECT 1 FROM book_series_overrides p
                      WHERE p.book_id = i.book_id AND p.scope_user = ?)
                THEN ? ELSE '' END))
`

// effectiveSeriesArgs are the CTE's bind parameters, in order.
func effectiveSeriesArgs(userID string) []any {
	if userID == "" {
		userID = store.NoReaderScope
	}
	return []any{userID, userID, userID}
}

func seriesReadArgs(userID string, rest ...any) []any {
	return append(effectiveSeriesArgs(userID), rest...)
}

// SetBookSeriesOverride replaces one layer's claim about one book.
func (s *Store) SetBookSeriesOverride(
	ctx context.Context, userID, bookID string, scope store.SeriesSource,
	items []store.SeriesClaimItem, at time.Time,
) error {
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return err
	}
	if userID == "" {
		return fmt.Errorf("%w: a series claim needs a writer", store.ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setSeriesClaimTx(
		ctx, tx, scopeUser, userID, bookID, items, at,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func setSeriesClaimTx(
	ctx context.Context, tx *sql.Tx, scopeUser, writer, bookID string,
	items []store.SeriesClaimItem, at time.Time,
) error {
	var folderID string
	err := tx.QueryRowContext(ctx, q(
		`SELECT folder_id FROM books WHERE id = ?`), bookID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO book_series_overrides
		     (folder_id, book_id, scope_user, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (book_id, scope_user) DO UPDATE SET
		     updated_at = excluded.updated_at,
		     updated_by = excluded.updated_by`),
		folderID, bookID, scopeUser, at.UTC(), writer); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM book_series_override_items
		  WHERE book_id = ? AND scope_user = ?`), bookID, scopeUser); err != nil {
		return err
	}

	for _, item := range items {
		seriesID, err := claimSeriesIDTx(ctx, tx, item, at)
		if err != nil {
			return err
		}
		var position any
		if item.Position != nil {
			position = *item.Position
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_series_override_items
			     (folder_id, book_id, scope_user, series_id, position)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (book_id, scope_user, series_id)
			 DO UPDATE SET position = excluded.position`),
			folderID, bookID, scopeUser, seriesID, position); err != nil {
			return err
		}
	}
	return nil
}

func claimSeriesIDTx(
	ctx context.Context, tx *sql.Tx,
	item store.SeriesClaimItem, at time.Time,
) (string, error) {
	if item.SeriesID != "" {
		var found string
		err := tx.QueryRowContext(ctx, q(
			`SELECT id FROM series WHERE id = ?`),
			item.SeriesID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: no series %q",
				store.ErrInvalidInput, item.SeriesID)
		}
		return found, err
	}
	seriesID, err := resolveEntityTx(ctx, tx, "series", item.Name, at)
	if err != nil {
		return "", err
	}
	if seriesID == "" {
		return "", fmt.Errorf("%w: a series claim needs a name",
			store.ErrInvalidInput)
	}
	return seriesID, nil
}

// ClearBookSeriesOverride drops one layer's claim.
func (s *Store) ClearBookSeriesOverride(
	ctx context.Context, userID, bookID string, scope store.SeriesSource,
) error {
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, q(
		`DELETE FROM book_series_overrides WHERE book_id = ? AND scope_user = ?`),
		bookID, scopeUser)
	return err
}

// ReorderSeries restates where books sit in one series, in one layer.
func (s *Store) ReorderSeries(
	ctx context.Context, userID, seriesID string,
	scope store.SeriesSource, order []store.SeriesPlacement, at time.Time,
) error {
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return err
	}
	if userID == "" {
		return fmt.Errorf("%w: a series claim needs a writer", store.ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, q(
		`SELECT 1 FROM series WHERE id = ?`),
		seriesID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	for _, placement := range order {
		// Each book is restated whole, so its memberships of other
		// series have to be carried across: renumbering a trilogy must
		// not drop a volume's place in an omnibus.
		current, err := effectiveSeriesForBookTx(ctx, tx, userID, placement.BookID)
		if err != nil {
			return err
		}
		items := make([]store.SeriesClaimItem, 0, len(current)+1)
		placed := false
		for _, membership := range current {
			item := store.SeriesClaimItem{
				SeriesID: membership.SeriesID,
				Position: membership.Position,
			}
			if membership.SeriesID == seriesID {
				item.Position = placement.Position
				placed = true
			}
			items = append(items, item)
		}
		if !placed {
			items = append(items, store.SeriesClaimItem{
				SeriesID: seriesID, Position: placement.Position,
			})
		}
		if err := setSeriesClaimTx(
			ctx, tx, scopeUser, userID, placement.BookID, items, at,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BookSeriesLayers reads all three layers for one book at once.
func (s *Store) BookSeriesLayers(
	ctx context.Context, userID, bookID string,
) (store.BookSeriesLayers, error) {
	var out store.BookSeriesLayers
	var folderID string
	err := s.db.QueryRowContext(ctx, q(
		`SELECT folder_id FROM books WHERE id = ?`), bookID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	if err != nil {
		return out, err
	}

	if out.Folder, err = seriesRows(ctx, s.db, q(
		`SELECT s.id, s.name, s.normalized_name, bs.position
		   FROM book_series bs JOIN series s ON s.id = bs.series_id
		  WHERE bs.book_id = ?
		  ORDER BY s.normalized_name, s.id`),
		store.SeriesSourceFolder, bookID); err != nil {
		return out, err
	}

	claimed, err := claimedLayers(ctx, s.db, userID, bookID)
	if err != nil {
		return out, err
	}
	out.Shared, out.Personal = claimed.shared, claimed.personal

	switch {
	case claimed.hasPersonal:
		out.Effective, out.Source = claimed.personal, store.SeriesSourcePersonal
	case claimed.hasShared:
		out.Effective, out.Source = claimed.shared, store.SeriesSourceShared
	default:
		out.Effective, out.Source = out.Folder, store.SeriesSourceFolder
	}
	if out.Effective == nil {
		out.Effective = []store.BookSeries{}
	}
	return out, nil
}

// layerRows is what one book's claims look like across both writable
// layers. The booleans matter independently of the slices: an existing
// claim with no memberships is the statement "in no series".
type layerRows struct {
	shared      []store.BookSeries
	personal    []store.BookSeries
	hasShared   bool
	hasPersonal bool
}

func claimedLayers(
	ctx context.Context, db querier, userID, bookID string,
) (layerRows, error) {
	var out layerRows
	rows, err := db.QueryContext(ctx, q(
		`SELECT scope_user FROM book_series_overrides
		  WHERE book_id = ? AND scope_user IN ('', ?)`), bookID, userID)
	if err != nil {
		return out, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var scopeUser string
		if err := scan(&scopeUser); err != nil {
			return err
		}
		if scopeUser == store.SharedSeriesScope {
			out.hasShared = true
		} else {
			out.hasPersonal = true
		}
		return nil
	}); err != nil {
		return out, err
	}

	claimQuery := q(`
		SELECT s.id, s.name, s.normalized_name, i.position
		  FROM book_series_override_items i
		  JOIN series s ON s.id = i.series_id
		 WHERE i.book_id = ? AND i.scope_user = ?
		 ORDER BY s.normalized_name, s.id`)
	if out.hasShared {
		out.shared, err = seriesRows(ctx, db, claimQuery,
			store.SeriesSourceShared, bookID, store.SharedSeriesScope)
		if err != nil {
			return out, err
		}
		if out.shared == nil {
			out.shared = []store.BookSeries{}
		}
	}
	if out.hasPersonal {
		out.personal, err = seriesRows(ctx, db, claimQuery,
			store.SeriesSourcePersonal, bookID, userID)
		if err != nil {
			return out, err
		}
		if out.personal == nil {
			out.personal = []store.BookSeries{}
		}
	}
	return out, nil
}

func effectiveSeriesForBookTx(
	ctx context.Context, tx *sql.Tx, userID, bookID string,
) ([]store.BookSeries, error) {
	rows, err := tx.QueryContext(ctx, q(
		effectiveSeriesCTE+`
		SELECT s.id, s.name, s.normalized_name, e.position
		  FROM eff_series e JOIN series s ON s.id = e.series_id
		 WHERE e.book_id = ?
		 ORDER BY s.normalized_name, s.id`),
		seriesReadArgs(userID, bookID)...)
	if err != nil {
		return nil, err
	}
	return scanSeriesRows(rows, "")
}

// querier is the little that the series readers need of a database
// handle, so they can run against a pool or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func seriesRows(
	ctx context.Context, db querier, query string,
	source store.SeriesSource, args ...any,
) ([]store.BookSeries, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanSeriesRows(rows, source)
}

func scanSeriesRows(
	rows *sql.Rows, source store.SeriesSource,
) ([]store.BookSeries, error) {
	var out []store.BookSeries
	err := eachRow(rows, func(scan func(...any) error) error {
		var series store.BookSeries
		var position sql.NullFloat64
		if err := scan(&series.SeriesID, &series.Name,
			&series.NormalizedName, &position); err != nil {
			return err
		}
		if position.Valid {
			p := position.Float64
			series.Position = &p
		}
		series.Source = source
		out = append(out, series)
		return nil
	})
	return out, err
}
