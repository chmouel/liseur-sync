package postgres

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

const folderColumns = `id, name, root_path, kind, accepts_uploads,
	created_at, updated_at`

func (s *Store) CreateFolder(ctx context.Context, folder store.Folder) error {
	return s.CreateFolderGranting(ctx, folder, "")
}

// CreateFolderGranting writes the folder and its first grant together.
//
// Separating them would leave a window, and a failure in that window
// leaves exactly the state issue #13 reported: a folder on disk that the
// catalog knows about and nobody can read. So the grant is part of the
// same transaction, and an unknown grantee takes the folder down with it
// rather than committing a folder somebody has to notice is invisible.
func (s *Store) CreateFolderGranting(
	ctx context.Context, folder store.Folder, grantUserID string,
) error {
	if !folder.Kind.Valid() {
		return fmt.Errorf("%w: folder kind %q", store.ErrInvalidInput, folder.Kind)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, q(
			`INSERT INTO folders
			     (id, name, root_path, kind, accepts_uploads, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`),
			folder.ID, folder.Name, folder.RootPath, string(folder.Kind),
			folder.AcceptsUploads,
			folder.CreatedAt.UTC(), folder.UpdatedAt.UTC())
		if isUniqueErr(err) {
			return store.ErrConflict
		}
		if err != nil {
			return err
		}
		if grantUserID == "" {
			return nil
		}
		if err := validateGrantTargets(ctx, tx, grantUserID, nil); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, q(`INSERT INTO user_folders
			(user_id, folder_id) VALUES (?, ?)`), grantUserID, folder.ID)
		return err
	})
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

func (s *Store) FolderByID(ctx context.Context, viewerID, folderID string) (store.Folder, error) {
	query := `SELECT ` + folderColumns + ` FROM folders WHERE id = ?`
	args := []any{folderID}
	if viewerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = folders.id AND uf.user_id = ?)`
		args = append(args, viewerID)
	}
	row := s.db.QueryRowContext(ctx, q(query), args...)
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
		query += ` (name COLLATE "C", id) > (?, ?)`
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

// HasAnyFolder answers one question and returns one bit: does this
// server watch anything? The library needs it to tell a reader with no
// grant why their shelf is empty (ADR-0029), and a reader must learn
// nothing else from it.
func (s *Store) HasAnyFolder(ctx context.Context) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM folders LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// FoldersWithGrants marks the folders on one administration page that no
// account can see. One grouped query rather than one query per folder,
// and booleans rather than counts: the page needs to warn, not to
// enumerate who reads what.
func (s *Store) FoldersWithGrants(
	ctx context.Context, folderIDs []string,
) (map[string]bool, error) {
	granted := make(map[string]bool, len(folderIDs))
	if len(folderIDs) == 0 {
		return granted, nil
	}
	for _, id := range folderIDs {
		granted[id] = false
	}
	placeholders, args := inArgs(folderIDs)
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT DISTINCT folder_id FROM user_folders
		 WHERE folder_id IN (`+placeholders+`)`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		granted[id] = true
	}
	return granted, rows.Err()
}

func (s *Store) AssignUserFolder(ctx context.Context, userID, folderID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := validateGrantTargets(ctx, tx, userID, []string{folderID}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO user_folders (user_id, folder_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, folderID)
		return err
	})
}

func (s *Store) UnassignUserFolder(ctx context.Context, userID, folderID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := validateGrantTargets(ctx, tx, userID, []string{folderID}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM user_folders WHERE user_id = $1 AND folder_id = $2`, userID, folderID)
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_folders WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for _, folderID := range unique {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_folders
				(user_id, folder_id) VALUES ($1, $2)`, userID, folderID); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateGrantTargets(ctx context.Context, tx *sql.Tx, userID string, folderIDs []string) error {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1`, userID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return err
	}
	for _, folderID := range folderIDs {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM folders WHERE id = $1`, folderID).Scan(&one); err != nil {
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
