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
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.Argon2Hash, nz(u.Timezone, "UTC"),
		b2i(u.KosyncEnabled), b2i(u.KopluginEnabled), formatTime(u.CreatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) UserByName(ctx context.Context, name string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at
		 FROM users WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, userID string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at
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
		 FROM tokens WHERE sha256 = ?`, sha256))
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
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at
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
