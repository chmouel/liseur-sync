package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The helpers below let the shared suite inspect quota and orphan state,
// which have no read method on the Store interface: they are internal
// bookkeeping, and exposing them for real would invite a caller to make
// decisions on them outside a transaction.

func (s *Store) ReservedBytesForTest(
	ctx context.Context, userID, sha256 string,
) (int64, bool, error) {
	var bytes int64
	err := s.db.QueryRowContext(ctx,
		`SELECT bytes FROM blob_reservations
		 WHERE quota_user_id = ? AND blob_sha256 = ?`,
		userID, sha256).Scan(&bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return bytes, true, nil
}

func (s *Store) BlobOrphanedForTest(
	ctx context.Context, sha256 string,
) (bool, error) {
	var orphaned sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT orphaned_at FROM blobs WHERE sha256 = ?`, sha256).Scan(&orphaned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return orphaned.Valid, nil
}

func (s *Store) ReserveForTest(
	ctx context.Context, userID string, blob store.BlobInfo,
) error {
	at := formatTime(time.Now().UTC())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (sha256, size_bytes, created_at)
		 VALUES (?, ?, ?) ON CONFLICT (sha256) DO NOTHING`,
		blob.SHA256, blob.SizeBytes, at); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO blob_reservations
		 (quota_user_id, blob_sha256, bytes, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (quota_user_id, blob_sha256) DO NOTHING`,
		userID, blob.SHA256, blob.SizeBytes, at)
	return err
}
