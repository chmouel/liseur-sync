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
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
		 FROM sessions s WHERE user_id = ? AND ended_at < ? AND source_key IS NULL
		 ORDER BY started_at`), userID, before.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

// ApplyRollups additively upserts daily aggregates and deletes the
// rolled-up raw session rows in one transaction. Supersession rows
// referencing deleted sessions cascade. Version 2 rollups are kept in
// their own table and write exact per-session proof to tombstones.
func (s *Store) ApplyRollups(ctx context.Context, userID string, rollups []store.SessionRollup, deleteSessions []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	v2 := rollupsV2(rollups)
	for _, r := range v2 {
		if r.Timezone == "" {
			return store.ErrInvalidInput
		}
	}
	proofs := make([]store.ArchivedSession, 0, len(deleteSessions))
	pageCounts := make(map[string]sql.NullInt64)
	for _, expected := range deleteSessions {
		current, err := scanSession(tx.QueryRowContext(ctx, q(
			`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
			        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
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
		if len(v2) > 0 {
			proof, err := archivedContribution(ctx, tx, userID, current, v2, pageCounts)
			if err != nil {
				return err
			}
			proofs = append(proofs, proof)
		}
	}
	if len(v2) > 0 {
		if err := store.ValidateRollupContributions(v2, proofs); err != nil {
			return err
		}
	}
	for _, r := range rollups {
		if rollupAttributionVersion(r) == 2 {
			if _, err := tx.ExecContext(ctx, q(
				`INSERT INTO session_rollups_v2 (user_id, work_id, day, timezone, attribution_version,
				                              active_seconds, pages, prog_delta, session_count,
				                              measured_active_seconds, measured_prog_delta)
				 VALUES (?, ?, ?, ?, 2, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(user_id, work_id, day, timezone) DO UPDATE SET
				     active_seconds          = session_rollups_v2.active_seconds + excluded.active_seconds,
				     pages                   = session_rollups_v2.pages + excluded.pages,
				     prog_delta              = session_rollups_v2.prog_delta + excluded.prog_delta,
				     session_count           = session_rollups_v2.session_count + excluded.session_count,
				     measured_active_seconds = session_rollups_v2.measured_active_seconds + excluded.measured_active_seconds,
				     measured_prog_delta     = session_rollups_v2.measured_prog_delta + excluded.measured_prog_delta`),
				userID, r.WorkID, r.Day, r.Timezone, r.ActiveSeconds, r.Pages, r.ProgDelta,
				r.SessionCount, r.MeasuredActiveSeconds, r.MeasuredProgDelta); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO session_rollups (user_id, work_id, day, active_seconds, pages, prog_delta, session_count)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, work_id, day) DO UPDATE SET
			     active_seconds = session_rollups.active_seconds + excluded.active_seconds,
			     pages          = session_rollups.pages + excluded.pages,
			     prog_delta     = session_rollups.prog_delta + excluded.prog_delta,
			     session_count  = session_rollups.session_count + excluded.session_count`),
			userID, r.WorkID, r.Day, r.ActiveSeconds, r.Pages, r.ProgDelta, r.SessionCount); err != nil {
			return err
		}
	}
	for i, ses := range deleteSessions {
		if len(v2) > 0 {
			archived := proofs[i]
			if _, err := tx.ExecContext(ctx, q(
				`INSERT INTO session_tombstones (user_id, session_id, fingerprint, work_id, day, timezone,
				                                attribution_version, present, active_seconds, pages,
				                                prog_delta, measured_active_seconds, measured_prog_delta)
				 VALUES (?, ?, ?, ?, ?, ?, 2, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(user_id, session_id) DO NOTHING`),
				userID, ses.SessionID, store.SessionFingerprint(ses), archived.WorkID, archived.Day,
				archived.Timezone, archived.Present, archived.ActiveSeconds, archived.Pages,
				archived.ProgDelta, archived.MeasuredActiveSeconds, archived.MeasuredProgDelta); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, q(
				`INSERT INTO session_tombstones (user_id, session_id, fingerprint) VALUES (?, ?, ?)
				 ON CONFLICT(user_id, session_id) DO NOTHING`),
				userID, ses.SessionID, store.SessionFingerprint(ses)); err != nil {
				return err
			}
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

func rollupAttributionVersion(r store.SessionRollup) int {
	if r.AttributionVersion == 2 || r.Timezone != "" {
		return 2
	}
	return 0
}

func rollupsV2(rollups []store.SessionRollup) []store.SessionRollup {
	out := make([]store.SessionRollup, 0, len(rollups))
	for _, r := range rollups {
		if rollupAttributionVersion(r) == 2 {
			r.AttributionVersion = 2
			out = append(out, r)
		}
	}
	return out
}

func archivedContribution(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, rollups []store.SessionRollup, pageCounts map[string]sql.NullInt64) (store.ArchivedSession, error) {
	for _, r := range rollups {
		if r.WorkID != ses.WorkID || r.Timezone == "" {
			continue
		}
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			return store.ArchivedSession{}, err
		}
		if ses.EndedAt.In(loc).Format("2006-01-02") != r.Day {
			continue
		}
		proof := store.ArchivedSession{
			Fingerprint:        store.SessionFingerprint(ses),
			WorkID:             ses.WorkID,
			Day:                r.Day,
			Timezone:           r.Timezone,
			AttributionVersion: 2,
			Present:            true,
			ActiveSeconds:      sessionActiveSeconds(ses),
			ProgDelta:          sessionProgDelta(ses),
		}
		pages, err := sessionPages(ctx, tx, userID, ses, proof.ProgDelta, pageCounts)
		if err != nil {
			return store.ArchivedSession{}, err
		}
		proof.Pages = pages
		if ses.Origin != store.OriginInferred {
			proof.MeasuredActiveSeconds = proof.ActiveSeconds
			proof.MeasuredProgDelta = proof.ProgDelta
		}
		return proof, nil
	}
	return store.ArchivedSession{}, store.ErrConflict
}

func sessionActiveSeconds(ses store.Session) float64 {
	if ses.ActiveMs != nil {
		if *ses.ActiveMs < 0 {
			return 0
		}
		return float64(*ses.ActiveMs) / 1000
	}
	active := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	if active <= 0 {
		return 0
	}
	return active
}

func sessionProgDelta(ses store.Session) float64 {
	delta := ses.EndProg - ses.StartProg
	if delta < 0 {
		return 0
	}
	return delta
}

func sessionPages(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, progDelta float64, pageCounts map[string]sql.NullInt64) (float64, error) {
	if ses.ReportedPages != nil {
		return *ses.ReportedPages, nil
	}
	if ses.EditionSHA == nil || progDelta <= 0 {
		return 0, nil
	}
	pages, cached := pageCounts[*ses.EditionSHA]
	if !cached {
		err := tx.QueryRowContext(ctx, q(
			`SELECT page_count FROM editions WHERE user_id = ? AND sha256 = ?`),
			userID, *ses.EditionSHA).Scan(&pages)
		if err != nil {
			return 0, err
		}
		pageCounts[*ses.EditionSHA] = pages
	}
	if !pages.Valid {
		return 0, nil
	}
	return float64(pages.Int64) * progDelta, nil
}

func (s *Store) RollupsInRange(ctx context.Context, userID, fromDay, toDay string) ([]store.SessionRollup, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        '' AS timezone, 0 AS attribution_version, 0 AS measured_active_seconds, 0 AS measured_prog_delta
		 FROM session_rollups WHERE user_id = ? AND day >= ? AND day <= ?
		 UNION ALL
		 SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        timezone, attribution_version, measured_active_seconds, measured_prog_delta
		 FROM session_rollups_v2 WHERE user_id = ? AND day >= ? AND day <= ?
		 ORDER BY day, work_id, attribution_version`), userID, fromDay, toDay, userID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRollups(rows)
}

func (s *Store) RollupsForWork(ctx context.Context, userID, workID string) ([]store.SessionRollup, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        '' AS timezone, 0 AS attribution_version, 0 AS measured_active_seconds, 0 AS measured_prog_delta
		 FROM session_rollups WHERE user_id = ? AND work_id = ?
		 UNION ALL
		 SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        timezone, attribution_version, measured_active_seconds, measured_prog_delta
		 FROM session_rollups_v2 WHERE user_id = ? AND work_id = ?
		 ORDER BY day, attribution_version`), userID, workID, userID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRollups(rows)
}

func scanRollups(rows *sql.Rows) ([]store.SessionRollup, error) {
	var out []store.SessionRollup
	for rows.Next() {
		var r store.SessionRollup
		if err := rows.Scan(&r.UserID, &r.WorkID, &r.Day, &r.ActiveSeconds, &r.Pages, &r.ProgDelta,
			&r.SessionCount, &r.Timezone, &r.AttributionVersion, &r.MeasuredActiveSeconds,
			&r.MeasuredProgDelta); err != nil {
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
