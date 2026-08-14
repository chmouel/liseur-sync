package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// scanUser scans one users row (PG: real bools and timestamptz).
func scanUser(row interface{ Scan(...any) error }) (store.User, error) {
	var u store.User
	err := row.Scan(&u.ID, &u.Name, &u.Argon2Hash, &u.Timezone,
		&u.KosyncEnabled, &u.KopluginEnabled, &u.IsAdmin, &u.DisabledAt,
		&u.CreatedAt)
	return u, err
}

func scanToken(row interface{ Scan(...any) error }) (store.Token, error) {
	var t store.Token
	err := row.Scan(&t.ID, &t.UserID, &t.DeviceID, &t.Name,
		&t.SHA256, &t.CreatedAt, &t.ExpiresAt, &t.LastUsed, &t.RevokedAt)
	return t, err
}

func scanOp(row interface{ Scan(...any) error }) (store.Op, error) {
	var o store.Op
	var origin string
	err := row.Scan(&o.UserID, &o.Seq, &o.OpID, &o.WorkID, &o.EditionSHA,
		&o.DeviceID, &o.ClientTS, &o.Progression, &o.LocatorJSON,
		&o.ForeignPos, &origin, &o.OriginAlias, &o.ReceivedAt)
	o.Origin = store.Origin(origin)
	return o, err
}

func (s *Store) CreateUser(ctx context.Context, u store.User) error {
	tz := u.Timezone
	if tz == "" {
		tz = "UTC"
	}
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		                    is_admin, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		u.ID, u.Name, u.Argon2Hash, tz, u.KosyncEnabled, u.KopluginEnabled,
		u.IsAdmin, u.DisabledAt, u.CreatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) UserByName(ctx context.Context, name string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, q(
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE name = ?`), name))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, userID string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, q(
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE id = ?`), userID))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

// adminRoleLockKey is the advisory-lock key every role and disable
// transition takes. Under READ COMMITTED a conditional UPDATE is not
// enough: two transactions demoting different administrators each see
// the other still in place, both commit, and the instance is left with
// none. The lock is transaction-scoped, so it is released by COMMIT or
// ROLLBACK without any unlock call to forget.
const adminRoleLockKey = 0x1132ad // "liseur admin role"

func lockAdminRole(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminRoleLockKey)
	return err
}

// SetUserAdmin moves the admin flag: guard, write, and revocation of
// the demoted account's admin-scoped tokens, in one locked transaction.
func (s *Store) SetUserAdmin(ctx context.Context, userID string, admin bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin bool
	if err := tx.QueryRowContext(ctx, q(
		`SELECT is_admin FROM users WHERE id = ?`), userID).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return err
	}
	if isAdmin == admin {
		return tx.Commit()
	}
	if !admin {
		var others int
		if err := tx.QueryRowContext(ctx, q(
			`SELECT count(*) FROM users
			 WHERE is_admin AND disabled_at IS NULL AND id <> ?`),
			userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return store.ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE users SET is_admin = ? WHERE id = ?`), admin, userID); err != nil {
		return err
	}
	if !admin {
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE tokens SET revoked_at = ?
			 WHERE user_id = ? AND revoked_at IS NULL AND id IN (
			     SELECT token_id FROM token_scopes
			     WHERE user_id = ? AND scope = ?)`),
			time.Now().UTC(), userID, userID, string(store.ScopeAdmin)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// requireAdminAccount refuses an admin-scoped token to an account that
// is not an enabled admin, inside the caller's transaction and under
// the same lock a demotion takes.
func requireAdminAccount(ctx context.Context, tx *sql.Tx, userID string, scopes store.ScopeSet) error {
	if !scopes.Contains(store.ScopeAdmin) {
		return nil
	}
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin bool
	var disabled *time.Time
	err := tx.QueryRowContext(ctx, q(
		`SELECT is_admin, disabled_at FROM users WHERE id = ?`), userID).
		Scan(&isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !isAdmin || disabled != nil {
		return store.ErrAdminGrantRequiresAdmin
	}
	return nil
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
	_, err = tx.ExecContext(ctx, q(
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		t.ID, t.UserID, t.DeviceID, t.Name, string(legacy), t.SHA256,
		t.CreatedAt.UTC(), t.ExpiresAt)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO token_scopes (token_id, user_id, scope) VALUES (?, ?, ?)
			 ON CONFLICT (token_id, scope) DO NOTHING`),
			t.ID, t.UserID, string(scope)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const tokenCols = `id, user_id, device_id, name, sha256, created_at, expires_at, last_used, revoked_at`

func (s *Store) TokenByHash(ctx context.Context, userID, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, q(
		`SELECT `+tokenCols+` FROM tokens WHERE user_id = ? AND sha256 = ?`), userID, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return t, store.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	err = s.loadTokenScopes(ctx, &t)
	return t, err
}

func (s *Store) TokenByHashGlobal(ctx context.Context, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, q(
		`SELECT `+tokenCols+` FROM tokens WHERE sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = tokens.user_id AND u.disabled_at IS NULL)`), sha256))
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
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+tokenCols+` FROM tokens WHERE user_id = ? ORDER BY created_at`), userID)
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
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT scope FROM token_scopes WHERE user_id = ? AND token_id = ?`),
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
	res, err := tx.ExecContext(ctx, q(
		`UPDATE tokens SET scope = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`),
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
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM token_scopes WHERE user_id = ? AND token_id = ?`),
		userID, tokenID); err != nil {
		return err
	}
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO token_scopes (token_id, user_id, scope) VALUES (?, ?, ?)`),
			tokenID, userID, string(scope)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RevokeToken(ctx context.Context, userID, tokenID string) error {
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE tokens SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`),
		time.Now().UTC(), userID, tokenID)
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
	_, err := s.db.ExecContext(ctx, q(
		`UPDATE tokens SET last_used = ? WHERE user_id = ? AND id = ?`), at.UTC(), userID, tokenID)
	return err
}

var _ = strings.TrimSpace

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
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		        is_admin, disabled_at, created_at
		 FROM users WHERE name > ? ORDER BY name LIMIT ?`), afterName, limit)
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
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return store.ErrConflict
	}
	_, err = tx.ExecContext(ctx, q(
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled,
		                    is_admin, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE, NULL, ?)`),
		u.ID, u.Name, u.Argon2Hash, tzOrUTC(u.Timezone),
		u.KosyncEnabled, u.KopluginEnabled, u.CreatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// tzOrUTC is the one place a missing timezone becomes UTC on this
// backend, so an account created by setup and one created by the CLI
// agree.
func tzOrUTC(tz string) string {
	if tz == "" {
		return "UTC"
	}
	return tz
}

// SetUserDisabled: see the SQLite implementation's doc comment.
func (s *Store) SetUserDisabled(ctx context.Context, userID string, disabled bool, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdminRole(ctx, tx); err != nil {
		return err
	}
	var isAdmin bool
	var disabledAt *time.Time
	err = tx.QueryRowContext(ctx, q(
		`SELECT is_admin, disabled_at FROM users WHERE id = ?`), userID).
		Scan(&isAdmin, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if (disabledAt != nil) == disabled {
		return tx.Commit() // already there
	}
	if disabled && isAdmin {
		var others int
		if err := tx.QueryRowContext(ctx, q(
			`SELECT count(*) FROM users
			 WHERE is_admin AND disabled_at IS NULL AND id <> ?`),
			userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return store.ErrLastAdmin
		}
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE users SET disabled_at = ? WHERE id = ?`), at.UTC(), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE auth_sessions SET revoked_at = ?
			 WHERE user_id = ? AND revoked_at IS NULL`), at.UTC(), userID); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, q(
		`UPDATE users SET disabled_at = NULL WHERE id = ?`), userID); err != nil {
		return err
	}
	return tx.Commit()
}
