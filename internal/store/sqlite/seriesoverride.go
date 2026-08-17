package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Series claims (ADR-0018) sit on top of what a reconcile pass observed
// rather than inside it. `book_series` keeps meaning "what the folder
// said on the last pass" and is still rewritten wholesale by every pass;
// a claim is applied when a series is read.
//
// Resolution is personal, then shared, then folder, per book. A claim
// speaks for the whole book — its memberships replace the observed ones
// rather than being merged with them — so a book whose claim has no
// memberships is in no series, which is how a stray volume gets detached
// from the series its directory implied.

// seriesNamesCTE resolves each series' display name for one reader:
// their own rename, then the shared one, then what a scan observed
// (ADR-0020). It carries the scanned name and the layer beside the
// resolved one, because a client that offers a revert has to be able to
// tell whether reverting would change anything.
//
// The scanned normalized_name is never resolved away in the other
// direction: it stays what a reconcile pass matches an observed name
// against, so a rename cannot move the fold key out from under itself.
const seriesNamesCTE = `series_names AS (
    SELECT s.id AS series_id,
           COALESCE(p.name, sh.name, s.name) AS name,
           COALESCE(p.normalized_name, sh.normalized_name, s.normalized_name)
               AS normalized_name,
           s.name AS scanned_name,
           CASE WHEN p.series_id IS NOT NULL THEN 'personal'
                WHEN sh.series_id IS NOT NULL THEN 'shared'
                ELSE 'folder' END AS name_source
      FROM series s
      LEFT JOIN series_name_overrides p
             ON p.series_id = s.id AND p.scope_user = ?
      LEFT JOIN series_name_overrides sh
             ON sh.series_id = s.id AND sh.scope_user = '')`

// seriesNamesOnlyCTE is for reads that display a series without
// resolving membership — the layer views, which read one claim's rows
// directly.
const seriesNamesOnlyCTE = "\nWITH " + seriesNamesCTE + "\n"

// seriesNamesArgs is the name CTE's single bind parameter.
func seriesNamesArgs(userID string) []any {
	if userID == "" {
		userID = store.NoReaderScope
	}
	return []any{userID}
}

// effectiveSeriesCTE is the WITH prefix every series read is built on.
// It stands in for `book_series` and carries one extra column, `source`,
// naming the layer each row came from, and it brings the resolved names
// along so that a read which shows a series never has to ask twice.
//
// It is a prefix rather than an inline subquery so that its bind
// parameters always come first: a caller appends its own arguments after
// effectiveSeriesArgs and never has to count placeholders.
const effectiveSeriesCTE = `
WITH ` + seriesNamesCTE + `,
eff_series AS (
    -- What the pass observed, for books no layer has claimed.
    SELECT bs.folder_id, bs.book_id, bs.series_id, bs.position,
           'folder' AS source
      FROM book_series bs
		WHERE NOT EXISTS (
			SELECT 1 FROM book_series_overrides o
			 WHERE o.book_id = bs.book_id
			   AND o.scope_user IN ('', ?)
			   AND o.deleted_at IS NULL)
    UNION ALL
    -- The winning claim's memberships. A personal claim wins over a
    -- shared one; the CASE says so in as many words rather than leaning
    -- on '' sorting below every user id.
    SELECT i.folder_id, i.book_id, i.series_id, i.position,
           CASE WHEN i.scope_user = '' THEN 'shared' ELSE 'personal' END
      FROM book_series_override_items i
     WHERE i.scope_user = (
			CASE WHEN EXISTS (
				      SELECT 1 FROM book_series_overrides p
				       WHERE p.book_id = i.book_id AND p.scope_user = ?
				         AND p.deleted_at IS NULL)
                THEN ? ELSE '' END))
`

// effectiveSeriesArgs are the CTE's bind parameters, in order: the name
// layer's reader first, then the claim layer's. A user id that would be
// empty collides with the shared layer's sentinel, so an anonymous
// reader is given a value no account can have; it matches no personal
// claim and no personal rename, which is the correct answer for nobody
// in particular.
func effectiveSeriesArgs(userID string) []any {
	if userID == "" {
		userID = store.NoReaderScope
	}
	return []any{userID, userID, userID, userID}
}

// seriesReadArgs prefixes a query's own arguments with the CTE's.
func seriesReadArgs(userID string, rest ...any) []any {
	return append(effectiveSeriesArgs(userID), rest...)
}

// SetBookSeriesOverride replaces one layer's claim about one book.
func (s *Store) SetBookSeriesOverride(
	ctx context.Context, userID, bookID string, scope store.SeriesSource,
	items []store.SeriesClaimItem, mutation store.SeriesClaimMutation,
) (store.SeriesClaimOutcome, error) {
	clientTS, ifUpdatedAt, at := mutation.ClientTS, mutation.IfUpdatedAt, mutation.At.UTC()
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return "", fmt.Errorf("%w: a series claim needs a writer", store.ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	outcome, err := setSeriesClaimTx(
		ctx, tx, scopeUser, userID, bookID, items, clientTS, ifUpdatedAt, at,
	)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return outcome, nil
}

// setSeriesClaimTx writes one claim. It resolves the book's folder first
// because every row of the claim carries it: the composite foreign keys
// are what stop a claim naming a series from another folder.
func setSeriesClaimTx(
	ctx context.Context, tx *sql.Tx, scopeUser, writer, bookID string,
	items []store.SeriesClaimItem, clientTS string, ifUpdatedAt *time.Time,
	at time.Time,
) (store.SeriesClaimOutcome, error) {
	var folderID string
	err := tx.QueryRowContext(ctx,
		`SELECT folder_id FROM books WHERE id = ?`, bookID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.SeriesID == "" && strings.TrimSpace(item.Name) == "" {
			return "", fmt.Errorf("%w: a series claim needs a name", store.ErrInvalidInput)
		}
	}
	var updatedAt, storedClientTS, storedHash string
	err = tx.QueryRowContext(ctx, `SELECT updated_at, client_ts, request_hash
		FROM book_series_overrides WHERE book_id = ? AND scope_user = ?`,
		bookID, scopeUser).Scan(&updatedAt, &storedClientTS, &storedHash)
	if err == nil {
		current, err := parseTime(updatedAt)
		if err != nil {
			return "", err
		}
		hash := store.SeriesClaimRequestHash(false, items)
		if clientTS != "" && clientTS == storedClientTS {
			if hash == storedHash {
				return store.SeriesClaimDuplicate, nil
			}
			return "", store.ErrConflict
		}
		if ifUpdatedAt != nil && !current.Equal(ifUpdatedAt.UTC()) {
			return store.SeriesClaimStale, nil
		}
	} else if errors.Is(err, sql.ErrNoRows) {
	} else {
		return "", err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO book_series_overrides
		     (folder_id, book_id, scope_user, updated_at, updated_by, deleted_at, client_ts, request_hash)
		 VALUES (?, ?, ?, ?, ?, NULL, ?, ?)
		 ON CONFLICT (book_id, scope_user) DO UPDATE SET
		     updated_at = excluded.updated_at,
		     updated_by = excluded.updated_by,
		     deleted_at = NULL, client_ts = excluded.client_ts,
		     request_hash = excluded.request_hash`,
		folderID, bookID, scopeUser, formatTime(at), writer, clientTS,
		store.SeriesClaimRequestHash(false, items)); err != nil {
		return "", err
	}
	// The claim is stated whole, so the previous memberships go rather
	// than being reconciled against the new ones.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM book_series_override_items
		  WHERE book_id = ? AND scope_user = ?`, bookID, scopeUser); err != nil {
		return "", err
	}

	for _, item := range items {
		seriesID, err := claimSeriesIDTx(ctx, tx, item, at)
		if err != nil {
			return "", err
		}
		var position any
		if item.Position != nil {
			position = *item.Position
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_series_override_items
			     (folder_id, book_id, scope_user, series_id, position)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (book_id, scope_user, series_id)
			 DO UPDATE SET position = excluded.position`,
			folderID, bookID, scopeUser, seriesID, position); err != nil {
			return "", err
		}
	}
	return store.SeriesClaimApplied, nil
}

// claimSeriesIDTx turns one claim item into a series id. An id must
// already name a series; a name creates one through the same path a pass
// uses, so an override-born series folds by normalized name and is
// thereafter indistinguishable from a scanned one.
func claimSeriesIDTx(
	ctx context.Context, tx *sql.Tx,
	item store.SeriesClaimItem, at time.Time,
) (string, error) {
	if item.SeriesID != "" {
		var found string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM series WHERE id = ?`,
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
	mutation store.SeriesClaimMutation,
) (store.SeriesClaimOutcome, error) {
	clientTS, ifUpdatedAt, at := mutation.ClientTS, mutation.IfUpdatedAt, mutation.At.UTC()
	scopeUser, err := scope.ScopeUser(userID)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var folderID string
	if err := tx.QueryRowContext(ctx, `SELECT folder_id FROM books WHERE id = ?`, bookID).Scan(&folderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", err
	}
	var updatedAt, storedClientTS, storedHash string
	err = tx.QueryRowContext(ctx, `SELECT updated_at, client_ts, request_hash
		FROM book_series_overrides WHERE book_id = ? AND scope_user = ?`,
		bookID, scopeUser).Scan(&updatedAt, &storedClientTS, &storedHash)
	if err == nil {
		current, err := parseTime(updatedAt)
		if err != nil {
			return "", err
		}
		hash := store.SeriesClaimRequestHash(true, nil)
		if clientTS != "" && clientTS == storedClientTS {
			if hash == storedHash {
				return store.SeriesClaimDuplicate, nil
			}
			return "", store.ErrConflict
		}
		if ifUpdatedAt != nil && !current.Equal(ifUpdatedAt.UTC()) {
			return store.SeriesClaimStale, nil
		}
	} else if errors.Is(err, sql.ErrNoRows) {
	} else {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO book_series_overrides
		(folder_id, book_id, scope_user, updated_at, updated_by, deleted_at, client_ts, request_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (book_id, scope_user) DO UPDATE SET
		updated_at = excluded.updated_at, updated_by = excluded.updated_by,
		deleted_at = excluded.deleted_at, client_ts = excluded.client_ts,
		request_hash = excluded.request_hash`, folderID, bookID, scopeUser,
		formatTime(at), userID, formatTime(at), clientTS,
		store.SeriesClaimRequestHash(true, nil)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_series_override_items
		WHERE book_id = ? AND scope_user = ?`, bookID, scopeUser); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return store.SeriesClaimApplied, nil
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
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM series WHERE id = ?`,
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
		if _, err := setSeriesClaimTx(
			ctx, tx, scopeUser, userID, placement.BookID, items, "", nil, at,
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
	err := s.db.QueryRowContext(ctx,
		`SELECT folder_id FROM books WHERE id = ?`, bookID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	if err != nil {
		return out, err
	}

	if out.Folder, err = seriesRows(ctx, s.db,
		seriesNamesOnlyCTE+`
		SELECT n.series_id, n.name, n.normalized_name, bs.position
		   FROM book_series bs JOIN series_names n ON n.series_id = bs.series_id
		  WHERE bs.book_id = ?
		  ORDER BY n.normalized_name, n.series_id`,
		store.SeriesSourceFolder,
		append(seriesNamesArgs(userID), bookID)...); err != nil {
		return out, err
	}

	claimed, err := claimedLayers(ctx, s.db, userID, bookID)
	if err != nil {
		return out, err
	}
	out.Shared, out.Personal = claimed.shared, claimed.personal
	out.SharedUpdatedAt, out.PersonalUpdatedAt = claimed.sharedUpdatedAt, claimed.personalUpdatedAt

	switch {
	case claimed.hasPersonal:
		out.Effective, out.Source = claimed.personal, store.SeriesSourcePersonal
	case claimed.hasShared:
		out.Effective, out.Source = claimed.shared, store.SeriesSourceShared
	default:
		out.Effective, out.Source = out.Folder, store.SeriesSourceFolder
	}
	// An empty claim is "in no series", so the slice stays non-nil to
	// keep that distinguishable from "no claim" in a payload.
	if out.Effective == nil {
		out.Effective = []store.BookSeries{}
	}
	return out, nil
}

// layerRows is what one book's claims look like across both writable
// layers. The booleans matter independently of the slices: an existing
// claim with no memberships is the statement "in no series".
type layerRows struct {
	shared            []store.BookSeries
	personal          []store.BookSeries
	hasShared         bool
	hasPersonal       bool
	sharedUpdatedAt   *time.Time
	personalUpdatedAt *time.Time
}

func claimedLayers(
	ctx context.Context, db querier, userID, bookID string,
) (layerRows, error) {
	var out layerRows
	rows, err := db.QueryContext(ctx,
		`SELECT scope_user, updated_at, deleted_at FROM book_series_overrides
		  WHERE book_id = ? AND scope_user IN ('', ?)`, bookID, userID)
	if err != nil {
		return out, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var scopeUser, updatedAt string
		var deletedAt sql.NullString
		if err := scan(&scopeUser, &updatedAt, &deletedAt); err != nil {
			return err
		}
		at, err := parseTime(updatedAt)
		if err != nil {
			return err
		}
		if scopeUser == store.SharedSeriesScope {
			out.sharedUpdatedAt = &at
			if !deletedAt.Valid {
				out.hasShared = true
			}
		} else {
			out.personalUpdatedAt = &at
			if !deletedAt.Valid {
				out.hasPersonal = true
			}
		}
		return nil
	}); err != nil {
		return out, err
	}

	const claimQuery = seriesNamesOnlyCTE + `
		SELECT n.series_id, n.name, n.normalized_name, i.position
		  FROM book_series_override_items i
		  JOIN series_names n ON n.series_id = i.series_id
		 WHERE i.book_id = ? AND i.scope_user = ?
		 ORDER BY n.normalized_name, n.series_id`
	if out.hasShared {
		out.shared, err = seriesRows(ctx, db, claimQuery,
			store.SeriesSourceShared,
			append(seriesNamesArgs(userID), bookID, store.SharedSeriesScope)...)
		if err != nil {
			return out, err
		}
		if out.shared == nil {
			out.shared = []store.BookSeries{}
		}
	}
	if out.hasPersonal {
		out.personal, err = seriesRows(ctx, db, claimQuery,
			store.SeriesSourcePersonal,
			append(seriesNamesArgs(userID), bookID, userID)...)
		if err != nil {
			return out, err
		}
		if out.personal == nil {
			out.personal = []store.BookSeries{}
		}
	}
	return out, nil
}

// effectiveSeriesForBookTx reads one book's series as they resolve for a
// reader, inside a transaction.
func effectiveSeriesForBookTx(
	ctx context.Context, tx *sql.Tx, userID, bookID string,
) ([]store.BookSeries, error) {
	rows, err := tx.QueryContext(ctx,
		effectiveSeriesCTE+`
		SELECT n.series_id, n.name, n.normalized_name, e.position
		  FROM eff_series e JOIN series_names n ON n.series_id = e.series_id
		 WHERE e.book_id = ?
		 ORDER BY n.normalized_name, n.series_id`,
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
