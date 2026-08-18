package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// A folder has no owner and no access list, so none of these queries
// take a user id. That is ADR-0017's deliberate carve-out from the
// user-scoping rule: the catalog is shared, reading state is not.

const folderColumns = `id, name, root_path, kind, accepts_uploads,
	created_at, updated_at`

func (s *Store) CreateFolder(ctx context.Context, folder store.Folder) error {
	if !folder.Kind.Valid() {
		return fmt.Errorf("%w: folder kind %q", store.ErrInvalidInput, folder.Kind)
	}
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO folders
		     (id, name, root_path, kind, accepts_uploads, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		folder.ID, folder.Name, folder.RootPath, string(folder.Kind),
		folder.AcceptsUploads,
		folder.CreatedAt.UTC(), folder.UpdatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func scanFolder(row interface{ Scan(...any) error }) (store.Folder, error) {
	var (
		f    store.Folder
		kind string
	)
	if err := row.Scan(&f.ID, &f.Name, &f.RootPath, &kind,
		&f.AcceptsUploads, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return store.Folder{}, err
	}
	f.Kind = store.FolderKind(kind)
	f.CreatedAt = f.CreatedAt.UTC()
	f.UpdatedAt = f.UpdatedAt.UTC()
	return f, nil
}

// SetFolderUploads is the whole of ADR-0023's permission model: one
// boolean an administrator sets on the folder whose path they chose.
func (s *Store) SetFolderUploads(
	ctx context.Context, folderID string, accepts bool, at time.Time,
) error {
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE folders SET accepts_uploads = ?, updated_at = ? WHERE id = ?`),
		accepts, at.UTC(), folderID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) FolderByID(ctx context.Context, folderID string) (store.Folder, error) {
	row := s.db.QueryRowContext(ctx,
		q(`SELECT `+folderColumns+` FROM folders WHERE id = ?`), folderID)
	folder, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Folder{}, store.ErrNotFound
	}
	return folder, err
}

func (s *Store) ListFolders(ctx context.Context, after string, limit int) ([]store.Folder, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + folderColumns + ` FROM folders`
	args := []any{}
	if after != "" {
		name, id := store.SplitFolderCursor(after)
		query += ` WHERE (name COLLATE "C", id) > (?, ?)`
		args = append(args, name, id)
	}
	// COLLATE "C" is byte order, which is what SQLite gives with no
	// collation at all. Without it a database created in en_US.UTF-8
	// sorts "Same Root" before "aaa" while SQLite sorts it after, and
	// the two backends hand out different pages for the same cursor.
	// The comparison above has to use the same collation as the sort or
	// keyset pagination skips rows.
	query += ` ORDER BY name COLLATE "C", id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	folders := []store.Folder{}
	for rows.Next() {
		folder, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

// DeleteFolder removes the folder row. Everything catalog-side hangs off
// it by cascade; nothing under root_path is touched, because those files
// were never this server's to delete.
func (s *Store) DeleteFolder(ctx context.Context, folderID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			q(`DELETE FROM folders WHERE id = ?`), folderID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return store.ErrNotFound
		}
		// The books went with the folder, so a series only this folder
		// held now belongs to nobody (ADR-0019).
		return collectOrphanEntitiesTx(ctx, tx)
	})
}
