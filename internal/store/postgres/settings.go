package postgres

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) GetUserSettings(ctx context.Context, userID string) ([]store.UserSetting, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT key, value, updated_at FROM user_settings WHERE user_id = ? ORDER BY key`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.UserSetting
	for rows.Next() {
		var us store.UserSetting
		if err := rows.Scan(&us.Key, &us.Value, &us.UpdatedAt); err != nil {
			return nil, err
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
	// Take the account's row first, so two requests for the same
	// account cannot both count the settings, both find room, and both
	// commit. READ COMMITTED lets each transaction see the other's
	// rows only after it has already decided, so counting inside a
	// transaction is not by itself enough. Locking here also gives
	// every writer for this account one order to work in, which is what
	// keeps two overlapping multi-key upserts from taking the same rows
	// in opposite orders and deadlocking one of them into a 500.
	if _, err := tx.ExecContext(ctx, q(
		`SELECT 1 FROM users WHERE id = ? FOR UPDATE`), userID); err != nil {
		return err
	}
	// Counting inside the transaction is the only place the answer
	// cannot go stale between the check and the insert. Keys already
	// present are replacements, not growth, so only the genuinely new
	// ones count against the cap.
	if err := checkSettingsQuota(ctx, tx, userID, settings, maxPerAccount); err != nil {
		return err
	}
	for _, us := range settings {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO user_settings (user_id, key, value, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id, key) DO UPDATE
			 SET value = excluded.value, updated_at = excluded.updated_at
			 WHERE excluded.updated_at > user_settings.updated_at`),
			userID, us.Key, us.Value, us.UpdatedAt.UTC()); err != nil {
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
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(*) FROM user_settings WHERE user_id = ?`), userID).Scan(&existing); err != nil {
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
		if err := tx.QueryRowContext(ctx, q(
			`SELECT COUNT(*) FROM user_settings WHERE user_id = ? AND key = ?`),
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
