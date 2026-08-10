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
		&u.KosyncEnabled, &u.KopluginEnabled, &u.CreatedAt)
	return u, err
}

func scanToken(row interface{ Scan(...any) error }) (store.Token, error) {
	var t store.Token
	var scope string
	err := row.Scan(&t.ID, &t.UserID, &t.DeviceID, &t.Name, &scope,
		&t.SHA256, &t.CreatedAt, &t.ExpiresAt, &t.LastUsed, &t.RevokedAt)
	t.Scope = store.Scope(scope)
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
		`INSERT INTO users (id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		u.ID, u.Name, u.Argon2Hash, tz, u.KosyncEnabled, u.KopluginEnabled, u.CreatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) UserByName(ctx context.Context, name string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, q(
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at
		 FROM users WHERE name = ?`), name))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, userID string) (store.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, q(
		`SELECT id, name, argon2_hash, timezone, kosync_enabled, koplugin_enabled, created_at
		 FROM users WHERE id = ?`), userID))
	if errors.Is(err, sql.ErrNoRows) {
		return u, store.ErrNotFound
	}
	return u, err
}

func (s *Store) CreateToken(ctx context.Context, t store.Token) error {
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		t.ID, t.UserID, t.DeviceID, t.Name, string(t.Scope), t.SHA256,
		t.CreatedAt.UTC(), t.ExpiresAt)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

const tokenCols = `id, user_id, device_id, name, scope, sha256, created_at, expires_at, last_used, revoked_at`

func (s *Store) TokenByHash(ctx context.Context, userID, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, q(
		`SELECT `+tokenCols+` FROM tokens WHERE user_id = ? AND sha256 = ?`), userID, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return t, store.ErrNotFound
	}
	return t, err
}

func (s *Store) TokenByHashGlobal(ctx context.Context, sha256 string) (store.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, q(
		`SELECT `+tokenCols+` FROM tokens WHERE sha256 = ?`), sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return t, store.ErrNotFound
	}
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
	return out, rows.Err()
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
