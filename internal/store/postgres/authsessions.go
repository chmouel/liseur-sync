package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateAuthSession(ctx context.Context, a store.AuthSession) error {
	var csrf *string
	if a.CSRFHash != "" {
		csrf = &a.CSRFHash
	}
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO auth_sessions (id, user_id, sha256, kind, csrf_token_sha256, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		a.ID, a.UserID, a.SHA256, a.Kind, csrf, a.CreatedAt.UTC(), a.ExpiresAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) AuthSessionByHash(ctx context.Context, sha256 string) (store.AuthSession, error) {
	var a store.AuthSession
	var csrf *string
	err := s.db.QueryRowContext(ctx, q(
		`SELECT id, user_id, sha256, kind, csrf_token_sha256, created_at, expires_at, revoked_at
		 FROM auth_sessions WHERE sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = auth_sessions.user_id AND u.disabled_at IS NULL)`), sha256).
		Scan(&a.ID, &a.UserID, &a.SHA256, &a.Kind, &csrf, &a.CreatedAt, &a.ExpiresAt, &a.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, store.ErrNotFound
	}
	if csrf != nil {
		a.CSRFHash = *csrf
	}
	return a, err
}

func (s *Store) RevokeAuthSession(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`),
		time.Now().UTC(), userID, id)
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
