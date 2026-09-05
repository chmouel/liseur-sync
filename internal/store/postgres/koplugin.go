package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateKopluginDevice(ctx context.Context, d store.KopluginDevice) error {
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO koplugin_devices (id, user_id, token_sha256, label, device_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		d.ID, d.UserID, d.TokenSHA256, d.Label, d.DeviceID, d.CreatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) KopluginDeviceByToken(ctx context.Context, tokenSHA256 string) (store.KopluginDevice, error) {
	var d store.KopluginDevice
	err := s.db.QueryRowContext(ctx, q(
		`SELECT id, user_id, token_sha256, label, device_id, created_at, revoked_at
		 FROM koplugin_devices WHERE token_sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = koplugin_devices.user_id AND u.disabled_at IS NULL)`), tokenSHA256).
		Scan(&d.ID, &d.UserID, &d.TokenSHA256, &d.Label, &d.DeviceID, &d.CreatedAt, &d.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, store.ErrNotFound
	}
	return d, err
}

// UpsertKopluginSession: see the SQLite implementation's doc comment.
func (s *Store) UpsertKopluginSession(ctx context.Context, userID string, ses store.Session) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", err
	}
	status, err := upsertKopluginSessionTx(ctx, tx, userID, ses)
	if err != nil {
		return "", err
	}
	if err := s.commitSessions(tx, userID, status != "duplicate"); err != nil {
		return "", err
	}
	return status, nil
}

func (s *Store) UpsertKopluginSessionByAlias(ctx context.Context, userID, partialMD5 string, ses store.Session) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return "", err
	}
	workID, _, err := createPendingWorkTx(ctx, tx, userID, partialMD5)
	if err != nil {
		return "", err
	}
	ses.WorkID = workID
	status, err := upsertKopluginSessionTx(ctx, tx, userID, ses)
	if err != nil {
		return "", err
	}
	if err := s.commitSessions(tx, userID, status != "duplicate"); err != nil {
		return "", err
	}
	return status, nil
}

func upsertKopluginSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session) (string, error) {
	if ses.ReportedPages == nil {
		one := 1.0
		ses.ReportedPages = &one
	}
	now := time.Now().UTC()

	var cnt int
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(1) FROM sessions WHERE user_id = ? AND session_id = ?`),
		userID, ses.SessionID).Scan(&cnt); err != nil {
		return "", err
	}
	if cnt > 0 {
		return "duplicate", nil
	}

	var curSessionID string
	var curRev int64
	err := tx.QueryRowContext(ctx, q(
		`SELECT session_id, revision FROM session_supersessions
		 WHERE user_id = ? AND source_key = ?
		 ORDER BY revision DESC LIMIT 1`), userID, *ses.SourceKey).Scan(&curSessionID, &curRev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	nextRev := int64(1)
	status := "inserted"
	if err == nil {
		nextRev = curRev + 1
		status = "superseded"
	}

	if err := insertSessionTx(ctx, tx, userID, ses, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO session_supersessions (user_id, source_key, revision, session_id, received_at)
		 VALUES (?, ?, ?, ?, ?)`), userID, *ses.SourceKey, nextRev, ses.SessionID, now); err != nil {
		return "", err
	}
	return status, nil
}

func insertSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, now time.Time) error {
	_, err := tx.ExecContext(ctx, q(
		`INSERT INTO sessions (user_id, session_id, work_id, edition_sha, device_id,
		                       started_at, ended_at, start_prog, end_prog, idle_ms, active_ms, reported_pages,
		                       origin, origin_alias, source_key, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, session_id) DO NOTHING`),
		userID, ses.SessionID, ses.WorkID, ses.EditionSHA, ses.DeviceID,
		ses.StartedAt.UTC(), ses.EndedAt.UTC(),
		ses.StartProg, ses.EndProg, ses.IdleMs, ses.ActiveMs, ses.ReportedPages,
		string(ses.Origin), ses.OriginAlias, ses.SourceKey, now)
	return err
}
