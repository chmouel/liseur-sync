package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// AppendSessions inserts a batch of sessions atomically. Idempotent on
// (user_id, session_id): identical re-uploads are skipped; the same id
// with a different payload, live or archived as a tombstone, fails the
// batch with a *store.ItemError wrapping ErrIDMismatch that names the
// item; a new session naming a work this user lacks fails it with one
// wrapping ErrNotFound. Replays are judged before the work check, so a
// duplicate stays a duplicate after its work is gone.
func (s *Store) AppendSessions(ctx context.Context, userID string, ss []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	inserted, err := appendSessionsTx(ctx, tx, userID, ss, formatTime(time.Now()))
	if err != nil {
		return err
	}
	return s.commitSessions(tx, userID, inserted > 0)
}

// commitSessions commits a session write and, when it actually stored
// something, tells whoever is listening that this user's statistics
// moved. Sessions have no delta feed, so a client answers by asking for
// the numbers again. A batch that was entirely duplicates changed
// nothing and must stay silent.
func (s *Store) commitSessions(tx *sql.Tx, userID string, changed bool) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed {
		s.Notify(userID, store.TopicInsights)
	}
	return nil
}

// appendSessionsTx returns how many sessions it stored; the rest were
// idempotent replays.
func appendSessionsTx(ctx context.Context, tx *sql.Tx, userID string, ss []store.Session, now string) (int, error) {
	inserted := 0
	for i, ses := range ss {
		var archivedFingerprint string
		err := tx.QueryRowContext(ctx,
			`SELECT fingerprint FROM session_tombstones WHERE user_id = ? AND session_id = ?`,
			userID, ses.SessionID).Scan(&archivedFingerprint)
		if err == nil {
			if archivedFingerprint != store.SessionFingerprint(ses) {
				return inserted, store.IDMismatch("session", ses.SessionID, i)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return inserted, err
		}
		var found int
		err = tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`,
			userID, ses.SessionID).Scan(&found)
		if err != nil {
			return inserted, err
		}
		if found > 0 {
			same, err := sameSession(ctx, tx, userID, ses)
			if err != nil {
				return inserted, err
			}
			if !same {
				return inserted, store.IDMismatch("session", ses.SessionID, i)
			}
			continue // idempotent duplicate
		}
		if ok, err := workExistsTx(ctx, tx, userID, ses.WorkID); err != nil {
			return inserted, err
		} else if !ok {
			return inserted, store.UnknownWork("session", ses.SessionID, i, ses.WorkID)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO sessions (user_id, session_id, work_id, edition_sha, device_id,
			                       started_at, ended_at, start_prog, end_prog, idle_ms, active_ms, reported_pages,
			                       origin, origin_alias, source_key, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, ses.SessionID, ses.WorkID, nullStr(ses.EditionSHA), ses.DeviceID,
			formatTime(ses.StartedAt), formatTime(ses.EndedAt),
			ses.StartProg, ses.EndProg, ses.IdleMs, ses.ActiveMs, ses.ReportedPages,
			string(ses.Origin), nullStr(ses.OriginAlias), nullStr(ses.SourceKey), now)
		if err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
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
		current, err := scanOp(tx.QueryRowContext(ctx,
			`SELECT `+opCols+` FROM ops
			 WHERE user_id = ? AND op_id = ? AND inferred_session_id IS NULL`,
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
	inserted, err := appendSessionsTx(ctx, tx, userID,
		[]store.Session{group.Session}, formatTime(time.Now()))
	if err != nil {
		return err
	}
	for _, op := range group.Ops {
		result, err := tx.ExecContext(ctx,
			`UPDATE ops SET inferred_session_id = ?
			 WHERE user_id = ? AND op_id = ? AND inferred_session_id IS NULL`,
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
	// Stamping ops for a session already stored changes no statistics.
	return s.commitSessions(tx, userID, inserted > 0)
}

func sameSession(ctx context.Context, tx *sql.Tx, userID string, ses store.Session) (bool, error) {
	var (
		workID, devID, origin string
		started, ended        string
		sp, ep                float64
		idle                  int64
		active                sql.NullInt64
		pages                 sql.NullFloat64
		edSHA, oalias, skey   sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key
		 FROM sessions WHERE user_id = ? AND session_id = ?`, userID, ses.SessionID).
		Scan(&workID, &edSHA, &devID, &started, &ended, &sp, &ep, &idle, &active, &pages, &origin, &oalias, &skey)
	if err != nil {
		return false, err
	}
	return workID == ses.WorkID && devID == ses.DeviceID &&
		started == formatTime(ses.StartedAt) && ended == formatTime(ses.EndedAt) &&
		sp == ses.StartProg && ep == ses.EndProg && idle == ses.IdleMs &&
		active.Valid == (ses.ActiveMs != nil) && (!active.Valid || active.Int64 == *ses.ActiveMs) &&
		pages.Valid == (ses.ReportedPages != nil) && (!pages.Valid || pages.Float64 == *ses.ReportedPages) &&
		origin == string(ses.Origin) &&
		edSHA.Valid == (ses.EditionSHA != nil) && (!edSHA.Valid || edSHA.String == *ses.EditionSHA) &&
		oalias.Valid == (ses.OriginAlias != nil) && (!oalias.Valid || oalias.String == *ses.OriginAlias) &&
		skey.Valid == (ses.SourceKey != nil) && (!skey.Valid || skey.String == *ses.SourceKey), nil
}

func (s *Store) SessionsForWork(ctx context.Context, userID, workID string, limit int) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
		 FROM sessions WHERE user_id = ? AND work_id = ? ORDER BY started_at DESC LIMIT ?`,
		userID, workID, limit)
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

// CurrentSessionsForWork excludes superseded koplugin revisions.
func (s *Store) CurrentSessionsForWork(ctx context.Context, userID, workID string, limit int) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
		 FROM sessions s WHERE user_id = ? AND work_id = ?
		   AND (source_key IS NULL OR session_id = (
		       SELECT ss.session_id FROM session_supersessions ss
		       WHERE ss.user_id = s.user_id AND ss.source_key = s.source_key
		       ORDER BY ss.revision DESC LIMIT 1))
		 ORDER BY started_at DESC LIMIT ?`,
		userID, workID, limit)
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

// WorkIDsWithInsights returns only works which have raw sessions or
// retained rollups. It keeps the aggregate endpoint proportional to the
// reader's history rather than to the size of a catalog it once resolved.
func (s *Store) WorkIDsWithInsights(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT work_id FROM sessions WHERE user_id = ?
		 UNION
		 SELECT work_id FROM session_rollups WHERE user_id = ?
		 UNION
		 SELECT work_id FROM session_rollups_v2 WHERE user_id = ?
		 ORDER BY work_id`, userID, userID, userID)
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
	var edSHA, oalias, skey sql.NullString
	var active sql.NullInt64
	var pages sql.NullFloat64
	var started, ended, received string
	err := row.Scan(&ses.UserID, &ses.SessionID, &ses.WorkID, &edSHA, &ses.DeviceID,
		&started, &ended, &ses.StartProg, &ses.EndProg, &ses.IdleMs,
		&active, &pages, &ses.Origin, &oalias, &skey, &received)
	if err != nil {
		return ses, err
	}
	if edSHA.Valid {
		ses.EditionSHA = &edSHA.String
	}
	if oalias.Valid {
		ses.OriginAlias = &oalias.String
	}
	if skey.Valid {
		ses.SourceKey = &skey.String
	}
	if active.Valid {
		ses.ActiveMs = &active.Int64
	}
	if pages.Valid {
		ses.ReportedPages = &pages.Float64
	}
	if ses.StartedAt, err = parseTime(started); err != nil {
		return ses, err
	}
	if ses.EndedAt, err = parseTime(ended); err != nil {
		return ses, err
	}
	ses.ReceivedAt, err = parseTime(received)
	return ses, err
}

var _ = errors.Is // keep import used if helpers change

// SessionsInRange returns measured (non-inferred) sessions overlapping
// [from, to), oldest first — the input to insights aggregation.
func (s *Store) SessionsInRange(ctx context.Context, userID string, from, to time.Time) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, active_ms, reported_pages, origin, origin_alias, source_key, received_at
		 FROM sessions
		 WHERE user_id = ? AND ended_at > ? AND started_at < ?
		   AND (source_key IS NULL OR session_id = (
		       SELECT ss.session_id FROM session_supersessions ss
		       WHERE ss.user_id = sessions.user_id AND ss.source_key = sessions.source_key
		       ORDER BY ss.revision DESC LIMIT 1))
		 ORDER BY started_at`, userID, formatTime(from), formatTime(to))
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

func (s *Store) EditionBySHA(ctx context.Context, userID, sha256 string) (store.Edition, error) {
	var e store.Edition
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, sha256, work_id, page_count, char_count, meta_json
		 FROM editions WHERE user_id = ? AND sha256 = ?`, userID, sha256).
		Scan(&e.UserID, &e.SHA256, &e.WorkID, &e.PageCount, &e.CharCount, &e.MetaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return e, store.ErrNotFound
	}
	return e, err
}
