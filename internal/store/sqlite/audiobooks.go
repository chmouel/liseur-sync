package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"errors"
	"github.com/google/uuid"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const audiobookLinkColumns = `peer_identity, abs_item_id, work_id, book_id, edition_sha, generation, source, state,
       confirmed_linear, mapping, mapping_key, pushed_seq, pushed_mark,
       remote_updated_ms, sessions_watermark_ms, last_error, created_at, updated_at`

func (s *Store) AudiobookLinks(ctx context.Context, userID, peer string) ([]store.AudiobookLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+audiobookLinkColumns+`
		   FROM audiobook_links WHERE user_id = ? AND peer_identity = ?
		  ORDER BY created_at, abs_item_id`, userID, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AudiobookLink
	for rows.Next() {
		var l store.AudiobookLink
		var mapping, state, created, updated string
		var linear int
		if err := rows.Scan(&l.PeerIdentity, &l.ItemID, &l.WorkID, &l.BookID, &l.EditionSHA,
			&l.Generation, &l.Source, &state, &linear, &mapping, &l.MappingKey, &l.PushedSeq, &l.PushedMark,
			&l.RemoteUpdatedMs, &l.SessionsWatermarkMs, &l.LastError, &created, &updated); err != nil {
			return nil, err
		}
		l.State = store.AudiobookLinkState(state)
		l.ConfirmedLinear = linear != 0
		if mapping != "" {
			l.Mapping = []byte(mapping)
		}
		if l.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) CreateAudiobookLink(ctx context.Context, userID string, l store.AudiobookLink) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exists, err := workExistsTx(ctx, tx, userID, l.WorkID)
	if err != nil {
		return err
	}
	if !exists {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	if l.CreatedAt.IsZero() {
		l.CreatedAt = now
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO audiobook_links (user_id, `+audiobookLinkColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, l.PeerIdentity, l.ItemID, l.WorkID, l.BookID, l.EditionSHA, uuid.NewString(), l.Source,
		string(l.State), boolInt(l.ConfirmedLinear), string(l.Mapping), l.MappingKey,
		l.PushedSeq, l.PushedMark, l.RemoteUpdatedMs, l.SessionsWatermarkMs, l.LastError,
		formatTime(l.CreatedAt), formatTime(now))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM audiobook_link_rejections
		  WHERE user_id = ? AND peer_identity = ? AND work_id = ? AND abs_item_id = ?`,
		userID, l.PeerIdentity, l.WorkID, l.ItemID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateAudiobookLink(ctx context.Context, userID string, l store.AudiobookLink) error {
	res, err := s.db.ExecContext(ctx, `UPDATE audiobook_links
 SET book_id = ?, edition_sha = ?, source = ?, state = ?, confirmed_linear = ?,
 mapping = ?, mapping_key = ?, generation = ?, pushed_seq = 0, pushed_mark = '',
 remote_updated_ms = 0, sessions_watermark_ms = 0, last_error = '', updated_at = ?
 WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ?`,
		l.BookID, l.EditionSHA, l.Source, string(l.State), boolInt(l.ConfirmedLinear),
		string(l.Mapping), l.MappingKey, uuid.NewString(), formatTime(time.Now()),
		userID, l.PeerIdentity, l.ItemID, l.Generation)
	return audiobookUpdated(res, err)
}

func (s *Store) UpdateAudiobookCursor(ctx context.Context, userID string, l store.AudiobookLink) error {
	res, err := s.db.ExecContext(ctx, `UPDATE audiobook_links SET pushed_seq = ?, pushed_mark = ?, remote_updated_ms = ?,
 sessions_watermark_ms = ?, last_error = ?, updated_at = ?
 WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ?`,
		l.PushedSeq, l.PushedMark, l.RemoteUpdatedMs, l.SessionsWatermarkMs, l.LastError,
		formatTime(time.Now()), userID, l.PeerIdentity, l.ItemID, l.Generation)
	return audiobookUpdated(res, err)
}

func audiobookUpdated(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrConflict
	}
	return nil
}

func (s *Store) DeleteAudiobookLink(ctx context.Context, userID, peer, itemID, generation string, reject bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workID string
	err = tx.QueryRowContext(ctx, `DELETE FROM audiobook_links WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ? RETURNING work_id`, userID, peer, itemID, generation).Scan(&workID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if reject {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO audiobook_link_rejections (user_id, peer_identity, work_id, abs_item_id, created_at)
			 VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			userID, peer, workID, itemID, formatTime(time.Now())); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AudiobookRejections(ctx context.Context, userID, peer string) ([]store.AudiobookRejection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT work_id, abs_item_id FROM audiobook_link_rejections
		  WHERE user_id = ? AND peer_identity = ?`, userID, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AudiobookRejection
	for rows.Next() {
		var r store.AudiobookRejection
		if err := rows.Scan(&r.WorkID, &r.ItemID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// moveAudiobookLinksTx carries a merged work's links to the survivor.
// When both works are linked to the same peer the survivor's link wins,
// whatever either state, because two links on one work would break the
// one-to-one rule every pass relies on. Rejections follow and collapse.
func moveAudiobookLinksTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	for _, q := range []string{
		`DELETE FROM audiobook_links
		  WHERE user_id = ? AND work_id = ?
		    AND peer_identity IN (SELECT peer_identity FROM audiobook_links
		                           WHERE user_id = ? AND work_id = ?)`,
		`DELETE FROM audiobook_link_rejections
		  WHERE user_id = ? AND work_id = ?
		    AND (peer_identity, abs_item_id) IN (SELECT peer_identity, abs_item_id
		                                           FROM audiobook_link_rejections
		                                          WHERE user_id = ? AND work_id = ?)`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID, fromWorkID, userID, intoWorkID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE audiobook_links SET work_id = ?, generation = ? WHERE user_id = ? AND work_id = ?`, intoWorkID, uuid.NewString(), userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE audiobook_link_rejections SET work_id = ? WHERE user_id = ? AND work_id = ?`, intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	return nil
}

// splitAudiobookLinksTx moves a link with the edition it was computed
// from. The new work has no links yet, so nothing can collide.
func splitAudiobookLinksTx(ctx context.Context, tx *sql.Tx, userID, workID, editionSHA, newWorkID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE audiobook_links SET work_id = ?, generation = ?
		  WHERE user_id = ? AND work_id = ? AND edition_sha = ?`,
		newWorkID, uuid.NewString(), userID, workID, editionSHA)
	return err
}

func (s *Store) AppendAudiobookSession(ctx context.Context, userID string, ses store.Session, remoteUpdatedAt time.Time, mapping []byte) (string, error) {
	if ses.SourceKey == nil || *ses.SourceKey == "" || ses.SessionID == "" {
		return "", store.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", err
	}
	var ledger int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM audiobook_session_imports WHERE user_id = ? AND source_key = ?`, userID, *ses.SourceKey).Scan(&ledger); err != nil {
		return "", err
	}
	if ledger > 0 {
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM session_supersessions WHERE user_id = ? AND source_key = ?`, userID, *ses.SourceKey).Scan(&current); err != nil {
			return "", err
		}
		if current == 0 {
			return "deleted", nil
		}
	}
	status, err := appendSourceSessionTx(ctx, tx, userID, ses, &remoteUpdatedAt)
	if err != nil {
		return "", err
	}
	if status != "inserted" && status != "superseded" {
		return status, nil
	}
	now := formatTime(time.Now())
	if ledger == 0 {
		var value any
		if err := json.Unmarshal(mapping, &value); err != nil {
			return "", store.ErrInvalidInput
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(canonical)
		key := hex.EncodeToString(sum[:])
		if _, err := tx.ExecContext(ctx, `INSERT INTO audiobook_mappings (user_id, mapping_key, mapping) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, userID, key, string(canonical)); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audiobook_session_imports (user_id, source_key, mapping_key, first_imported_at) VALUES (?, ?, ?, ?)`, userID, *ses.SourceKey, key, now); err != nil {
			return "", err
		}
	}
	if err := s.commitSessions(tx, userID, true); err != nil {
		return "", err
	}
	return status, nil
}

func (s *Store) CurrentSourceSession(ctx context.Context, userID, sourceKey string) (store.Session, error) {
	ses, err := scanSession(s.db.QueryRowContext(ctx,
		`SELECT s.user_id, s.session_id, s.work_id, s.edition_sha, s.device_id, s.started_at, s.ended_at,
		        s.start_prog, s.end_prog, s.idle_ms, s.active_ms, s.reported_pages, s.origin,
		        s.origin_alias, s.source_key, s.received_at
		   FROM session_supersessions ss
		   JOIN sessions s ON s.user_id = ss.user_id AND s.session_id = ss.session_id
		  WHERE ss.user_id = ? AND ss.source_key = ?
		  ORDER BY ss.revision DESC LIMIT 1`, userID, sourceKey))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	return ses, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) AudiobookSessionImport(ctx context.Context, userID, sourceKey string) (store.AudiobookSessionImport, error) {
	var out store.AudiobookSessionImport
	var raw string
	var created string

	err := s.db.QueryRowContext(ctx, `SELECT i.mapping_key, m.mapping, i.first_imported_at FROM audiobook_session_imports i
 JOIN audiobook_mappings m ON m.user_id = i.user_id AND m.mapping_key = i.mapping_key
 WHERE i.user_id = ? AND i.source_key = ?`, userID, sourceKey).Scan(&out.MappingKey, &raw, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Mapping = []byte(raw)
	out.FirstImportedAt, err = parseTime(created)

	return out, err
}

func (s *Store) AudiobookSessionImported(ctx context.Context, userID, sourceKey, sessionID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM session_supersessions WHERE user_id = ? AND source_key = ? AND session_id = ?`, userID, sourceKey, sessionID).Scan(&count)
	return count > 0, err
}

func appendSourceSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, remoteUpdatedAt *time.Time) (string, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`,
		userID, ses.SessionID).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		same, err := sameSession(ctx, tx, userID, ses)
		if err != nil {
			return "", err
		}
		if !same {
			return "conflict", nil
		}
		return "duplicate", nil
	}
	var rev int64
	var remoteAt, edition sql.NullString
	var workID, deviceID string
	err := tx.QueryRowContext(ctx,
		`SELECT ss.revision, ss.remote_updated_at, s.work_id, s.edition_sha, s.device_id
		   FROM session_supersessions ss
		   JOIN sessions s ON s.user_id = ss.user_id AND s.session_id = ss.session_id
		  WHERE ss.user_id = ? AND ss.source_key = ?
		  ORDER BY ss.revision DESC LIMIT 1`, userID, *ses.SourceKey).
		Scan(&rev, &remoteAt, &workID, &edition, &deviceID)
	status := "inserted"
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", err
	default:
		if remoteUpdatedAt != nil && remoteAt.Valid {
			current, err := parseTime(remoteAt.String)
			if err != nil {
				return "", err
			}
			switch incoming := remoteUpdatedAt.UTC(); {
			case incoming.Before(current):
				return "stale", nil
			case incoming.Equal(current):
				return "conflict", nil
			}
		}
		if remoteUpdatedAt != nil {
			ses.WorkID, ses.DeviceID, ses.EditionSHA = workID, deviceID, nil
			if edition.Valid {
				ses.EditionSHA = &edition.String
			}
		}
		status = "superseded"
	}
	now := formatTime(time.Now())
	if err := insertSessionTx(ctx, tx, userID, ses, now); err != nil {
		return "", err
	}
	var remoteValue any
	if remoteUpdatedAt != nil {
		remoteValue = formatTime(*remoteUpdatedAt)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_supersessions (user_id, source_key, revision, session_id, received_at, remote_updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		userID, *ses.SourceKey, rev+1, ses.SessionID, now, remoteValue); err != nil {
		return "", err
	}
	return status, nil
}
