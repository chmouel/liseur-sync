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
	last_refresh_attempt_at, last_refresh_code, refresh_requested_at,
	last_inventory_digest, refresh_lease_owner, refresh_lease_until`

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
	var root, refreshCode, digest, leaseOwner sql.NullString
	var lastRefresh, lastAttempt, requested, leaseUntil sql.NullString
	var created, updated string
	var refreshSeconds int64
	if err := row.Scan(&l.ID, &l.OwnerUserID, &l.QuotaUserID,
		&l.Source, &l.Storage, &l.Refresh, &refreshSeconds, &l.Name,
		&root, &l.ConfigJSON, &created, &updated,
		&lastRefresh, &lastAttempt, &refreshCode, &requested,
		&digest, &leaseOwner, &leaseUntil); err != nil {
		return l, err
	}
	if root.Valid {
		l.RootPath = &root.String
	}
	l.LastRefreshCode = store.RefreshCode(refreshCode.String)
	l.LastInventoryDigest = digest.String
	l.RefreshLeaseOwner = leaseOwner.String
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
	if l.RefreshLeaseUntil, err = parseTimePtr(leaseUntil); err != nil {
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

// AdminDeleteLibrary removes a library, or trashes its books as the
// first step towards it. See the interface for why the two cases differ.
func (s *Store) AdminDeleteLibrary(
	ctx context.Context,
	actorUserID, libraryID string,
	at, trashExpiresAt time.Time,
) (store.LibraryDeletion, error) {
	_ = actorUserID
	var result store.LibraryDeletion
	if at.IsZero() || trashExpiresAt.IsZero() || !trashExpiresAt.After(at) {
		return result, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	var storage string
	if err := tx.QueryRowContext(ctx,
		`SELECT storage FROM libraries WHERE id = ?`,
		libraryID).Scan(&storage); errors.Is(err, sql.ErrNoRows) {
		return result, store.ErrNotFound
	} else if err != nil {
		return result, err
	}

	live, err := libraryBookIDsTx(ctx, tx, libraryID, false)
	if err != nil {
		return result, err
	}
	if store.LibraryStorage(storage) != store.LibraryStorageInPlace &&
		len(live) > 0 {
		// The server holds the only copy of these. They go to the trash
		// on the ordinary window, where the reader can see them and put
		// them back; the library stays until somebody says again that
		// they meant it.
		if _, err := tx.ExecContext(ctx,
			`UPDATE books
			 SET status = 'trashed', trashed_at = ?, trash_expires_at = ?,
			     updated_at = ?
			 WHERE library_id = ? AND status != 'trashed'`,
			formatTime(at.UTC()), formatTime(trashExpiresAt.UTC()),
			formatTime(at.UTC()), libraryID); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		return store.LibraryDeletion{
			Trashed:        len(live),
			TrashExpiresAt: trashExpiresAt.UTC(),
		}, nil
	}

	all, err := libraryBookIDsTx(ctx, tx, libraryID, true)
	if err != nil {
		return result, err
	}
	if len(all) > 0 {
		// Purging through the trash purge's own routine is what releases
		// the quota and orphan-marks the blobs. Dropping the library row
		// and letting the cascade take the books would leave a principal
		// charged for bytes nothing references.
		purged, err := purgeBooksTx(ctx, tx, all, at.UTC())
		if err != nil {
			return result, err
		}
		purged.BookIDs = all
		result.Purged = purged
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM libraries WHERE id = ?`, libraryID); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return store.LibraryDeletion{}, err
	}
	result.Removed = true
	return result, nil
}

// libraryBookIDsTx lists a library's book ids, either every one of them
// or only those outside the trash.
func libraryBookIDsTx(
	ctx context.Context, tx *sql.Tx, libraryID string, includeTrashed bool,
) ([]string, error) {
	query := `SELECT id FROM books WHERE library_id = ? AND status != 'trashed'
	          ORDER BY id`
	if includeTrashed {
		query = `SELECT id FROM books WHERE library_id = ? ORDER BY id`
	}
	rows, err := tx.QueryContext(ctx, query, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
