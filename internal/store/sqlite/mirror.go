package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// MirrorCandidates is the bounded set of works a mirror could exchange:
// those carrying a KOReader fingerprint and touched since `since`,
// newest first, each with the newest op for the work.
//
// The fingerprint comes from the alias graph rather than from the
// catalog book, because the alias is the value both a KOReader device
// and a folder pass write. Its primary key guarantees one *alias row*
// per fingerprint per reader, but not that the catalog has only one
// book with that fingerprint: a 12-kilobyte KOReader sample can name
// two different active books, and only one of them holds the alias.
// The subquery below excludes any fingerprint the catalog itself
// cannot tell apart, which is the refusal ADR-0047 requires rather
// than a guess at which book the reader meant.
func (s *Store) MirrorCandidates(ctx context.Context, userID string, since time.Time, limit int) ([]store.MirrorCandidate, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.value, `+prefixed(opCols, "o")+`
		   FROM aliases a
		   JOIN ops o
		     ON o.user_id = a.user_id AND o.work_id = a.work_id
		  WHERE a.user_id = ?
		    AND a.kind = ?
		    AND o.seq = (SELECT MAX(seq) FROM ops m
		                  WHERE m.user_id = a.user_id AND m.work_id = a.work_id)
		    AND o.received_at >= ?
		    AND (SELECT COUNT(*) FROM books b
		          WHERE b.partial_md5 = a.value AND b.status = 'active') <= 1
		  ORDER BY o.seq DESC
		  LIMIT ?`,
		userID, "partial-md5", formatTime(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MirrorCandidate
	for rows.Next() {
		var c store.MirrorCandidate
		o, err := scanCandidate(rows, &c.Document)
		if err != nil {
			return nil, err
		}
		c.WorkID, c.Latest = o.WorkID, o
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MirrorCursors(ctx context.Context, userID, peer string) (map[string]store.MirrorCursor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT work_id, document, pushed_seq, pushed_at, remote_ts, pulled_at,
		        last_error, last_error_at
		   FROM mirror_cursors WHERE user_id = ? AND peer = ?`, userID, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]store.MirrorCursor{}
	for rows.Next() {
		var c store.MirrorCursor
		var pushedAt, pulledAt, errAt sql.NullString
		if err := rows.Scan(&c.WorkID, &c.Document, &c.PushedSeq, &pushedAt,
			&c.RemoteTS, &pulledAt, &c.LastError, &errAt); err != nil {
			return nil, err
		}
		var err2 error
		if c.PushedAt, err2 = parseTimePtr(pushedAt); err2 != nil {
			return nil, err2
		}
		if c.PulledAt, err2 = parseTimePtr(pulledAt); err2 != nil {
			return nil, err2
		}
		if c.LastErrorAt, err2 = parseTimePtr(errAt); err2 != nil {
			return nil, err2
		}
		out[c.WorkID] = c
	}
	return out, rows.Err()
}

func (s *Store) PutMirrorCursor(ctx context.Context, userID, peer string, c store.MirrorCursor) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mirror_cursors (user_id, work_id, peer, document, pushed_seq,
		                             pushed_at, remote_ts, pulled_at, last_error, last_error_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, peer, work_id) DO UPDATE SET
		   document = excluded.document,
		   pushed_seq = excluded.pushed_seq,
		   pushed_at = excluded.pushed_at,
		   remote_ts = excluded.remote_ts,
		   pulled_at = excluded.pulled_at,
		   last_error = excluded.last_error,
		   last_error_at = excluded.last_error_at`,
		userID, c.WorkID, peer, c.Document, c.PushedSeq, formatTimePtr(c.PushedAt),
		c.RemoteTS, formatTimePtr(c.PulledAt), c.LastError, formatTimePtr(c.LastErrorAt))
	return err
}

// scanCandidate reads the alias value and then an op, in the column
// order prefixed(opCols) produces.
func scanCandidate(rows *sql.Rows, document *string) (store.Op, error) {
	var o store.Op
	var editionSHA, foreignPos, originAlias sql.NullString
	var clientTS, receivedAt string
	err := rows.Scan(document, &o.UserID, &o.Seq, &o.OpID, &o.WorkID, &editionSHA,
		&o.DeviceID, &clientTS, &o.Progression, &o.LocatorJSON,
		&foreignPos, &o.Origin, &originAlias, &receivedAt)
	if err != nil {
		return o, err
	}
	if editionSHA.Valid {
		o.EditionSHA = &editionSHA.String
	}
	if foreignPos.Valid {
		o.ForeignPos = &foreignPos.String
	}
	if originAlias.Valid {
		o.OriginAlias = &originAlias.String
	}
	if o.ClientTS, err = parseTime(clientTS); err != nil {
		return o, err
	}
	o.ReceivedAt, err = parseTime(receivedAt)
	return o, err
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
