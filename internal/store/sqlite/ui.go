package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ListWorks returns all works for a user with their head progression
// and last activity, newest activity first.
func (s *Store) ListWorks(ctx context.Context, userID string) ([]store.WorkSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.user_id, w.title, w.author, w.pending, w.created_at,
		        (SELECT progression FROM ops o WHERE o.user_id = w.user_id AND o.work_id = w.id
		         ORDER BY seq DESC LIMIT 1) AS head_prog,
		        (SELECT MAX(received_at) FROM ops o WHERE o.user_id = w.user_id AND o.work_id = w.id) AS last_active
		 FROM works w WHERE w.user_id = ?
		 ORDER BY last_active DESC NULLS LAST, w.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.WorkSummary
	for rows.Next() {
		var ws store.WorkSummary
		var pending int
		var created string
		var prog sql.NullFloat64
		var last sql.NullString
		if err := rows.Scan(&ws.Work.ID, &ws.Work.UserID, &ws.Work.Title, &ws.Work.Author,
			&pending, &created, &prog, &last); err != nil {
			return nil, err
		}
		ws.Pending = pending != 0
		ws.Work.Pending = ws.Pending
		if ws.Work.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if prog.Valid {
			ws.Progression = &prog.Float64
		}
		if last.Valid {
			t, err := parseTime(last.String)
			if err != nil {
				return nil, err
			}
			ws.LastActive = &t
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUserSettings(ctx context.Context, userID, timezone string, kosyncEnabled, kopluginEnabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET timezone = ?, kosync_enabled = ?, koplugin_enabled = ? WHERE id = ?`,
		nz(timezone, "UTC"), b2i(kosyncEnabled), b2i(kopluginEnabled), userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateInvite(ctx context.Context, inv store.Invite) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (id, code_sha256, created_by, expires_at) VALUES (?, ?, ?, ?)`,
		inv.ID, inv.CodeSHA256, inv.CreatedBy, formatTime(inv.ExpiresAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) ListInvites(ctx context.Context, userID string) ([]store.Invite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, code_sha256, created_by, expires_at, used_by, used_at
		 FROM invites WHERE created_by = ? ORDER BY rowid DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Invite
	for rows.Next() {
		var inv store.Invite
		var expires string
		var usedBy, usedAt sql.NullString
		if err := rows.Scan(&inv.ID, &inv.CodeSHA256, &inv.CreatedBy, &expires, &usedBy, &usedAt); err != nil {
			return nil, err
		}
		var err2 error
		if inv.ExpiresAt, err2 = parseTime(expires); err2 != nil {
			return nil, err2
		}
		if usedBy.Valid {
			inv.UsedBy = &usedBy.String
		}
		if usedAt.Valid {
			t, err := parseTime(usedAt.String)
			if err != nil {
				return nil, err
			}
			inv.UsedAt = &t
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, userID, inviteID string) error {
	// Invites are revoked by expiring them now.
	res, err := s.db.ExecContext(ctx,
		`UPDATE invites SET expires_at = ? WHERE id = ? AND created_by = ? AND used_at IS NULL`,
		formatTime(time.Now()), inviteID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListKosyncDevices(ctx context.Context, userID string) ([]store.KosyncDevice, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, device_slot, key_sha256, label, revoked_at
		 FROM kosync_devices WHERE user_id = ? ORDER BY device_slot`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.KosyncDevice
	for rows.Next() {
		var d store.KosyncDevice
		var revoked sql.NullString
		if err := rows.Scan(&d.UserID, &d.DeviceSlot, &d.KeySHA256, &d.Label, &revoked); err != nil {
			return nil, err
		}
		if revoked.Valid {
			t, err := parseTime(revoked.String)
			if err != nil {
				return nil, err
			}
			d.RevokedAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RevokeKosyncDevice(ctx context.Context, userID, slot string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE kosync_devices SET revoked_at = ? WHERE user_id = ? AND device_slot = ? AND revoked_at IS NULL`,
		formatTime(time.Now()), userID, slot)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListKopluginDevices(ctx context.Context, userID string) ([]store.KopluginDevice, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, token_sha256, label, device_id, created_at, revoked_at
		 FROM koplugin_devices WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.KopluginDevice
	for rows.Next() {
		var d store.KopluginDevice
		var created string
		var revoked sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.TokenSHA256, &d.Label, &d.DeviceID, &created, &revoked); err != nil {
			return nil, err
		}
		var err2 error
		if d.CreatedAt, err2 = parseTime(created); err2 != nil {
			return nil, err2
		}
		if revoked.Valid {
			t, err := parseTime(revoked.String)
			if err != nil {
				return nil, err
			}
			d.RevokedAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RevokeKopluginDevice(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE koplugin_devices SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`,
		formatTime(time.Now()), userID, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return store.ErrNotFound
	}
	return nil
}

var _ = errors.Is

// RedeemInvite atomically marks an invite used if valid (unexpired,
// unused). Single-use enforced under the write lock.
func (s *Store) RedeemInvite(ctx context.Context, codeSHA256 string, at time.Time) (store.Invite, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Invite{}, err
	}
	defer tx.Rollback()
	var inv store.Invite
	var expires string
	var usedBy, usedAt sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT id, code_sha256, created_by, expires_at, used_by, used_at
		 FROM invites WHERE code_sha256 = ?`, codeSHA256).
		Scan(&inv.ID, &inv.CodeSHA256, &inv.CreatedBy, &expires, &usedBy, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return inv, store.ErrNotFound
	}
	if err != nil {
		return inv, err
	}
	if inv.ExpiresAt, err = parseTime(expires); err != nil {
		return inv, err
	}
	if usedAt.Valid || at.After(inv.ExpiresAt) {
		return inv, store.ErrConflict
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE invites SET used_at = ? WHERE code_sha256 = ? AND used_at IS NULL`,
		formatTime(at), codeSHA256)
	if err != nil {
		return inv, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return inv, store.ErrConflict
	}
	return inv, tx.Commit()
}
