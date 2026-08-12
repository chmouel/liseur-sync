package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// SessionsEndedBefore returns sessions that ended before the cutoff,
// oldest first — the input to the rollup job.
func (s *Store) SessionsEndedBefore(ctx context.Context, userID string, before time.Time) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions s WHERE user_id = ? AND ended_at < ? AND source_key IS NULL
		 ORDER BY started_at`), userID, before.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

// ApplyRollups: see the SQLite implementation's doc comment.
func (s *Store) ApplyRollups(ctx context.Context, userID string, rollups []store.SessionRollup, deleteSessions []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	for _, expected := range deleteSessions {
		current, err := scanSession(tx.QueryRowContext(ctx, q(
			`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
			        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
			 FROM sessions WHERE user_id = ? AND session_id = ?`),
			userID, expected.SessionID))
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			return store.ErrConflict
		}
		if store.SessionFingerprint(current) != store.SessionFingerprint(expected) {
			return store.ErrConflict
		}
	}
	for _, r := range rollups {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO session_rollups (user_id, work_id, day, active_seconds, pages, prog_delta, session_count)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (user_id, work_id, day) DO UPDATE SET
			     active_seconds = session_rollups.active_seconds + excluded.active_seconds,
			     pages          = session_rollups.pages + excluded.pages,
			     prog_delta     = session_rollups.prog_delta + excluded.prog_delta,
			     session_count  = session_rollups.session_count + excluded.session_count`),
			userID, r.WorkID, r.Day, r.ActiveSeconds, r.Pages, r.ProgDelta, r.SessionCount); err != nil {
			return err
		}
	}
	for _, ses := range deleteSessions {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO session_tombstones (user_id, session_id, fingerprint) VALUES (?, ?, ?)
			 ON CONFLICT (user_id, session_id) DO NOTHING`),
			userID, ses.SessionID, store.SessionFingerprint(ses)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, q(
			`DELETE FROM sessions WHERE user_id = ? AND session_id = ?`),
			userID, ses.SessionID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrConflict
		}
	}
	return tx.Commit()
}

func (s *Store) RollupsInRange(ctx context.Context, userID, fromDay, toDay string) ([]store.SessionRollup, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count
		 FROM session_rollups WHERE user_id = ? AND day >= ? AND day <= ?
		 ORDER BY day`), userID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SessionRollup
	for rows.Next() {
		var r store.SessionRollup
		if err := rows.Scan(&r.UserID, &r.WorkID, &r.Day, &r.ActiveSeconds, &r.Pages, &r.ProgDelta, &r.SessionCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RollupsForWork(ctx context.Context, userID, workID string) ([]store.SessionRollup, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count
		 FROM session_rollups WHERE user_id = ? AND work_id = ?
		 ORDER BY day`), userID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SessionRollup
	for rows.Next() {
		var r store.SessionRollup
		if err := rows.Scan(&r.UserID, &r.WorkID, &r.Day, &r.ActiveSeconds, &r.Pages, &r.ProgDelta, &r.SessionCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Housekeep deletes expired auth debris.
func (s *Store) Housekeep(ctx context.Context, now time.Time) error {
	now = now.UTC()
	grace := now.Add(-store.TokenPurgeGrace)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM pairing_codes WHERE expires_at < ?`), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM auth_sessions WHERE expires_at < ? OR revoked_at IS NOT NULL`), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM tokens WHERE (expires_at IS NOT NULL AND expires_at < ?)
		                       OR (revoked_at IS NOT NULL AND revoked_at < ?)`), grace, grace); err != nil {
		return err
	}
	return tx.Commit()
}
