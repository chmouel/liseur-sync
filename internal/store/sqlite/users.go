package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateUser(ctx context.Context, u store.User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		                    is_admin, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.Argon2Hash, nz(u.Timezone, "UTC"),
		b2i(u.KosyncEnabled), b2i(u.KopluginEnabled), b2i(u.IsAdmin),
		formatTimePtr(u.DisabledAt), formatTime(u.CreatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

// SetUserAdmin moves the admin flag under the instance-wide role lock.
// Creating the first admin needs no guard; clearing one does, and the
// guard has to see a consistent count, which is why the whole thing is
// one transaction rather than a check followed by a write.
func (s *Store) SetUserAdmin(ctx context.Context, userID string, admin bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin int
	if err := tx.QueryRowContext(ctx,
		`SELECT is_admin FROM users WHERE id = ?`, userID).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return err
	}
	if (isAdmin != 0) == admin {
		return tx.Commit() // already there; nothing to guard against
	}
	if !admin {
		var others int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users
			 WHERE is_admin = 1 AND disabled_at IS NULL AND id <> ?`,
			userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return store.ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET is_admin = ? WHERE id = ?`, b2i(admin), userID); err != nil {
		return err
	}
	if !admin {
		// ScopeAdmin implies every other scope, so a surviving
		// admin-scoped token would keep the authority the role just
		// lost.
		if _, err := tx.ExecContext(ctx,
			`UPDATE tokens SET revoked_at = ?
			 WHERE user_id = ? AND revoked_at IS NULL AND id IN (
			     SELECT token_id FROM token_scopes
			     WHERE user_id = ? AND scope = ?)`,
			formatTime(time.Now().UTC()), userID, userID,
			string(store.ScopeAdmin)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// lockAdminRole serializes every transition that reads the number of
// enabled admins and then writes. SQLite has one writer, so taking the
// write lock immediately is enough; the PostgreSQL backend takes an
// advisory lock, where a bare conditional UPDATE under READ COMMITTED
// would let two transactions demote the last two admins at once.
func lockAdminRole(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET id = id WHERE id IS NULL`)
	return err
}

// requireAdminAccount refuses an admin-scoped token to an account that
// is not an enabled admin, inside the caller's transaction. A check
// outside it would pass, then land a dormant admin token after a
// concurrent demotion had already revoked the ones that existed.
func requireAdminAccount(ctx context.Context, tx *sql.Tx, userID string, scopes store.ScopeSet) error {
	if !scopes.Contains(store.ScopeAdmin) {
		return nil
	}
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin int
	var disabled sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT is_admin, disabled_at FROM users WHERE id = ?`, userID).
		Scan(&isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin == 0 || disabled.Valid {
		return store.ErrAdminGrantRequiresAdmin
	}
	return nil
}

func (s *Store) UserByName(ctx context.Context, name string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, userID string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) CreateToken(ctx context.Context, t store.Token) error {
	scopes, err := store.NormalizeScopes(t.Scopes)
	if err != nil {
		return err
	}
	legacy, _ := scopes.Legacy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminAccount(ctx, tx, t.UserID, scopes); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.DeviceID, t.Name, string(legacy), t.SHA256,
		formatTime(t.CreatedAt), formatTimePtr(t.ExpiresAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO token_scopes (token_id, user_id, scope) VALUES (?, ?, ?)`,
			t.ID, t.UserID, string(scope)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) TokenByHash(ctx context.Context, userID, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, device_id, name, sha256, created_at, expires_at, last_used, revoked_at
		 FROM tokens WHERE user_id = ? AND sha256 = ?`, userID, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return t, store.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	err = s.loadTokenScopes(ctx, &t)
	return t, err
}

// TokenByHashGlobal looks up a token by hash without knowing the user
// (bearer middleware path). Hash is globally unique.
func (s *Store) TokenByHashGlobal(ctx context.Context, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, device_id, name, sha256, created_at, expires_at, last_used, revoked_at
		 FROM tokens WHERE sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = tokens.user_id AND u.disabled_at IS NULL)`, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return t, store.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	err = s.loadTokenScopes(ctx, &t)
	return t, err
}

func (s *Store) ListTokens(ctx context.Context, userID string) ([]store.Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, device_id, name, sha256, created_at, expires_at, last_used, revoked_at
		 FROM tokens WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadTokenScopes(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) loadTokenScopes(ctx context.Context, t *store.Token) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT scope FROM token_scopes WHERE user_id = ? AND token_id = ?`,
		t.UserID, t.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var scopes []store.Scope
	for rows.Next() {
		var scope store.Scope
		if err := rows.Scan(&scope); err != nil {
			return err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	t.Scopes, err = store.NormalizeScopes(scopes)
	return err
}

func (s *Store) UpdateTokenScopes(ctx context.Context, userID, tokenID string, requested store.ScopeSet) error {
	scopes, err := store.NormalizeScopes(requested)
	if err != nil {
		return err
	}
	legacy, _ := scopes.Legacy()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminAccount(ctx, tx, userID, scopes); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE tokens SET scope = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`,
		string(legacy), userID, tokenID)
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
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM token_scopes WHERE user_id = ? AND token_id = ?`,
		userID, tokenID); err != nil {
		return err
	}
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO token_scopes (token_id, user_id, scope) VALUES (?, ?, ?)`,
			tokenID, userID, string(scope)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RevokeToken(ctx context.Context, userID, tokenID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`,
		formatTime(time.Now()), userID, tokenID)
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

func (s *Store) DeleteToken(ctx context.Context, userID, tokenID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tokens WHERE user_id = ? AND id = ?`, userID, tokenID)
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

func (s *Store) TouchToken(ctx context.Context, userID, tokenID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tokens SET last_used = ? WHERE user_id = ? AND id = ?`,
		formatTime(at), userID, tokenID)
	return err
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UserIDs lists all user IDs (background jobs).
func (s *Store) UserIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users`)
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

// ListUsers returns all users (admin UI).
func (s *Store) ListUsers(ctx context.Context) ([]store.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListUsersPage returns users after afterName, in name order, at most
// limit of them. The caller asks for one more than it shows and reads
// the extra row as "there is another page".
func (s *Store) ListUsersPage(ctx context.Context, afterName string, limit int) ([]store.User, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE name > ? ORDER BY name LIMIT ?`, afterName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateFirstAdmin makes the instance's very first account, already an
// administrator, and refuses once any account exists. The emptiness
// check runs inside the locked transaction that inserts, because the
// window between "nobody is here" and "I am here" is exactly what an
// unauthenticated setup page must not have.
func (s *Store) CreateFirstAdmin(ctx context.Context, u store.User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	if err != nil {
		return err
	}
	if exists != 0 {
		return store.ErrConflict
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		                    is_admin, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1, NULL, ?)`,
		u.ID, u.Name, u.Argon2Hash, nz(u.Timezone, "UTC"),
		b2i(u.KosyncEnabled), b2i(u.KopluginEnabled), formatTime(u.CreatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserDisabled stops or restarts an account. Disabling takes the
// same lock as a demotion, for the same reason: the guard reads how
// many enabled admins there are and then writes.
func (s *Store) SetUserDisabled(ctx context.Context, userID string, disabled bool, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin int
	var disabledAt sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT is_admin, disabled_at FROM users WHERE id = ?`, userID).
		Scan(&isAdmin, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if disabledAt.Valid == disabled {
		return tx.Commit() // already there
	}
	if disabled && isAdmin != 0 {
		var others int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users
			 WHERE is_admin = 1 AND disabled_at IS NULL AND id <> ?`,
			userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return store.ErrLastAdmin
		}
	}
	if disabled {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET disabled_at = ? WHERE id = ?`,
			formatTime(at.UTC()), userID); err != nil {
			return err
		}
		// An open tab stops working now rather than at cookie expiry.
		if _, err := tx.ExecContext(ctx,
			`UPDATE auth_sessions SET revoked_at = ?
			 WHERE user_id = ? AND revoked_at IS NULL`,
			formatTime(at.UTC()), userID); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx,
		`UPDATE users SET disabled_at = NULL WHERE id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
