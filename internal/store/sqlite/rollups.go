package sqlite

import (
	"context"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// SessionsEndedBefore returns sessions that ended before the cutoff,
// oldest first — the input to the rollup job.
func (s *Store) SessionsEndedBefore(ctx context.Context, userID string, before time.Time) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions s WHERE user_id = ? AND ended_at < ? AND source_key IS NULL
		 ORDER BY started_at`, userID, formatTime(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Session
	for rows.Next() {
		ses, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ses)
	}
	return out, rows.Err()
}

// ApplyRollups additively upserts daily aggregates and deletes the
// rolled-up raw session rows in one transaction. Supersession rows
// referencing deleted sessions cascade. Idempotent by construction:
// deletion happens with the upsert, so a re-run never re-counts.
func (s *Store) ApplyRollups(ctx context.Context, userID string, rollups []store.SessionRollup, deleteSessions []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rollups {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_rollups (user_id, work_id, day, active_seconds, pages, prog_delta, session_count)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, work_id, day) DO UPDATE SET
			     active_seconds = active_seconds + excluded.active_seconds,
			     pages          = pages + excluded.pages,
			     prog_delta     = prog_delta + excluded.prog_delta,
			     session_count  = session_count + excluded.session_count`,
			userID, r.WorkID, r.Day, r.ActiveSeconds, r.Pages, r.ProgDelta, r.SessionCount); err != nil {
			return err
		}
	}
	for _, ses := range deleteSessions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_tombstones (user_id, session_id, fingerprint) VALUES (?, ?, ?)
			 ON CONFLICT(user_id, session_id) DO NOTHING`,
			userID, ses.SessionID, store.SessionFingerprint(ses)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = ? AND session_id = ?`,
			userID, ses.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RollupsInRange(ctx context.Context, userID, fromDay, toDay string) ([]store.SessionRollup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count
		 FROM session_rollups WHERE user_id = ? AND day >= ? AND day <= ?
		 ORDER BY day`, userID, fromDay, toDay)
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count
		 FROM session_rollups WHERE user_id = ? AND work_id = ?
		 ORDER BY day`, userID, workID)
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

// Housekeep deletes expired auth debris. Timestamps are RFC3339Nano UTC
// text, so lexicographic comparison is chronological.
func (s *Store) Housekeep(ctx context.Context, now time.Time) error {
	nowS := formatTime(now)
	graceS := formatTime(now.Add(-store.TokenPurgeGrace))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pairing_codes WHERE expires_at < ?`, nowS); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE expires_at < ? OR revoked_at IS NOT NULL`, nowS); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tokens WHERE (expires_at IS NOT NULL AND expires_at < ?)
		                       OR (revoked_at IS NOT NULL AND revoked_at < ?)`, graceS, graceS); err != nil {
		return err
	}
	return tx.Commit()
}
