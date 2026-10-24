package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateKopluginDevice(ctx context.Context, d store.KopluginDevice) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO koplugin_devices (id, user_id, token_sha256, label, device_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.TokenSHA256, d.Label, d.DeviceID, formatTime(d.CreatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) KopluginDeviceByToken(ctx context.Context, tokenSHA256 string) (store.KopluginDevice, error) {
	var d store.KopluginDevice
	var created string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_sha256, label, device_id, created_at, revoked_at
		 FROM koplugin_devices WHERE token_sha256 = ?
		   AND EXISTS (SELECT 1 FROM users u
		               WHERE u.id = koplugin_devices.user_id AND u.disabled_at IS NULL)`, tokenSHA256).
		Scan(&d.ID, &d.UserID, &d.TokenSHA256, &d.Label, &d.DeviceID, &created, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return d, store.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if d.CreatedAt, err = parseTime(created); err != nil {
		return d, err
	}
	if revoked.Valid {
		t, err := parseTime(revoked.String)
		if err != nil {
			return d, err
		}
		d.RevokedAt = &t
	}
	return d, nil
}

// UpsertKopluginSession implements the legacy upsert on
// (device_id, book_md5, page, start_time) within the append-only
// model. ses.SourceKey is the legacy key; ses.SessionID is the
// caller-derived deterministic UUIDv5 of key + payload hash.
//
// Returns "inserted" | "duplicate" | "superseded".
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
	if err := s.commitSessions(tx, userID, status == "inserted" || status == "superseded"); err != nil {
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
	if err := s.commitSessions(tx, userID, status == "inserted" || status == "superseded"); err != nil {
		return "", err
	}
	return status, nil
}

func upsertKopluginSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session) (string, error) {
	if ses.ReportedPages == nil {
		one := 1.0
		ses.ReportedPages = &one
	}
	return appendSourceSessionTx(ctx, tx, userID, ses, nil)
}

func insertSessionTx(ctx context.Context, tx *sql.Tx, userID string, ses store.Session, now string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (user_id, session_id, work_id, edition_sha, device_id,
		                       started_at, ended_at, start_prog, end_prog, idle_ms, active_ms, reported_pages,
		                       origin, origin_alias, source_key, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, ses.SessionID, ses.WorkID, nullStr(ses.EditionSHA), ses.DeviceID,
		formatTime(ses.StartedAt), formatTime(ses.EndedAt),
		ses.StartProg, ses.EndProg, ses.IdleMs, ses.ActiveMs, ses.ReportedPages,
		string(ses.Origin), nullStr(ses.OriginAlias), nullStr(ses.SourceKey), now)
	if isUniqueErr(err) {
		// Same session_id inserted before: treat as duplicate of that
		// revision (identical re-upload of a superseded payload).
		return nil
	}
	return err
}
