package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// A folder has no owner. An empty viewer id is the trusted internal
// view; every real viewer must have a user_folders grant.

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

func (s *Store) FolderByID(ctx context.Context, viewerID, folderID string) (store.Folder, error) {
	query := `SELECT ` + folderColumns + ` FROM folders WHERE id = ?`
	args := []any{folderID}
	if viewerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = folders.id AND uf.user_id = ?)`
		args = append(args, viewerID)
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	folder, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Folder{}, store.ErrNotFound
	}
	return folder, err
}

func (s *Store) ListFolders(ctx context.Context, viewerID, after string, limit int) ([]store.Folder, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + folderColumns + ` FROM folders`
	args := []any{}
	where := false
	if viewerID != "" {
		query += ` WHERE EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = folders.id AND uf.user_id = ?)`
		args = append(args, viewerID)
		where = true
	}
	if after != "" {
		name, id := store.SplitFolderCursor(after)
		if where {
			query += ` AND`
		} else {
			query += ` WHERE`
		}
		query += ` (name, id) > (?, ?)`
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

func (s *Store) AssignUserFolder(ctx context.Context, userID, folderID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := validateGrantTargets(ctx, tx, userID, []string{folderID}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_folders
			(user_id, folder_id) VALUES (?, ?)`, userID, folderID)
		return err
	})
}

func (s *Store) UnassignUserFolder(ctx context.Context, userID, folderID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := validateGrantTargets(ctx, tx, userID, []string{folderID}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM user_folders WHERE user_id = ? AND folder_id = ?`, userID, folderID)
		return err
	})
}

func (s *Store) ListUserFolders(ctx context.Context, userID, after string, limit int) ([]store.Folder, error) {
	if _, err := s.UserByID(ctx, userID); err != nil {
		return nil, err
	}
	return s.ListFolders(ctx, userID, after, limit)
}

func (s *Store) ReplaceUserFolders(ctx context.Context, userID string, folderIDs []string) error {
	unique := make([]string, 0, len(folderIDs))
	seen := make(map[string]struct{}, len(folderIDs))
	for _, id := range folderIDs {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := validateGrantTargets(ctx, tx, userID, unique); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_folders WHERE user_id = ?`, userID); err != nil {
			return err
		}
		for _, folderID := range unique {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_folders
				(user_id, folder_id) VALUES (?, ?)`, userID, folderID); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateGrantTargets(ctx context.Context, tx *sql.Tx, userID string, folderIDs []string) error {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return err
	}
	for _, folderID := range folderIDs {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM folders WHERE id = ?`, folderID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
	}
	return nil
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
