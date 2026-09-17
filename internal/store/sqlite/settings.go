package sqlite

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) GetUserSettings(ctx context.Context, userID string) ([]store.UserSetting, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, updated_at FROM user_settings WHERE user_id = ? ORDER BY key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.UserSetting
	for rows.Next() {
		var us store.UserSetting
		var at string
		if err := rows.Scan(&us.Key, &us.Value, &at); err != nil {
			return nil, err
		}
		var err2 error
		if us.UpdatedAt, err2 = parseTime(at); err2 != nil {
			return nil, err2
		}
		out = append(out, us)
	}
	return out, rows.Err()
}

func (s *Store) PutUserSettings(ctx context.Context, userID string, settings []store.UserSetting, maxPerAccount int) error {
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
	if err := checkSettingsQuota(ctx, tx, userID, settings, maxPerAccount); err != nil {
		return err
	}
	for _, us := range settings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_settings (user_id, key, value, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id, key) DO UPDATE
			 SET value = excluded.value, updated_at = excluded.updated_at
			 WHERE excluded.updated_at > user_settings.updated_at`,
			userID, us.Key, us.Value, formatTime(us.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func checkSettingsQuota(ctx context.Context, tx *sql.Tx, userID string, settings []store.UserSetting, maxPerAccount int) error {
	if maxPerAccount < 1 {
		return nil
	}
	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_settings WHERE user_id = ?`, userID).Scan(&existing); err != nil {
		return err
	}
	seen := make(map[string]bool, len(settings))
	added := 0
	for _, us := range settings {
		if seen[us.Key] {
			continue
		}
		seen[us.Key] = true
		var present int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_settings WHERE user_id = ? AND key = ?`,
			userID, us.Key).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			added++
		}
	}
	if existing+added > maxPerAccount {
		return store.ErrQuotaExceeded
	}
	return nil
}
