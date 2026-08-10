package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreatePairingCode(ctx context.Context, p store.PairingCode) error {
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO pairing_codes (id, user_id, code_sha256, expires_at) VALUES (?, ?, ?, ?)`),
		p.ID, p.UserID, p.CodeSHA256, p.ExpiresAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) RedeemPairingCode(ctx context.Context, codeSHA256 string, at time.Time) (store.PairingCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.PairingCode{}, err
	}
	defer tx.Rollback()
	var p store.PairingCode
	err = tx.QueryRowContext(ctx, q(
		`SELECT id, user_id, code_sha256, expires_at, used_at FROM pairing_codes WHERE code_sha256 = ?`),
		codeSHA256).Scan(&p.ID, &p.UserID, &p.CodeSHA256, &p.ExpiresAt, &p.UsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, store.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if p.UsedAt != nil || at.After(p.ExpiresAt) {
		return p, store.ErrConflict
	}
	res, err := tx.ExecContext(ctx, q(
		`UPDATE pairing_codes SET used_at = ? WHERE code_sha256 = ? AND used_at IS NULL`),
		at.UTC(), codeSHA256)
	if err != nil {
		return p, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return p, store.ErrConflict
	}
	return p, tx.Commit()
}

func (s *Store) CreateKosyncDevice(ctx context.Context, d store.KosyncDevice) error {
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO kosync_devices (user_id, device_slot, key_sha256, label) VALUES (?, ?, ?, ?)`),
		d.UserID, d.DeviceSlot, d.KeySHA256, d.Label)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) KosyncDeviceByKey(ctx context.Context, keySHA256 string) (store.KosyncDevice, error) {
	var d store.KosyncDevice
	err := s.db.QueryRowContext(ctx, q(
		`SELECT user_id, device_slot, key_sha256, label, revoked_at
		 FROM kosync_devices WHERE key_sha256 = ?`), keySHA256).
		Scan(&d.UserID, &d.DeviceSlot, &d.KeySHA256, &d.Label, &d.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, store.ErrNotFound
	}
	return d, err
}

func (s *Store) CreatePendingWork(ctx context.Context, userID, partialMD5 string) (string, bool, error) {
	if wid, err := s.WorkIDByAlias(ctx, userID, "partial-md5", partialMD5); err == nil {
		return wid, false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", false, err
	}
	workID := hex.EncodeToString(b)
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, '', '', TRUE, ?)`),
		workID, userID, time.Now().UTC()); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, 'partial-md5', ?, ?)`),
		userID, partialMD5, workID); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO seq_counters (user_id, next_seq) VALUES (?, 1) ON CONFLICT DO NOTHING`), userID); err != nil {
		return "", false, err
	}
	return workID, true, tx.Commit()
}

func (s *Store) WorkIDByAlias(ctx context.Context, userID, kind, value string) (string, error) {
	var wid string
	err := s.db.QueryRowContext(ctx, q(
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`),
		userID, kind, value).Scan(&wid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return wid, err
}
