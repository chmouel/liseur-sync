package sqlite

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

func (s *Store) CreateFolder(ctx context.Context, folder store.Folder) error {
	if !folder.Kind.Valid() {
		return fmt.Errorf("%w: folder kind %q", store.ErrInvalidInput, folder.Kind)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO folders
		     (id, name, root_path, kind, accepts_uploads, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		folder.ID, folder.Name, folder.RootPath, string(folder.Kind),
		folder.AcceptsUploads,
		formatTime(folder.CreatedAt), formatTime(folder.UpdatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func scanFolder(row interface{ Scan(...any) error }) (store.Folder, error) {
	var (
		f               store.Folder
		kind            string
		createdAt, upAt string
	)
	if err := row.Scan(&f.ID, &f.Name, &f.RootPath, &kind,
		&f.AcceptsUploads, &createdAt, &upAt); err != nil {
		return store.Folder{}, err
	}
	f.Kind = store.FolderKind(kind)
	var err error
	if f.CreatedAt, err = parseTime(createdAt); err != nil {
		return store.Folder{}, err
	}
	if f.UpdatedAt, err = parseTime(upAt); err != nil {
		return store.Folder{}, err
	}
	return f, nil
}

const folderColumns = `id, name, root_path, kind, accepts_uploads,
	created_at, updated_at`

// SetFolderUploads is the whole of ADR-0023's permission model: one
// boolean an administrator sets on the folder whose path they chose.
func (s *Store) SetFolderUploads(
	ctx context.Context, folderID string, accepts bool, at time.Time,
) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE folders SET accepts_uploads = ?, updated_at = ? WHERE id = ?`,
		accepts, formatTime(at), folderID)
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
		`SELECT `+folderColumns+` FROM folders WHERE id = ?`, folderID)
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
		query += ` WHERE (name, id) > (?, ?)`
		args = append(args, name, id)
	}
	query += ` ORDER BY name, id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
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
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM book_search WHERE folder_id = ?`, folderID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, folderID)
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
