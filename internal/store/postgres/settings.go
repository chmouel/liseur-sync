package postgres

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) GetDeviceSettings(ctx context.Context, userID, deviceID string) ([]store.DeviceSetting, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT key, value, updated_at FROM device_settings
		 WHERE user_id = ? AND device_id = ? ORDER BY key`), userID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.DeviceSetting
	for rows.Next() {
		var ds store.DeviceSetting
		if err := rows.Scan(&ds.Key, &ds.Value, &ds.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ds)
	}
	return out, rows.Err()
}

func (s *Store) PutDeviceSettings(ctx context.Context, userID, deviceID string, settings []store.DeviceSetting, maxPerDevice int) error {
	if len(settings) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Take the account's row first, so two requests for the same
	// device cannot both count the settings, both find room, and both
	// commit. READ COMMITTED lets each transaction see the other's
	// rows only after it has already decided, so counting inside a
	// transaction is not by itself enough. Locking here also gives
	// every writer one order to work in, which is what keeps two
	// overlapping multi-key upserts from taking the same rows in
	// opposite orders and deadlocking one of them into a 500.
	if _, err := tx.ExecContext(ctx, q(
		`SELECT 1 FROM users WHERE id = ? FOR UPDATE`), userID); err != nil {
		return err
	}
	// Counting inside the transaction is the only place the answer
	// cannot go stale between the check and the insert. Keys already
	// present are replacements, not growth, so only the genuinely new
	// ones count against the cap.
	if err := checkSettingsQuota(ctx, tx, userID, deviceID, settings, maxPerDevice); err != nil {
		return err
	}
	for _, ds := range settings {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO device_settings (user_id, device_id, key, value, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, device_id, key) DO UPDATE
			 SET value = excluded.value, updated_at = excluded.updated_at
			 WHERE excluded.updated_at > device_settings.updated_at`),
			userID, deviceID, ds.Key, ds.Value, ds.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func checkSettingsQuota(ctx context.Context, tx *sql.Tx, userID, deviceID string, settings []store.DeviceSetting, maxPerDevice int) error {
	if maxPerDevice < 1 {
		return nil
	}
	var existing int
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(*) FROM device_settings WHERE user_id = ? AND device_id = ?`),
		userID, deviceID).Scan(&existing); err != nil {
		return err
	}
	seen := make(map[string]bool, len(settings))
	added := 0
	for _, ds := range settings {
		if seen[ds.Key] {
			continue
		}
		seen[ds.Key] = true
		var present int
		if err := tx.QueryRowContext(ctx, q(
			`SELECT COUNT(*) FROM device_settings WHERE user_id = ? AND device_id = ? AND key = ?`),
			userID, deviceID, ds.Key).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			added++
		}
	}
	// Only a request that adds a key can be over the limit. A device
	// already above it — because an operator lowered the cap — must
	// still be able to replace what it has, or every key it holds is
	// frozen with no delete route to get back under.
	if added > 0 && existing+added > maxPerDevice {
		return store.ErrQuotaExceeded
	}
	return nil
}
