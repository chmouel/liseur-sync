package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const annotationCols = `user_id, id, seq, rev, work_id, edition_sha, kind, locator_json,
                        progression, excerpt, color, body, device_id, client_ts,
                        updated_at, deleted_at`

// scanAnnotation scans one row of the annotations table.
func scanAnnotation(row interface{ Scan(...any) error }) (store.Annotation, error) {
	var a store.Annotation
	var editionSHA, clientTS, deletedAt sql.NullString
	var progression sql.NullFloat64
	var kind, updatedAt string
	err := row.Scan(&a.UserID, &a.ID, &a.Seq, &a.Rev, &a.WorkID, &editionSHA,
		&kind, &a.LocatorJSON, &progression, &a.Excerpt, &a.Color, &a.Body,
		&a.DeviceID, &clientTS, &updatedAt, &deletedAt)
	if err != nil {
		return a, err
	}
	a.Kind = store.AnnotationKind(kind)
	if editionSHA.Valid {
		a.EditionSHA = &editionSHA.String
	}
	if progression.Valid {
		a.Progression = &progression.Float64
	}
	if clientTS.Valid {
		if a.ClientTS, err = parseTime(clientTS.String); err != nil {
			return a, err
		}
	}
	if a.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return a, err
	}
	if a.DeletedAt, err = parseTimePtr(deletedAt); err != nil {
		return a, err
	}
	return a, nil
}

// nextAnnotationSeqTx advances the per-user annotation counter — a
// second counter beside the op one, never shared with it. Advanced
// inside the write's transaction, holding the counter row, so seq
// order is commit order per user.
func nextAnnotationSeqTx(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx,
		`INSERT INTO seq_counters (user_id, next_annotation_seq) VALUES (?, 2)
		 ON CONFLICT(user_id) DO UPDATE SET next_annotation_seq = next_annotation_seq + 1
		 RETURNING next_annotation_seq - 1`, userID).Scan(&seq)
	return seq, err
}

func annotationByIDTx(ctx context.Context, tx *sql.Tx, userID, id string) (store.Annotation, bool, error) {
	a, err := scanAnnotation(tx.QueryRowContext(ctx,
		`SELECT `+annotationCols+` FROM annotations WHERE user_id = ? AND id = ?`,
		userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

func liveAnnotationCountTx(ctx context.Context, tx *sql.Tx, userID, workID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM annotations
		 WHERE user_id = ? AND work_id = ? AND deleted_at IS NULL`,
		userID, workID).Scan(&n)
	return n, err
}

// PushAnnotations applies a batch of annotation writes, never
// atomically: one result per item, one bad item fails alone.
// Compare-and-set on rev; a retry of an accepted write (same id, same
// base rev, byte-identical payload) is acknowledged, not conflicted.
func (s *Store) PushAnnotations(ctx context.Context, userID, deviceID string, items []store.AnnotationWrite, maxLivePerWork int) ([]store.AnnotationResult, error) {
	results := make([]store.AnnotationResult, len(items))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		now := time.Now()
		for i, item := range items {
			r, err := pushAnnotationTx(ctx, tx, userID, deviceID, item, maxLivePerWork, now)
			if err != nil {
				return err
			}
			results[i] = r
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func pushAnnotationTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, item store.AnnotationWrite, maxLivePerWork int, now time.Time) (store.AnnotationResult, error) {
	r := store.AnnotationResult{ID: item.ID, Status: "applied"}
	stored, found, err := annotationByIDTx(ctx, tx, userID, item.ID)
	if err != nil {
		return r, err
	}
	if found {
		switch {
		case stored.Rev == item.BaseRev+1 && store.SameAnnotationPayload(stored, item, deviceID):
			// The accepted write, pushed again: a lost response is
			// harmless.
			r.Status, r.Rev, r.Seq = "duplicate", stored.Rev, stored.Seq
			return r, nil
		case stored.Rev != item.BaseRev || stored.WorkID != item.WorkID:
			// Stale rev — or a record split/merge moved to another
			// work. Either way the client resolves from the server
			// copy; the server orders, it never merges.
			r.Status, r.Server = "conflict", &stored
			return r, nil
		}
	}
	if !found || stored.Deleted() {
		// A create — or a deliberate, rev-matching write onto a
		// tombstone — adds a live record, so it answers to the cap.
		if invalid, err := refuseAnnotationInsertTx(ctx, tx, userID, item, maxLivePerWork); err != nil {
			return r, err
		} else if invalid != "" {
			r.Status, r.Reason = "invalid", invalid
			return r, nil
		}
	} else {
		// A live edit keeps its work (a mismatch was a conflict
		// above), but its edition reference still has to exist —
		// checked here so the composite FK never aborts the batch.
		if invalid, err := refuseAnnotationEditionTx(ctx, tx, userID, item); err != nil {
			return r, err
		} else if invalid != "" {
			r.Status, r.Reason = "invalid", invalid
			return r, nil
		}
	}
	seq, err := nextAnnotationSeqTx(ctx, tx, userID)
	if err != nil {
		return r, err
	}
	// An unknown id — including one whose tombstone was swept — starts
	// over: new record, rev 1, whatever base the client quoted.
	rev := int64(1)
	if found {
		rev = item.BaseRev + 1
		_, err = tx.ExecContext(ctx,
			`UPDATE annotations SET seq = ?, rev = ?, work_id = ?, edition_sha = ?,
			        kind = ?, locator_json = ?, progression = ?, excerpt = ?,
			        color = ?, body = ?, device_id = ?, client_ts = ?,
			        updated_at = ?, deleted_at = NULL
			 WHERE user_id = ? AND id = ?`,
			seq, rev, item.WorkID, nullStr(item.EditionSHA), string(item.Kind),
			item.LocatorJSON, nullFloat(item.Progression), item.Excerpt,
			item.Color, item.Body, deviceID, formatTime(item.ClientTS),
			formatTime(now), userID, item.ID)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO annotations (user_id, id, seq, rev, work_id, edition_sha,
			         kind, locator_json, progression, excerpt, color, body,
			         device_id, client_ts, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, item.ID, seq, rev, item.WorkID, nullStr(item.EditionSHA),
			string(item.Kind), item.LocatorJSON, nullFloat(item.Progression),
			item.Excerpt, item.Color, item.Body, deviceID,
			formatTime(item.ClientTS), formatTime(now))
	}
	if err != nil {
		return r, err
	}
	r.Rev, r.Seq = rev, seq
	return r, nil
}

// refuseAnnotationInsertTx is every reason a new live record cannot
// land, checked inside the transaction rather than left to a foreign
// key: a constraint violation would abort the batch, and one bad item
// must fail alone. The cap especially belongs here — two concurrent
// creates must not both squeeze under it.
func refuseAnnotationInsertTx(ctx context.Context, tx *sql.Tx, userID string, item store.AnnotationWrite, maxLivePerWork int) (string, error) {
	var dummy int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM works WHERE user_id = ? AND id = ?`, userID, item.WorkID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown work", nil
	}
	if err != nil {
		return "", err
	}
	if item.EditionSHA != nil {
		if invalid, err := refuseAnnotationEditionTx(ctx, tx, userID, item); err != nil {
			return "", err
		} else if invalid != "" {
			return invalid, nil
		}
	}
	n, err := liveAnnotationCountTx(ctx, tx, userID, item.WorkID)
	if err != nil {
		return "", err
	}
	if n >= maxLivePerWork {
		return "annotation cap reached for this work", nil
	}
	return "", nil
}

// refuseAnnotationEditionTx says whether the write names an edition
// this work does not have. Checked for every accepted write, not only
// a create: a live edit can change edition_sha too, and the composite
// FK aborting the transaction would fail the whole batch.
func refuseAnnotationEditionTx(ctx context.Context, tx *sql.Tx, userID string, item store.AnnotationWrite) (string, error) {
	if item.EditionSHA == nil {
		return "", nil
	}
	var dummy int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM editions WHERE user_id = ? AND sha256 = ? AND work_id = ?`,
		userID, *item.EditionSHA, item.WorkID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown edition for this work", nil
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

// DeleteAnnotation writes the tombstone iff rev matches. The tombstone
// keeps nothing but identity, rev, seq and when.
func (s *Store) DeleteAnnotation(ctx context.Context, userID, id string, rev int64) (store.AnnotationResult, error) {
	r := store.AnnotationResult{ID: id, Status: "applied"}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		stored, found, err := annotationByIDTx(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if !found {
			return store.ErrNotFound
		}
		if stored.Deleted() {
			r.Status, r.Rev, r.Seq = "duplicate", stored.Rev, stored.Seq
			return nil
		}
		if stored.Rev != rev {
			r.Status, r.Server = "conflict", &stored
			return nil
		}
		seq, err := nextAnnotationSeqTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		now := formatTime(time.Now())
		if _, err := tx.ExecContext(ctx,
			`UPDATE annotations SET seq = ?, rev = rev + 1, edition_sha = NULL,
			        locator_json = NULL, progression = NULL, excerpt = '',
			        color = '', body = '', device_id = '', client_ts = NULL,
			        updated_at = ?, deleted_at = ?
			 WHERE user_id = ? AND id = ?`,
			seq, now, now, userID, id); err != nil {
			return err
		}
		r.Rev, r.Seq = stored.Rev+1, seq
		return nil
	})
	if err != nil {
		return store.AnnotationResult{}, err
	}
	return r, nil
}

// AnnotationChanges returns records (tombstones included) with
// seq > since, in seq order. This is a state feed, not history: an
// edited record reappears at the head with its current content.
func (s *Store) AnnotationChanges(ctx context.Context, userID string, since int64, limit int) (store.AnnotationChangesPage, error) {
	var page store.AnnotationChangesPage
	err := s.db.QueryRowContext(ctx,
		`SELECT next_annotation_seq - 1 FROM seq_counters WHERE user_id = ?`,
		userID).Scan(&page.HighWater)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationCols+` FROM annotations
		 WHERE user_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		userID, since, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return page, err
		}
		page.Annotations = append(page.Annotations, a)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Annotations) > limit {
		page.HasMore = true
		page.Annotations = page.Annotations[:limit]
	}
	return page, nil
}

// WorkAnnotations returns the live set for one work, ordered by
// progression then client_ts.
func (s *Store) WorkAnnotations(ctx context.Context, userID, workID string) ([]store.Annotation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationCols+` FROM annotations
		 WHERE user_id = ? AND work_id = ? AND deleted_at IS NULL
		 ORDER BY progression IS NULL, progression, client_ts, id`,
		userID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Annotation
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SweepAnnotationTombstones removes tombstones older than the cutoff.
// After the sweep the id is simply unknown: a device offline longer
// than the window that pushes it again creates a new record.
func (s *Store) SweepAnnotationTombstones(ctx context.Context, userID string, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM annotations
		 WHERE user_id = ? AND deleted_at IS NOT NULL AND deleted_at < ?`,
		userID, formatTime(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// moveAnnotationsTx reassigns annotations during a split or a merge.
// A moved annotation is a write like any other — new seq, rev
// incremented — so every synced device learns its new work_id on the
// next pull. editionSHA nil moves the whole work's records (a merge);
// non-nil moves only the edition's (a split — records with no edition
// stay with the surviving work).
func moveAnnotationsTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, toWorkID string, editionSHA *string) error {
	query := `SELECT id FROM annotations WHERE user_id = ? AND work_id = ?`
	args := []any{userID, fromWorkID}
	if editionSHA != nil {
		query += ` AND edition_sha = ?`
		args = append(args, *editionSHA)
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY seq`, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := formatTime(time.Now())
	for _, id := range ids {
		seq, err := nextAnnotationSeqTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE annotations SET work_id = ?, seq = ?, rev = rev + 1, updated_at = ?
			 WHERE user_id = ? AND id = ?`,
			toWorkID, seq, now, userID, id); err != nil {
			return err
		}
	}
	return nil
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}
