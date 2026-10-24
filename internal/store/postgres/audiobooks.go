package postgres

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
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+audiobookLinkColumns+`
		   FROM audiobook_links WHERE user_id = ? AND peer_identity = ?
		  ORDER BY created_at, abs_item_id`), userID, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AudiobookLink
	for rows.Next() {
		var l store.AudiobookLink
		var mapping, state string
		if err := rows.Scan(&l.PeerIdentity, &l.ItemID, &l.WorkID, &l.BookID, &l.EditionSHA,
			&l.Generation, &l.Source, &state, &l.ConfirmedLinear, &mapping, &l.MappingKey, &l.PushedSeq, &l.PushedMark,
			&l.RemoteUpdatedMs, &l.SessionsWatermarkMs, &l.LastError, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		l.State = store.AudiobookLinkState(state)
		if mapping != "" {
			l.Mapping = []byte(mapping)
		}
		l.CreatedAt, l.UpdatedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) CreateAudiobookLink(ctx context.Context, userID string, l store.AudiobookLink) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
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
	_, err = tx.ExecContext(ctx, q(
		`INSERT INTO audiobook_links (user_id, `+audiobookLinkColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		userID, l.PeerIdentity, l.ItemID, l.WorkID, l.BookID, l.EditionSHA, uuid.NewString(), l.Source,
		string(l.State), l.ConfirmedLinear, string(l.Mapping), l.MappingKey,
		l.PushedSeq, l.PushedMark, l.RemoteUpdatedMs, l.SessionsWatermarkMs, l.LastError,
		l.CreatedAt.UTC(), now)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM audiobook_link_rejections
		  WHERE user_id = ? AND peer_identity = ? AND work_id = ? AND abs_item_id = ?`),
		userID, l.PeerIdentity, l.WorkID, l.ItemID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateAudiobookLink(ctx context.Context, userID string, l store.AudiobookLink) error {
	res, err := s.db.ExecContext(ctx, q(`UPDATE audiobook_links
 SET book_id = ?, edition_sha = ?, source = ?, state = ?, confirmed_linear = ?,
 mapping = ?, mapping_key = ?, generation = ?, pushed_seq = 0, pushed_mark = '',
 remote_updated_ms = 0, sessions_watermark_ms = 0, last_error = '', updated_at = ?
 WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ?`),
		l.BookID, l.EditionSHA, l.Source, string(l.State), l.ConfirmedLinear,
		string(l.Mapping), l.MappingKey, uuid.NewString(), time.Now().UTC(),
		userID, l.PeerIdentity, l.ItemID, l.Generation)
	return audiobookUpdated(res, err)
}

func (s *Store) UpdateAudiobookCursor(ctx context.Context, userID string, l store.AudiobookLink) error {
	res, err := s.db.ExecContext(ctx, q(`UPDATE audiobook_links SET pushed_seq = ?, pushed_mark = ?, remote_updated_ms = ?,
 sessions_watermark_ms = ?, last_error = ?, updated_at = ?
 WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ?`),
		l.PushedSeq, l.PushedMark, l.RemoteUpdatedMs, l.SessionsWatermarkMs, l.LastError,
		time.Now().UTC(), userID, l.PeerIdentity, l.ItemID, l.Generation)
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
	defer func() { _ = tx.Rollback() }()
	var workID string
	err = tx.QueryRowContext(ctx, q(`DELETE FROM audiobook_links WHERE user_id = ? AND peer_identity = ? AND abs_item_id = ? AND generation = ? RETURNING work_id`), userID, peer, itemID, generation).Scan(&workID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if reject {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO audiobook_link_rejections (user_id, peer_identity, work_id, abs_item_id, created_at)
			 VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`),
			userID, peer, workID, itemID, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AudiobookRejections(ctx context.Context, userID, peer string) ([]store.AudiobookRejection, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT work_id, abs_item_id FROM audiobook_link_rejections
		  WHERE user_id = ? AND peer_identity = ?`), userID, peer)
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

// moveAudiobookLinksTx: see the SQLite implementation's doc comment.
func moveAudiobookLinksTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	for _, query := range []string{
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
		if _, err := tx.ExecContext(ctx, q(query), userID, fromWorkID, userID, intoWorkID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, q(`UPDATE audiobook_links SET work_id = ?, generation = ? WHERE user_id = ? AND work_id = ?`), intoWorkID, uuid.NewString(), userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(`UPDATE audiobook_link_rejections SET work_id = ? WHERE user_id = ? AND work_id = ?`), intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	return nil
}

// splitAudiobookLinksTx: see the SQLite implementation's doc comment.
func splitAudiobookLinksTx(ctx context.Context, tx *sql.Tx, userID, workID, editionSHA, newWorkID string) error {
	_, err := tx.ExecContext(ctx, q(
		`UPDATE audiobook_links SET work_id = ?, generation = ?
		  WHERE user_id = ? AND work_id = ? AND edition_sha = ?`),
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
	defer func() { _ = tx.Rollback() }()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", err
	}
	var ledger int
	if err := tx.QueryRowContext(ctx, q(`SELECT COUNT(1) FROM audiobook_session_imports WHERE user_id = ? AND source_key = ?`), userID, *ses.SourceKey).Scan(&ledger); err != nil {
		return "", err
	}
	if ledger > 0 {
		var current int
		if err := tx.QueryRowContext(ctx, q(`SELECT COUNT(1) FROM session_supersessions WHERE user_id = ? AND source_key = ?`), userID, *ses.SourceKey).Scan(&current); err != nil {
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
	now := time.Now().UTC()
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
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO audiobook_mappings (user_id, mapping_key, mapping) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`), userID, key, string(canonical)); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO audiobook_session_imports (user_id, source_key, mapping_key, first_imported_at) VALUES (?, ?, ?, ?)`), userID, *ses.SourceKey, key, now); err != nil {
			return "", err
		}
	}
	if err := s.commitSessions(tx, userID, true); err != nil {
		return "", err
	}
	return status, nil
}

func (s *Store) CurrentSourceSession(ctx context.Context, userID, sourceKey string) (store.Session, error) {
	ses, err := scanSession(s.db.QueryRowContext(ctx, q(
		`SELECT s.user_id, s.session_id, s.work_id, s.edition_sha, s.device_id, s.started_at, s.ended_at,
		        s.start_prog, s.end_prog, s.idle_ms, s.active_ms, s.reported_pages, s.origin,
		        s.origin_alias, s.source_key, s.received_at
		   FROM session_supersessions ss
		   JOIN sessions s ON s.user_id = ss.user_id AND s.session_id = ss.session_id
		  WHERE ss.user_id = ? AND ss.source_key = ?
		  ORDER BY ss.revision DESC LIMIT 1`), userID, sourceKey))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, store.ErrNotFound
	}
	ses.StartedAt, ses.EndedAt, ses.ReceivedAt = ses.StartedAt.UTC(), ses.EndedAt.UTC(), ses.ReceivedAt.UTC()
	return ses, err
}

func (s *Store) AudiobookSessionImport(ctx context.Context, userID, sourceKey string) (store.AudiobookSessionImport, error) {
	var out store.AudiobookSessionImport
	var raw string

	err := s.db.QueryRowContext(ctx, q(`SELECT i.mapping_key, m.mapping, i.first_imported_at FROM audiobook_session_imports i
 JOIN audiobook_mappings m ON m.user_id = i.user_id AND m.mapping_key = i.mapping_key
 WHERE i.user_id = ? AND i.source_key = ?`), userID, sourceKey).Scan(&out.MappingKey, &raw, &out.FirstImportedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Mapping = []byte(raw)

	return out, err
}

func (s *Store) AudiobookSessionImported(ctx context.Context, userID, sourceKey, sessionID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, q(`SELECT COUNT(1) FROM session_supersessions WHERE user_id = ? AND source_key = ? AND session_id = ?`), userID, sourceKey, sessionID).Scan(&count)
	return count > 0, err
}

func appendSourceSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, remoteUpdatedAt *time.Time) (string, error) {
	var n int
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`),
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
	var remoteAt sql.NullTime
	var edition sql.NullString
	var workID, deviceID string
	err := tx.QueryRowContext(ctx, q(
		`SELECT ss.revision, ss.remote_updated_at, s.work_id, s.edition_sha, s.device_id
		   FROM session_supersessions ss
		   JOIN sessions s ON s.user_id = ss.user_id AND s.session_id = ss.session_id
		  WHERE ss.user_id = ? AND ss.source_key = ?
		  ORDER BY ss.revision DESC LIMIT 1`), userID, *ses.SourceKey).
		Scan(&rev, &remoteAt, &workID, &edition, &deviceID)
	status := "inserted"
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", err
	default:
		if remoteUpdatedAt != nil && remoteAt.Valid {
			switch incoming, current := remoteUpdatedAt.UTC(), remoteAt.Time.UTC(); {
			case incoming.Before(current):
				return "stale", nil
			case tsEqual(incoming, current):
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
	now := time.Now().UTC()
	if err := insertSessionTx(ctx, tx, userID, ses, now); err != nil {
		return "", err
	}
	var remoteValue any
	if remoteUpdatedAt != nil {
		remoteValue = remoteUpdatedAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO session_supersessions (user_id, source_key, revision, session_id, received_at, remote_updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		userID, *ses.SourceKey, rev+1, ses.SessionID, now, remoteValue); err != nil {
		return "", err
	}
	return status, nil
}
