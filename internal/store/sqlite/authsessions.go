package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateAuthSession(ctx context.Context, a store.AuthSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_sessions (id, user_id, sha256, kind, csrf_token_sha256, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.SHA256, a.Kind, a.CSRFHash,
		formatTime(a.CreatedAt), formatTime(a.ExpiresAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) AuthSessionByHash(ctx context.Context, sha256 string) (store.AuthSession, error) {
	var a store.AuthSession
	var csrf sql.NullString
	var created, expires string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, sha256, kind, csrf_token_sha256, created_at, expires_at, revoked_at
		 FROM auth_sessions WHERE sha256 = ?`, sha256).
		Scan(&a.ID, &a.UserID, &a.SHA256, &a.Kind, &csrf, &created, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return a, store.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.CSRFHash = csrf.String
	if a.CreatedAt, err = parseTime(created); err != nil {
		return a, err
	}
	if a.ExpiresAt, err = parseTime(expires); err != nil {
		return a, err
	}
	if revoked.Valid {
		t, err := parseTime(revoked.String)
		if err != nil {
			return a, err
		}
		a.RevokedAt = &t
	}
	return a, nil
}

func (s *Store) RevokeAuthSession(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`,
		formatTime(time.Now()), userID, id)
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
