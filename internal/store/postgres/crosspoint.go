package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func kosyncStatisticsCutoffTx(ctx context.Context, tx *sql.Tx, userID, slot string) (*time.Time, error) {
	var cutoff sql.NullTime
	err := tx.QueryRowContext(ctx, q(`SELECT measured_sessions_from FROM kosync_devices kd
 WHERE kd.user_id = ? AND kd.device_slot = ? AND kd.revoked_at IS NULL
 AND EXISTS (SELECT 1 FROM users u WHERE u.id = kd.user_id AND u.disabled_at IS NULL AND u.kosync_enabled = TRUE) FOR UPDATE`), userID, slot).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !cutoff.Valid {
		return nil, nil
	}
	at := cutoff.Time
	return &at, nil
}

func (s *Store) EnableKosyncStatistics(ctx context.Context, userID, slot string, at time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return time.Time{}, err
	}
	cutoff, err := kosyncStatisticsCutoffTx(ctx, tx, userID, slot)
	if err != nil {
		return time.Time{}, err
	}
	if cutoff != nil {
		return *cutoff, tx.Commit()
	}
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, q(`UPDATE kosync_devices SET measured_sessions_from = ? WHERE user_id = ? AND device_slot = ?`), at, userID, slot); err != nil {
		return time.Time{}, err
	}
	return at, tx.Commit()
}

func (s *Store) AppendKosyncSession(ctx context.Context, userID, slot, document, eventID, payloadHash string, session store.Session) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", err
	}
	cutoff, err := kosyncStatisticsCutoffTx(ctx, tx, userID, slot)
	if err != nil {
		return "", err
	}
	if cutoff == nil {
		return "", store.ErrConflict
	}
	var previous, outcome string
	err = tx.QueryRowContext(ctx, q(`SELECT payload_hash, outcome FROM crosspoint_session_receipts WHERE user_id = ? AND device_slot = ? AND event_id = ?`), userID, slot, eventID).Scan(&previous, &outcome)
	if err == nil {
		if previous != payloadHash {
			return "", store.ErrIDMismatch
		}
		return outcome, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	outcome = "ignored"
	var workID, sessionID *string
	inserted := 0
	if !session.StartedAt.Before(*cutoff) {
		session.WorkID, _, err = createPendingWorkTx(ctx, tx, userID, document)
		if err != nil {
			return "", err
		}
		session.DeviceID = "kosync:" + slot
		session.Origin = store.OriginKosync
		alias := "partial-md5:" + document
		session.OriginAlias = &alias
		session.EditionSHA = nil
		var sha string
		// Partial-MD5 collisions and ungranted files cannot supply edition metadata.
		err = tx.QueryRowContext(ctx, q(`SELECT MIN(b.content_sha256) FROM books b
   WHERE b.partial_md5 = ? AND b.status = 'active'
   AND EXISTS (SELECT 1 FROM user_folders uf WHERE uf.folder_id = b.folder_id AND uf.user_id = ?)
   HAVING COUNT(DISTINCT b.content_sha256) = 1`), document, userID).Scan(&sha)
		if err == nil {
			var found int
			err = tx.QueryRowContext(ctx, q(`SELECT COUNT(*) FROM editions WHERE user_id = ? AND work_id = ? AND sha256 = ?`), userID, session.WorkID, sha).Scan(&found)
			if err != nil {
				return "", err
			}
			if found == 1 {
				session.EditionSHA = &sha
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		inserted, err = appendSessionsTx(ctx, tx, userID, []store.Session{session}, time.Now().UTC())
		if err != nil {
			return "", err
		}
		outcome = "stored"
		workID, sessionID = &session.WorkID, &session.SessionID
	}
	if _, err := tx.ExecContext(ctx, q(`INSERT INTO crosspoint_session_receipts (user_id, device_slot, event_id, payload_hash, outcome, work_id, session_id) VALUES (?, ?, ?, ?, ?, ?, ?)`), userID, slot, eventID, payloadHash, outcome, workID, sessionID); err != nil {
		return "", err
	}
	return outcome, s.commitSessions(tx, userID, inserted > 0)
}
