package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// AppendSessions inserts a batch of sessions. Idempotent on
// (user_id, session_id): identical re-uploads are skipped; same id with
// a different payload is ErrIDMismatch for that item (reported per-item
// by the caller layer; here the whole batch fails atomically with
// per-item status returned via SessionResult in a later milestone —
// sessions arrive in M3, the store path exists now).
func (s *Store) AppendSessions(ctx context.Context, userID string, ss []store.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now())
	for _, ses := range ss {
		var found int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`,
			userID, ses.SessionID).Scan(&found)
		if err != nil {
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
			continue // idempotent duplicate
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO sessions (user_id, session_id, work_id, edition_sha, device_id,
			                       started_at, ended_at, start_prog, end_prog, idle_ms,
			                       origin, origin_alias, source_key, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, ses.SessionID, ses.WorkID, nullStr(ses.EditionSHA), ses.DeviceID,
			formatTime(ses.StartedAt), formatTime(ses.EndedAt),
			ses.StartProg, ses.EndProg, ses.IdleMs,
			string(ses.Origin), nullStr(ses.OriginAlias), nullStr(ses.SourceKey), now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sameSession(ctx context.Context, tx *sql.Tx, userID string, ses store.Session) (bool, error) {
	var (
		workID, devID, origin         string
		started, ended                string
		sp, ep                        float64
		idle                          int64
		edSHA, oalias, skey           sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key
		 FROM sessions WHERE user_id = ? AND session_id = ?`, userID, ses.SessionID).
		Scan(&workID, &edSHA, &devID, &started, &ended, &sp, &ep, &idle, &origin, &oalias, &skey)
	if err != nil {
		return false, err
	}
	return workID == ses.WorkID && devID == ses.DeviceID &&
		started == formatTime(ses.StartedAt) && ended == formatTime(ses.EndedAt) &&
		sp == ses.StartProg && ep == ses.EndProg && idle == ses.IdleMs &&
		origin == string(ses.Origin) &&
		edSHA.Valid == (ses.EditionSHA != nil) && (!edSHA.Valid || edSHA.String == *ses.EditionSHA) &&
		oalias.Valid == (ses.OriginAlias != nil) && (!oalias.Valid || oalias.String == *ses.OriginAlias) &&
		skey.Valid == (ses.SourceKey != nil) && (!skey.Valid || skey.String == *ses.SourceKey), nil
}

func (s *Store) SessionsForWork(ctx context.Context, userID, workID string, limit int) ([]store.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, session_id, work_id, edition_sha, device_id, started_at, ended_at,
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
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

func scanSession(row interface{ Scan(...any) error }) (store.Session, error) {
	var ses store.Session
	var edSHA, oalias, skey sql.NullString
	var started, ended, received string
	err := row.Scan(&ses.UserID, &ses.SessionID, &ses.WorkID, &edSHA, &ses.DeviceID,
		&started, &ended, &ses.StartProg, &ses.EndProg, &ses.IdleMs,
		&ses.Origin, &oalias, &skey, &received)
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
		        start_prog, end_prog, idle_ms, origin, origin_alias, source_key, received_at
		 FROM sessions
		 WHERE user_id = ? AND ended_at > ? AND started_at < ?
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
