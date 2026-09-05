package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) StatisticsSnapshot(ctx context.Context, userID string, candidateIDs []string) (store.StatsSnapshot, error) {
	var snap store.StatsSnapshot
	tx, err := s.db.BeginTx(ctx, snapshotTx)
	if err != nil {
		return snap, err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, q(
		`SELECT u.timezone, sr.revision
		   FROM users u JOIN stats_revisions sr ON sr.user_id = u.id
		  WHERE u.id = ?`), userID).Scan(&snap.Timezone, &snap.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return snap, store.ErrNotFound
		}
		return snap, err
	}

	rows, err := tx.QueryContext(ctx, q(
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
		   FROM sessions s
		  WHERE user_id = ?
		    AND (source_key IS NULL OR session_id = (
		        SELECT ss.session_id FROM session_supersessions ss
		         WHERE ss.user_id = s.user_id AND ss.source_key = s.source_key
		         ORDER BY ss.revision DESC LIMIT 1))
		  ORDER BY started_at, session_id`), userID)
	if err != nil {
		return snap, err
	}
	snap.Sessions, err = scanSessions(rows)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return snap, err
	}

	rows, err = tx.QueryContext(ctx, q(
		`SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        '' AS timezone, 0 AS attribution_version, 0 AS measured_active_seconds, 0 AS measured_prog_delta
		   FROM session_rollups WHERE user_id = ?
		  UNION ALL
		 SELECT user_id, work_id, day, active_seconds, pages, prog_delta, session_count,
		        timezone, attribution_version, measured_active_seconds, measured_prog_delta
		   FROM session_rollups_v2 WHERE user_id = ?
		  ORDER BY day, work_id, attribution_version, timezone`), userID, userID)
	if err != nil {
		return snap, err
	}
	snap.Rollups, err = scanRollups(rows)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return snap, err
	}

	rows, err = tx.QueryContext(ctx, q(
		`SELECT id, user_id, title, author, pending, created_at
		   FROM works WHERE user_id = ? ORDER BY id`), userID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var w store.Work
		if err := rows.Scan(&w.ID, &w.UserID, &w.Title, &w.Author, &w.Pending, &w.CreatedAt); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Works = append(snap.Works, w)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return snap, err
	}
	if err := rows.Close(); err != nil {
		return snap, err
	}

	snap.Positions = make(map[string]store.Op)
	rows, err = tx.QueryContext(ctx, q(
		`SELECT user_id, seq, op_id, work_id, edition_sha, device_id, client_ts,
		        progression, locator_json, foreign_pos, origin, origin_alias, received_at
		   FROM (
		     SELECT user_id, seq, op_id, work_id, edition_sha, device_id, client_ts,
		            progression, locator_json, foreign_pos, origin, origin_alias, received_at,
		            row_number() OVER (PARTITION BY work_id ORDER BY seq DESC) AS rn
		       FROM ops WHERE user_id = ?
		   ) ranked WHERE rn = 1 ORDER BY work_id`), userID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			rows.Close()
			return snap, err
		}
		snap.Positions[op.WorkID] = op
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return snap, err
	}
	if err := rows.Close(); err != nil {
		return snap, err
	}

	snap.Editions = make(map[string]store.Edition)
	rows, err = tx.QueryContext(ctx, q(
		`SELECT user_id, sha256, work_id, page_count, char_count, meta_json
		   FROM editions WHERE user_id = ? ORDER BY sha256`), userID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var e store.Edition
		if err := rows.Scan(&e.UserID, &e.SHA256, &e.WorkID, &e.PageCount, &e.CharCount, &e.MetaJSON); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Editions[e.SHA256] = e
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return snap, err
	}
	if err := rows.Close(); err != nil {
		return snap, err
	}

	snap.Archived = make(map[string]store.ArchivedSession)
	if err := loadArchivedSnapshot(ctx, tx, userID, candidateIDs, snap.Archived); err != nil {
		return snap, err
	}
	return snap, tx.Commit()
}

func loadArchivedSnapshot(ctx context.Context, tx *sql.Tx, userID string, ids []string, out map[string]store.ArchivedSession) error {
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		parts := make([]string, end-start)
		args := make([]any, 0, end-start+1)
		args = append(args, userID)
		for i, id := range ids[start:end] {
			parts[i] = fmt.Sprintf("$%d", i+2)
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT session_id, fingerprint, work_id, day, timezone, attribution_version, present,
			        active_seconds, pages, prog_delta, measured_active_seconds, measured_prog_delta
			   FROM session_tombstones WHERE user_id = $1 AND session_id IN (`+strings.Join(parts, ",")+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var a store.ArchivedSession
			var workID, day, timezone *string
			if err := rows.Scan(&id, &a.Fingerprint, &workID, &day, &timezone, &a.AttributionVersion,
				&a.Present, &a.ActiveSeconds, &a.Pages, &a.ProgDelta, &a.MeasuredActiveSeconds,
				&a.MeasuredProgDelta); err != nil {
				rows.Close()
				return err
			}
			if workID != nil {
				a.WorkID = *workID
			}
			if day != nil {
				a.Day = *day
			}
			if timezone != nil {
				a.Timezone = *timezone
			}
			out[id] = a
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
