package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Merging and splitting a series (ADR-0021); see the SQLite copy for
// why a merge leaves a binding behind rather than a hole.

func (s *Store) MergeSeries(
	ctx context.Context, userID, seriesID, intoID string, at time.Time,
) (string, error) {
	if seriesID == intoID {
		return "", fmt.Errorf("%w: a series cannot be merged into itself",
			store.ErrInvalidInput)
	}
	if userID == "" {
		return "", fmt.Errorf("%w: a merge needs a writer", store.ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// The absorbed series' own name is what the binding has to carry.
	// Read it before the row goes, not after.
	var name, normalized string
	err = tx.QueryRowContext(ctx,
		q(`SELECT name, normalized_name FROM series WHERE id = ?`),
		seriesID).Scan(&name, &normalized)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if err := seriesExistsTx(ctx, tx, intoID); err != nil {
		return "", err
	}

	// A book on both shelves keeps the survivor's place on it, so its
	// duplicate membership goes before the rest are repointed. UPDATE OR
	// IGNORE would leave those rows behind instead of merging them.
	if _, err := tx.ExecContext(ctx,
		q(`DELETE FROM book_series
		  WHERE series_id = ?
		    AND book_id IN (SELECT book_id FROM book_series WHERE series_id = ?)`),
		seriesID, intoID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE book_series SET series_id = ? WHERE series_id = ?`),
		intoID, seriesID); err != nil {
		return "", err
	}
	// A reader's claim has to follow the shelf it names, or the book
	// they filed by hand falls off it.
	if _, err := tx.ExecContext(ctx,
		q(`DELETE FROM book_series_override_items
		  WHERE series_id = ?
		    AND (book_id, scope_user) IN (
		        SELECT book_id, scope_user FROM book_series_override_items
		         WHERE series_id = ?)`),
		seriesID, intoID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE book_series_override_items SET series_id = ? WHERE series_id = ?`),
		intoID, seriesID); err != nil {
		return "", err
	}
	// Bindings that pointed at the absorbed series point at the survivor
	// now, so a binding always names a live shelf and the resolver never
	// has a chain to follow.
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE series_bindings SET series_id = ? WHERE series_id = ?`),
		intoID, seriesID); err != nil {
		return "", err
	}
	if err := bindSeriesTx(
		ctx, tx, "", name, normalized, intoID, userID, at,
	); err != nil {
		return "", err
	}
	// The absorbed row goes last. Its renames go with it: the survivor's
	// name is the name, and a merge that quietly renamed the survivor
	// for a reader who was never asked would be worse than one that
	// forgets a nickname.
	if _, err := tx.ExecContext(ctx,
		q(`DELETE FROM series WHERE id = ?`), seriesID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return intoID, nil
}

func (s *Store) SplitSeriesFolder(
	ctx context.Context, userID, seriesID, folderID, name string, at time.Time,
) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("%w: a split needs a writer", store.ErrInvalidInput)
	}
	name, normalized, err := seriesRenameName(name)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := seriesExistsTx(ctx, tx, seriesID); err != nil {
		return "", err
	}

	// Splitting is only meaningful where the shelf holds more than one
	// folder's books. Everything from one folder is a rename, and
	// nothing from it is a request about books that are not there.
	var here, elsewhere int
	if err := tx.QueryRowContext(ctx,
		q(`SELECT
		    COUNT(*) FILTER (WHERE folder_id = ?),
		    COUNT(*) FILTER (WHERE folder_id <> ?)
		   FROM book_series WHERE series_id = ?`),
		folderID, folderID, seriesID).Scan(&here, &elsewhere); err != nil {
		return "", err
	}
	if here == 0 {
		return "", fmt.Errorf("%w: that folder has no books on this shelf",
			store.ErrNotFound)
	}
	if elsewhere == 0 {
		return "", fmt.Errorf(
			"%w: every book on this shelf came from that folder, so this is a rename",
			store.ErrInvalidInput)
	}
	var taken string
	err = tx.QueryRowContext(ctx,
		q(`SELECT id FROM series WHERE normalized_name = ?`), normalized).Scan(&taken)
	switch {
	case err == nil:
		return "", fmt.Errorf("%w: %q already names another series",
			store.ErrConflict, name)
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}

	newID := store.NewID()
	if _, err := tx.ExecContext(ctx,
		q(`INSERT INTO series (id, name, normalized_name, created_at)
		 VALUES (?, ?, ?, ?)`),
		newID, name, normalized, at.UTC()); err != nil {
		return "", err
	}
	// Every name that folds into the old shelf folds into the new one
	// *in this folder*: its own, and any this shelf absorbed in an
	// earlier merge. Binding only the first would send a folder whose
	// books arrived through a merge straight back to the old shelf.
	names, err := foldedNamesTx(ctx, tx, seriesID)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if err := bindSeriesTx(
			ctx, tx, folderID, n.name, n.normalized, newID, userID, at,
		); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE book_series SET series_id = ? WHERE series_id = ? AND folder_id = ?`),
		newID, seriesID, folderID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE book_series_override_items SET series_id = ?
		  WHERE series_id = ? AND folder_id = ?`),
		newID, seriesID, folderID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newID, nil
}

func (s *Store) SeriesBindings(
	ctx context.Context, seriesID string,
) ([]store.SeriesBinding, error) {
	rows, err := s.db.QueryContext(ctx,
		q(`SELECT b.id, COALESCE(b.folder_id, ''), COALESCE(f.name, ''),
		        b.name, b.normalized_name, b.series_id, b.created_at, b.created_by
		   FROM series_bindings b
		   LEFT JOIN folders f ON f.id = b.folder_id
		  WHERE b.series_id = ?
		  ORDER BY b.normalized_name`), seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SeriesBinding
	for rows.Next() {
		var b store.SeriesBinding
		if err := rows.Scan(&b.ID, &b.FolderID, &b.FolderName, &b.Name,
			&b.NormalizedName, &b.SeriesID, &b.CreatedAt, &b.CreatedBy); err != nil {
			return nil, err
		}
		b.CreatedAt = b.CreatedAt.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSeriesBinding(ctx context.Context, bindingID string) error {
	res, err := s.db.ExecContext(ctx,
		q(`DELETE FROM series_bindings WHERE id = ?`), bindingID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

type foldedName struct{ name, normalized string }

// foldedNamesTx is every observed name that currently resolves to one
// series: the name a pass gave it, plus every name it has absorbed.
func foldedNamesTx(
	ctx context.Context, tx *sql.Tx, seriesID string,
) ([]foldedName, error) {
	rows, err := tx.QueryContext(ctx,
		q(`SELECT name, normalized_name FROM series WHERE id = ?
		 UNION
		 SELECT name, normalized_name FROM series_bindings WHERE series_id = ?`),
		seriesID, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []foldedName
	for rows.Next() {
		var n foldedName
		if err := rows.Scan(&n.name, &n.normalized); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// bindSeriesTx writes one binding, replacing whatever held that key.
// Written as a delete and an insert rather than an upsert because the
// key is an expression index, and naming an expression in ON CONFLICT is
// the kind of thing that works in one backend and not the other.
func bindSeriesTx(
	ctx context.Context, tx *sql.Tx,
	folderID, name, normalized, seriesID, userID string, at time.Time,
) error {
	var folder any
	if folderID != "" {
		folder = folderID
	}
	if _, err := tx.ExecContext(ctx,
		q(`DELETE FROM series_bindings
		  WHERE normalized_name = ? AND COALESCE(folder_id, '') = ?`),
		normalized, folderID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		q(`INSERT INTO series_bindings
		     (id, folder_id, name, normalized_name, series_id, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		store.NewID(), folder, name, normalized, seriesID,
		at.UTC(), userID)
	return err
}

func (s *Store) SeriesFolders(
	ctx context.Context, seriesID string,
) ([]store.SeriesFolderCount, error) {
	rows, err := s.db.QueryContext(ctx,
		q(`SELECT bs.folder_id, f.name, COUNT(*)
		   FROM book_series bs
		   JOIN folders f ON f.id = bs.folder_id
		  WHERE bs.series_id = ?
		  GROUP BY bs.folder_id, f.name
		  ORDER BY COUNT(*) DESC, f.name`), seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SeriesFolderCount
	for rows.Next() {
		var c store.SeriesFolderCount
		if err := rows.Scan(&c.FolderID, &c.Name, &c.BookCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
