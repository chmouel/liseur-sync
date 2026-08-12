package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) AppendSessions(ctx context.Context, userID string, ss []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	if err := appendSessionsTx(ctx, tx, userID, ss, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func appendSessionsTx(ctx context.Context, tx *sql.Tx, userID string, ss []store.Session, now time.Time) error {
	for _, ses := range ss {
		var archivedFingerprint string
		err := tx.QueryRowContext(ctx, q(
			`SELECT fingerprint FROM session_tombstones WHERE user_id = ? AND session_id = ?`),
			userID, ses.SessionID).Scan(&archivedFingerprint)
		if err == nil {
			if archivedFingerprint != store.SessionFingerprint(ses) {
				return store.ErrIDMismatch
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var found int
		if err = tx.QueryRowContext(ctx, q(
			`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`),
			userID, ses.SessionID).Scan(&found); err != nil {
			return err
		}
		if found > 0 {
			same, err := sameSession(ctx, tx, userID, ses)
			if err != nil {
				return err
			}
			if !same {
				return store.ErrIDMismatch
			}
			continue
		}
		_, err = tx.ExecContext(ctx, q(
			`INSERT INTO sessions (user_id, session_id, work_id, edition_sha, device_id,
			                       started_at, ended_at, start_prog, end_prog, idle_ms,
			                       origin, origin_alias, source_key, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			userID, ses.SessionID, ses.WorkID, ses.EditionSHA, ses.DeviceID,
			ses.StartedAt.UTC(), ses.EndedAt.UTC(),
			ses.StartProg, ses.EndProg, ses.IdleMs,
			string(ses.Origin), ses.OriginAlias, ses.SourceKey, now)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AppendInferredSession(ctx context.Context, userID string, group store.InferredSessionGroup) error {
	if !store.ValidInferredSessionGroup(group) {
		return store.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	for _, expected := range group.Ops {
		current, err := scanOp(tx.QueryRowContext(ctx, q(
			`SELECT `+opCols+` FROM ops
			 WHERE user_id = ? AND op_id = ? AND inferred_session_id IS NULL`),
			userID, expected.OpID))
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrConflict
		}
		if err != nil {
			return err
		}
		if store.InferenceOpFingerprint(current) != store.InferenceOpFingerprint(expected) {
			return store.ErrConflict
		}
	}
	if err := appendSessionsTx(ctx, tx, userID,
		[]store.Session{group.Session}, time.Now().UTC()); err != nil {
		return err
	}
	for _, op := range group.Ops {
		result, err := tx.ExecContext(ctx, q(
			`UPDATE ops SET inferred_session_id = ?
			 WHERE user_id = ? AND op_id = ? AND inferred_session_id IS NULL`),
			group.Session.SessionID, userID, op.OpID)
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

func sameSession(ctx context.Context, tx *sql.Tx, userID string, ses store.Session) (bool, error) {
	var (
		workID, devID, origin string
		started, ended        time.Time
		sp, ep                float64
		idle                  int64
		edSHA, oalias, skey   *string
	)
	err := tx.QueryRowContext(ctx, q(
		`SELECT work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key
		 FROM sessions WHERE user_id = ? AND session_id = ?`), userID, ses.SessionID).
		Scan(&workID, &edSHA, &devID, &started, &ended, &sp, &ep, &idle, &origin, &oalias, &skey)
	if err != nil {
		return false, err
	}
	return workID == ses.WorkID && devID == ses.DeviceID &&
		tsEqual(started, ses.StartedAt) && tsEqual(ended, ses.EndedAt) &&
		sp == ses.StartProg && ep == ses.EndProg && idle == ses.IdleMs &&
		origin == string(ses.Origin) &&
		(edSHA == nil) == (ses.EditionSHA == nil) && (edSHA == nil || *edSHA == *ses.EditionSHA) &&
		(oalias == nil) == (ses.OriginAlias == nil) && (oalias == nil || *oalias == *ses.OriginAlias) &&
		(skey == nil) == (ses.SourceKey == nil) && (skey == nil || *skey == *ses.SourceKey), nil
}

func (s *Store) SessionsForWork(ctx context.Context, userID, workID string, limit int) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions WHERE user_id = ? AND work_id = ? ORDER BY started_at DESC LIMIT ?`),
		userID, workID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Session
	for rows.Next() {
		var ses store.Session
		var origin string
		if err := rows.Scan(&ses.UserID, &ses.SessionID, &ses.WorkID, &ses.EditionSHA, &ses.DeviceID,
			&ses.StartedAt, &ses.EndedAt, &ses.StartProg, &ses.EndProg, &ses.IdleMs,
			&origin, &ses.OriginAlias, &ses.SourceKey, &ses.ReceivedAt); err != nil {
			return nil, err
		}
		ses.Origin = store.Origin(origin)
		out = append(out, ses)
	}
	return out, rows.Err()
}

// CurrentSessionsForWork excludes superseded koplugin revisions.
func (s *Store) CurrentSessionsForWork(ctx context.Context, userID, workID string, limit int) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions s WHERE user_id = ? AND work_id = ?
		   AND (source_key IS NULL OR session_id = (
		       SELECT ss.session_id FROM session_supersessions ss
		       WHERE ss.user_id = s.user_id AND ss.source_key = s.source_key
		       ORDER BY ss.revision DESC LIMIT 1))
		 ORDER BY started_at DESC LIMIT ?`),
		userID, workID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Session
	for rows.Next() {
		var ses store.Session
		var origin string
		if err := rows.Scan(&ses.UserID, &ses.SessionID, &ses.WorkID, &ses.EditionSHA, &ses.DeviceID,
			&ses.StartedAt, &ses.EndedAt, &ses.StartProg, &ses.EndProg, &ses.IdleMs,
			&origin, &ses.OriginAlias, &ses.SourceKey, &ses.ReceivedAt); err != nil {
			return nil, err
		}
		ses.Origin = store.Origin(origin)
		out = append(out, ses)
	}
	return out, rows.Err()
}

// WorkIDsWithInsights returns only works which have raw sessions or
// retained rollups. It keeps the aggregate endpoint proportional to the
// reader's history rather than to the size of a catalog it once resolved.
func (s *Store) WorkIDsWithInsights(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT work_id FROM sessions WHERE user_id = ?
		 UNION
		 SELECT work_id FROM session_rollups WHERE user_id = ?
		 ORDER BY work_id`), userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var workID string
		if err := rows.Scan(&workID); err != nil {
			return nil, err
		}
		out = append(out, workID)
	}
	return out, rows.Err()
}

// SessionsInRange returns sessions overlapping [from, to), oldest first.
func (s *Store) SessionsInRange(ctx context.Context, userID string, from, to time.Time) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions
		 WHERE user_id = ? AND ended_at > ? AND started_at < ?
		   AND (source_key IS NULL OR session_id = (
		       SELECT ss.session_id FROM session_supersessions ss
		       WHERE ss.user_id = sessions.user_id AND ss.source_key = sessions.source_key
		       ORDER BY ss.revision DESC LIMIT 1))
		 ORDER BY started_at`), userID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

func scanSessions(rows *sql.Rows) ([]store.Session, error) {
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

func scanSession(row interface{ Scan(...any) error }) (store.Session, error) {
	var ses store.Session
	var origin string
	err := row.Scan(&ses.UserID, &ses.SessionID, &ses.WorkID, &ses.EditionSHA, &ses.DeviceID,
		&ses.StartedAt, &ses.EndedAt, &ses.StartProg, &ses.EndProg, &ses.IdleMs,
		&origin, &ses.OriginAlias, &ses.SourceKey, &ses.ReceivedAt)
	ses.Origin = store.Origin(origin)
	return ses, err
}

func (s *Store) EditionBySHA(ctx context.Context, userID, sha256 string) (store.Edition, error) {
	var e store.Edition
	err := s.db.QueryRowContext(ctx, q(
		`SELECT user_id, sha256, work_id, page_count, char_count, meta_json
		 FROM editions WHERE user_id = ? AND sha256 = ?`), userID, sha256).
		Scan(&e.UserID, &e.SHA256, &e.WorkID, &e.PageCount, &e.CharCount, &e.MetaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return e, store.ErrNotFound
	}
	return e, err
}
