package postgres

// The admin panel's library reads and writes (ADR-0013), the twin of
// the SQLite file of the same name. See it for why the ACL join is
// absent.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// plainLibraryColumns is a library row with no ACL role attached: the
// admin panel's reads and the scanner's, which have no caller to join
// against.
const plainLibraryColumns = `id, owner_user_id, quota_user_id, source,
	storage, refresh, refresh_interval_seconds, name, root_path,
	config_json, created_at, updated_at, last_refresh_at,
	last_refresh_attempt_at, last_refresh_error, refresh_requested_at`

func (s *Store) AdminListLibraries(ctx context.Context, after string, limit int) ([]store.Library, error) {
	name, id := store.SplitLibraryCursor(after)
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+plainLibraryColumns+`
		 FROM libraries
		 WHERE ? = '' OR (name, id) > (?, ?)
		 ORDER BY name, id
		 LIMIT ?`),
		after, name, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Library
	for rows.Next() {
		l, err := scanPlainLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) AdminUserLibraries(ctx context.Context, userID, after string, limit int) ([]store.AccessibleLibrary, error) {
	name, id := store.SplitLibraryCursor(after)
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE (l.owner_user_id = ? OR a.role IS NOT NULL)
		   AND (? = '' OR (l.name, l.id) > (?, ?))
		 ORDER BY l.name, l.id
		 LIMIT ?`),
		userID, userID, userID, after, name, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AccessibleLibrary
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) AdminLibraryGrants(ctx context.Context, libraryID string, limit int) ([]store.LibraryGrant, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT a.library_id, a.user_id, u.name, a.role, a.created_at
		 FROM library_access a
		 JOIN users u ON u.id = a.user_id
		 WHERE a.library_id = ?
		 ORDER BY a.created_at DESC, a.user_id
		 LIMIT ?`),
		libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.LibraryGrant
	for rows.Next() {
		var g store.LibraryGrant
		if err := rows.Scan(&g.LibraryID, &g.UserID, &g.UserName, &g.Role, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.CreatedAt = g.CreatedAt.UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) AdminSetLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role *store.LibraryRole, at time.Time) error {
	_ = actorUserID
	if role == nil {
		res, err := s.db.ExecContext(ctx, q(
			`DELETE FROM library_access WHERE library_id = ? AND user_id = ?`),
			libraryID, userID)
		return affectedOne(res, err)
	}
	if err := checkLibraryRole(*role); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, q(
		`INSERT INTO library_access (library_id, user_id, role, created_at)
		 SELECT l.id, ?, ?, ?
		 FROM libraries l
		 WHERE l.id = ? AND l.owner_user_id <> ?
		 ON CONFLICT (library_id, user_id) DO UPDATE SET role = excluded.role`),
		userID, string(*role), at.UTC(), libraryID, userID)
	return affectedOne(res, err)
}

func (s *Store) AdminSetLibraryConfig(ctx context.Context, actorUserID, libraryID string, configJSON []byte, at time.Time) error {
	_ = actorUserID
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE libraries SET config_json = ?, updated_at = ? WHERE id = ?`),
		configJSON, at.UTC(), libraryID)
	return affectedOne(res, err)
}

// affectedOne turns "the WHERE clause matched nothing" into ErrNotFound.
func affectedOne(res sql.Result, err error) error {
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

// scanPlainLibrary reads a library row with no role column.
func scanPlainLibrary(row interface{ Scan(...any) error }) (store.Library, error) {
	var l store.Library
	var root, refreshError sql.NullString
	var refreshSeconds int64
	if err := row.Scan(&l.ID, &l.OwnerUserID, &l.QuotaUserID,
		&l.Source, &l.Storage, &l.Refresh, &refreshSeconds, &l.Name,
		&root, &l.ConfigJSON, &l.CreatedAt, &l.UpdatedAt,
		&l.LastRefreshAt, &l.LastRefreshAttemptAt, &refreshError,
		&l.RefreshRequestedAt); err != nil {
		return l, err
	}
	if root.Valid {
		l.RootPath = &root.String
	}
	if refreshError.Valid {
		l.LastRefreshError = &refreshError.String
	}
	l.RefreshInterval = store.RefreshIntervalFrom(refreshSeconds)
	l.CreatedAt, l.UpdatedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC()
	utcPtr(l.LastRefreshAt)
	utcPtr(l.LastRefreshAttemptAt)
	utcPtr(l.RefreshRequestedAt)
	return l, nil
}

// utcPtr normalises a nullable timestamp in place, the way every other
// read here normalises its scalar ones. Timestamps are UTC everywhere
// above the store, and pgx hands back the session's zone.
func utcPtr(t *time.Time) {
	if t != nil {
		*t = t.UTC()
	}
}

func (s *Store) AdminLibraryByID(ctx context.Context, libraryID string) (store.Library, error) {
	l, err := scanPlainLibrary(s.db.QueryRowContext(ctx, q(
		`SELECT `+plainLibraryColumns+`
		 FROM libraries WHERE id = ?`), libraryID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, store.ErrNotFound
	}
	return l, err
}
