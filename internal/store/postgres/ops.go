package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const opCols = `user_id, seq, op_id, work_id, edition_sha, device_id, client_ts,
                progression, locator_json, foreign_pos, origin, origin_alias, received_at`

func (s *Store) AppendOps(ctx context.Context, userID, deviceID string, ops []store.Op) ([]store.OpResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	results := make([]store.OpResult, len(ops))
	now := time.Now().UTC()

	for i, o := range ops {
		results[i] = store.OpResult{OpID: o.OpID, Status: "applied"}

		var existSeq int64
		var existFound bool
		err := tx.QueryRowContext(ctx, q(
			`SELECT seq FROM ops WHERE user_id = ? AND op_id = ?`), userID, o.OpID).Scan(&existSeq)
		switch {
		case err == nil:
			existFound = true
		case errors.Is(err, sql.ErrNoRows):
		default:
			return nil, err
		}
		if existFound {
			same, err := sameOp(ctx, tx, userID, o, deviceID)
			if err != nil {
				return nil, err
			}
			if same {
				results[i].Status = "duplicate"
				results[i].Seq = existSeq
			} else {
				results[i].Status = "conflict"
				results[i].Reason = "op_id reused with a different payload"
			}
			continue
		}

		var seq int64
		err = tx.QueryRowContext(ctx, q(
			`INSERT INTO seq_counters (user_id, next_seq) VALUES (?, 2)
			 ON CONFLICT(user_id) DO UPDATE SET next_seq = seq_counters.next_seq + 1
			 RETURNING next_seq - 1`), userID).Scan(&seq)
		if err != nil {
			return nil, err
		}

		_, err = tx.ExecContext(ctx, q(
			`INSERT INTO ops (user_id, seq, op_id, work_id, edition_sha, device_id,
			                  client_ts, progression, locator_json, foreign_pos,
			                  origin, origin_alias, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			userID, seq, o.OpID, o.WorkID, o.EditionSHA, deviceID,
			o.ClientTS.UTC(), o.Progression, o.LocatorJSON,
			o.ForeignPos, string(o.Origin), o.OriginAlias, now)
		if err != nil {
			return nil, err
		}
		results[i].Seq = seq
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func sameOp(ctx context.Context, tx *sql.Tx, userID string, o store.Op, deviceID string) (bool, error) {
	var (
		workID, devID, origin string
		clientTS              time.Time
		prog                  float64
		locator               []byte
		edSHA, fpos, oalias   *string
	)
	err := tx.QueryRowContext(ctx, q(
		`SELECT work_id, edition_sha, device_id, client_ts, progression, locator_json, foreign_pos, origin, origin_alias
		 FROM ops WHERE user_id = ? AND op_id = ?`), userID, o.OpID).
		Scan(&workID, &edSHA, &devID, &clientTS, &prog, &locator, &fpos, &origin, &oalias)
	if err != nil {
		return false, err
	}
	if workID != o.WorkID || devID != deviceID || prog != o.Progression ||
		!clientTS.Equal(o.ClientTS.UTC()) || origin != string(o.Origin) {
		return false, nil
	}
	if (edSHA == nil) != (o.EditionSHA == nil) || (edSHA != nil && *edSHA != *o.EditionSHA) {
		return false, nil
	}
	if (fpos == nil) != (o.ForeignPos == nil) || (fpos != nil && *fpos != *o.ForeignPos) {
		return false, nil
	}
	if (oalias == nil) != (o.OriginAlias == nil) || (oalias != nil && *oalias != *o.OriginAlias) {
		return false, nil
	}
	if string(locator) != string(o.LocatorJSON) {
		return false, nil
	}
	return true, nil
}

func (s *Store) Changes(ctx context.Context, userID string, since int64, limit int) (store.ChangesPage, error) {
	var page store.ChangesPage
	if err := s.db.QueryRowContext(ctx, q(
		`SELECT COALESCE(MAX(seq), 0) FROM ops WHERE user_id = ?`), userID).Scan(&page.HighWater); err != nil {
		return page, err
	}
	var horizon int64
	err := s.db.QueryRowContext(ctx, q(
		`SELECT horizon FROM compaction_state WHERE user_id = ?`), userID).Scan(&horizon)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	if since < horizon {
		page.ResyncNeeded = true
		return page, nil
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+opCols+` FROM ops WHERE user_id = ? AND seq > ? ORDER BY seq LIMIT ?`),
		userID, since, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return page, err
		}
		page.Ops = append(page.Ops, o)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Ops) > limit {
		page.HasMore = true
		page.Ops = page.Ops[:limit]
	}
	return page, nil
}

func (s *Store) Positions(ctx context.Context, userID, workID string, limit int) ([]store.Op, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+opCols+` FROM ops WHERE user_id = ? AND work_id = ? ORDER BY seq DESC LIMIT ?`),
		userID, workID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Op
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) HeadsFor(ctx context.Context, userID string) (store.Heads, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return store.Heads{}, err
	}
	defer tx.Rollback()
	var h store.Heads
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COALESCE(MAX(seq), 0) FROM ops WHERE user_id = ?`), userID).Scan(&h.SnapshotSeq); err != nil {
		return h, err
	}
	rows, err := tx.QueryContext(ctx, q(
		`SELECT DISTINCT ON (work_id, device_id) `+opCols+` FROM ops
		 WHERE user_id = ? ORDER BY work_id, device_id, seq DESC`), userID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return h, err
		}
		h.Ops = append(h.Ops, o)
	}
	return h, rows.Err()
}

func (s *Store) CompactionHorizon(ctx context.Context, userID string) (int64, error) {
	var h int64
	err := s.db.QueryRowContext(ctx, q(
		`SELECT horizon FROM compaction_state WHERE user_id = ?`), userID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return h, err
}

// OpsBefore returns all ops received before the given time.
func (s *Store) OpsBefore(ctx context.Context, userID string, before time.Time) ([]store.Op, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+opCols+` FROM ops WHERE user_id = ? AND received_at < ?
		 ORDER BY device_id, work_id, received_at, seq`),
		userID, before.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Op
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Compact: see the SQLite implementation's doc comment.
func (s *Store) Compact(ctx context.Context, userID string, olderThan time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cut := olderThan.UTC()

	rows, err := tx.QueryContext(ctx, q(
		`DELETE FROM ops
		 WHERE user_id = ? AND received_at < ?
		 AND seq NOT IN (
		     SELECT MAX(seq) FROM ops o2
		     WHERE o2.user_id = ops.user_id AND o2.received_at < ?
		     GROUP BY o2.work_id, o2.device_id, o2.received_at::date)
		 AND seq NOT IN (
		     SELECT MAX(seq) FROM ops o3
		     WHERE o3.user_id = ops.user_id
		     GROUP BY o3.work_id, o3.device_id)
		 RETURNING seq`), userID, cut, cut)
	if err != nil {
		return 0, err
	}
	var horizon int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return 0, err
		}
		if seq > horizon {
			horizon = seq
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()
	if horizon == 0 {
		return 0, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO compaction_state (user_id, horizon) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET horizon = excluded.horizon`),
		userID, horizon); err != nil {
		return 0, err
	}
	return horizon, tx.Commit()
}
