package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// MirrorCandidates is the bounded set of works a mirror could exchange,
// for the reason the SQLite copy gives.
func (s *Store) MirrorCandidates(ctx context.Context, userID string, since time.Time, limit int) ([]store.MirrorCandidate, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT a.value, `+prefixed(opCols, "o")+`
		   FROM aliases a
		   JOIN ops o
		     ON o.user_id = a.user_id AND o.work_id = a.work_id
		  WHERE a.user_id = ?
		    AND a.kind = ?
		    AND o.seq = (SELECT MAX(seq) FROM ops m
		                  WHERE m.user_id = a.user_id AND m.work_id = a.work_id)
		    AND o.received_at >= ?
		  ORDER BY o.seq DESC
		  LIMIT ?`),
		userID, "partial-md5", since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MirrorCandidate
	for rows.Next() {
		var c store.MirrorCandidate
		var o store.Op
		var origin string
		if err := rows.Scan(&c.Document, &o.UserID, &o.Seq, &o.OpID, &o.WorkID,
			&o.EditionSHA, &o.DeviceID, &o.ClientTS, &o.Progression, &o.LocatorJSON,
			&o.ForeignPos, &origin, &o.OriginAlias, &o.ReceivedAt); err != nil {
			return nil, err
		}
		o.Origin = store.Origin(origin)
		c.WorkID, c.Latest = o.WorkID, o
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MirrorCursors(ctx context.Context, userID, peer string) (map[string]store.MirrorCursor, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT work_id, document, pushed_seq, pushed_at, remote_ts, pulled_at,
		        last_error, last_error_at
		   FROM mirror_cursors WHERE user_id = ? AND peer = ?`), userID, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]store.MirrorCursor{}
	for rows.Next() {
		var c store.MirrorCursor
		if err := rows.Scan(&c.WorkID, &c.Document, &c.PushedSeq, &c.PushedAt,
			&c.RemoteTS, &c.PulledAt, &c.LastError, &c.LastErrorAt); err != nil {
			return nil, err
		}
		utcPtr(c.PushedAt)
		utcPtr(c.PulledAt)
		utcPtr(c.LastErrorAt)
		out[c.WorkID] = c
	}
	return out, rows.Err()
}

func (s *Store) PutMirrorCursor(ctx context.Context, userID, peer string, c store.MirrorCursor) error {
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO mirror_cursors (user_id, work_id, peer, document, pushed_seq,
		                             pushed_at, remote_ts, pulled_at, last_error, last_error_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, peer, work_id) DO UPDATE SET
		   document = excluded.document,
		   pushed_seq = excluded.pushed_seq,
		   pushed_at = excluded.pushed_at,
		   remote_ts = excluded.remote_ts,
		   pulled_at = excluded.pulled_at,
		   last_error = excluded.last_error,
		   last_error_at = excluded.last_error_at`),
		userID, c.WorkID, peer, c.Document, c.PushedSeq, c.PushedAt,
		c.RemoteTS, c.PulledAt, c.LastError, c.LastErrorAt)
	return err
}

// prefixed qualifies a bare column list with a table alias, so a join
// can reuse the same list the single-table reads use rather than
// keeping a second copy that drifts.
func prefixed(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
