package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func randRead(b []byte) (int, error) { return rand.Read(b) }
func hexEnc(b []byte) string         { return hex.EncodeToString(b) }

func (s *Store) CreatePairingCode(ctx context.Context, p store.PairingCode) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pairing_codes (id, user_id, code_sha256, expires_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.UserID, p.CodeSHA256, formatTime(p.ExpiresAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

// RedeemPairingCode atomically marks a code used if valid: correct
// hash, unexpired, unused. Single-use is enforced by the UPDATE's
// WHERE clause under the write lock.
func (s *Store) RedeemPairingCode(ctx context.Context, codeSHA256 string, at time.Time) (store.PairingCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.PairingCode{}, err
	}
	defer tx.Rollback()
	var p store.PairingCode
	var expires string
	var used sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, code_sha256, expires_at, used_at FROM pairing_codes
		 WHERE code_sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = pairing_codes.user_id AND u.disabled_at IS NULL)`,
		codeSHA256).Scan(&p.ID, &p.UserID, &p.CodeSHA256, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return p, store.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if p.ExpiresAt, err = parseTime(expires); err != nil {
		return p, err
	}
	if used.Valid || at.After(p.ExpiresAt) {
		return p, store.ErrConflict // used or expired
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE pairing_codes SET used_at = ? WHERE code_sha256 = ? AND used_at IS NULL`,
		formatTime(at), codeSHA256)
	if err != nil {
		return p, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return p, store.ErrConflict
	}
	return p, tx.Commit()
}

func (s *Store) CreateKosyncDevice(ctx context.Context, d store.KosyncDevice) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kosync_devices (user_id, device_slot, key_sha256, label) VALUES (?, ?, ?, ?)`,
		d.UserID, d.DeviceSlot, d.KeySHA256, d.Label)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) KosyncDeviceByKey(ctx context.Context, keySHA256 string) (store.KosyncDevice, error) {
	var d store.KosyncDevice
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, device_slot, key_sha256, label, revoked_at
		 FROM kosync_devices WHERE key_sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = kosync_devices.user_id AND u.disabled_at IS NULL)`, keySHA256).
		Scan(&d.UserID, &d.DeviceSlot, &d.KeySHA256, &d.Label, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return d, store.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if revoked.Valid {
		t, err := parseTime(revoked.String)
		if err != nil {
			return d, err
		}
		d.RevokedAt = &t
	}
	return d, nil
}

// CreatePendingWork creates a pending work keyed on a partial-md5
// alias, or returns the existing one. Kosync traffic referencing an
// unknown digest lands here so a KOReader-first book is not lost.
func (s *Store) CreatePendingWork(ctx context.Context, userID, partialMD5 string) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", false, err
	}
	workID, created, err := createPendingWorkTx(ctx, tx, userID, partialMD5)
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return workID, created, nil
}

func createPendingWorkTx(ctx context.Context, tx *sql.Tx, userID, partialMD5 string) (string, bool, error) {
	var existing string
	err := tx.QueryRowContext(ctx,
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = 'partial-md5' AND value = ?`,
		userID, partialMD5).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	var workID string
	{
		b := make([]byte, 16)
		if _, err := randRead(b); err != nil {
			return "", false, err
		}
		workID = hexEnc(b)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, '', '', 1, ?)`,
		workID, userID, formatTime(time.Now())); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, 'partial-md5', ?, ?)`,
		userID, partialMD5, workID); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO seq_counters (user_id, next_seq) VALUES (?, 1)`, userID); err != nil {
		return "", false, err
	}
	return workID, true, nil
}

func (s *Store) WorkIDByAlias(ctx context.Context, userID, kind, value string) (string, error) {
	var wid string
	err := s.db.QueryRowContext(ctx,
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`,
		userID, kind, value).Scan(&wid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return wid, err
}
