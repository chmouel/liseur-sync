package sqlite

// The admin panel's library reads and writes (ADR-0013). They skip the
// ACL join that the owner-facing methods in catalog.go carry; the
// authorization that replaces it is the caller's admin flag, checked at
// the handler edge, and the actor's id travels with each write so the
// log line names somebody.

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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+plainLibraryColumns+`
		 FROM libraries
		 WHERE ? = '' OR (name, id) > (?, ?)
		 ORDER BY name, id
		 LIMIT ?`,
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE (l.owner_user_id = ? OR a.role IS NOT NULL)
		   AND (? = '' OR (l.name, l.id) > (?, ?))
		 ORDER BY l.name, l.id
		 LIMIT ?`,
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.library_id, a.user_id, u.name, a.role, a.created_at
		 FROM library_access a
		 JOIN users u ON u.id = a.user_id
		 WHERE a.library_id = ?
		 ORDER BY a.created_at DESC, a.user_id
		 LIMIT ?`,
		libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.LibraryGrant
	for rows.Next() {
		var g store.LibraryGrant
		var created string
		if err := rows.Scan(&g.LibraryID, &g.UserID, &g.UserName, &g.Role, &created); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = parseTime(created)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) AdminSetLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role *store.LibraryRole, at time.Time) error {
	_ = actorUserID
	if role == nil {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM library_access WHERE library_id = ? AND user_id = ?`,
			libraryID, userID)
		return affectedOne(res, err)
	}
	if err := checkLibraryRole(*role); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO library_access (library_id, user_id, role, created_at)
		 SELECT l.id, ?, ?, ?
		 FROM libraries l
		 WHERE l.id = ? AND l.owner_user_id <> ?
		 ON CONFLICT(library_id, user_id) DO UPDATE SET role = excluded.role`,
		userID, string(*role), formatTime(at), libraryID, userID)
	return affectedOne(res, err)
}

func (s *Store) AdminSetLibraryConfig(ctx context.Context, actorUserID, libraryID string, configJSON []byte, at time.Time) error {
	_ = actorUserID
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET config_json = ?, updated_at = ? WHERE id = ?`,
		configJSON, formatTime(at), libraryID)
	return affectedOne(res, err)
}

// affectedOne turns "the WHERE clause matched nothing" into ErrNotFound,
// which for these writes covers both a library that does not exist and
// a grant aimed at its owner.
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
	var lastRefresh, lastAttempt, requested sql.NullString
	var created, updated string
	var refreshSeconds int64
	if err := row.Scan(&l.ID, &l.OwnerUserID, &l.QuotaUserID,
		&l.Source, &l.Storage, &l.Refresh, &refreshSeconds, &l.Name,
		&root, &l.ConfigJSON, &created, &updated,
		&lastRefresh, &lastAttempt, &refreshError, &requested); err != nil {
		return l, err
	}
	if root.Valid {
		l.RootPath = &root.String
	}
	if refreshError.Valid {
		l.LastRefreshError = &refreshError.String
	}
	l.RefreshInterval = store.RefreshIntervalFrom(refreshSeconds)
	var err error
	if l.CreatedAt, err = parseTime(created); err != nil {
		return l, err
	}
	if l.UpdatedAt, err = parseTime(updated); err != nil {
		return l, err
	}
	if l.LastRefreshAt, err = parseTimePtr(lastRefresh); err != nil {
		return l, err
	}
	if l.LastRefreshAttemptAt, err = parseTimePtr(lastAttempt); err != nil {
		return l, err
	}
	if l.RefreshRequestedAt, err = parseTimePtr(requested); err != nil {
		return l, err
	}
	return l, nil
}

func (s *Store) AdminLibraryByID(ctx context.Context, libraryID string) (store.Library, error) {
	l, err := scanPlainLibrary(s.db.QueryRowContext(ctx,
		`SELECT `+plainLibraryColumns+`
		 FROM libraries WHERE id = ?`, libraryID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, store.ErrNotFound
	}
	return l, err
}
