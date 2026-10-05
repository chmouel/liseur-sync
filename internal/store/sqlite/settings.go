package sqlite

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) GetDeviceSettings(ctx context.Context, userID, deviceID string) ([]store.DeviceSetting, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, updated_at FROM device_settings
		 WHERE user_id = ? AND device_id = ? ORDER BY key`, userID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.DeviceSetting
	for rows.Next() {
		var ds store.DeviceSetting
		var at string
		if err := rows.Scan(&ds.Key, &ds.Value, &at); err != nil {
			return nil, err
		}
		var err2 error
		if ds.UpdatedAt, err2 = parseTime(at); err2 != nil {
			return nil, err2
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
	// Counting inside the transaction is the only place the answer
	// cannot go stale between the check and the insert. Keys already
	// present are replacements, not growth, so only the genuinely new
	// ones count against the cap.
	if err := checkSettingsQuota(ctx, tx, userID, deviceID, settings, maxPerDevice); err != nil {
		return err
	}
	for _, ds := range settings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO device_settings (user_id, device_id, key, value, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, device_id, key) DO UPDATE
			 SET value = excluded.value, updated_at = excluded.updated_at
			 WHERE excluded.updated_at > device_settings.updated_at`,
			userID, deviceID, ds.Key, ds.Value, formatTime(ds.UpdatedAt)); err != nil {
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
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM device_settings WHERE user_id = ? AND device_id = ?`,
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
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM device_settings WHERE user_id = ? AND device_id = ? AND key = ?`,
			userID, deviceID, ds.Key).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			added++
		}
	}
	if existing+added > maxPerDevice {
		return store.ErrQuotaExceeded
	}
	return nil
}
