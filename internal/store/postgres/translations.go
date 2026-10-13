package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// workTranslationCols is the column list a pull reads. The push does
// not share it: it writes every column by name and reads only the one it
// compares, so the two cannot drift in a way a scan would hide.
const workTranslationCols = `key, translation, device_id, updated_at`

// scanWorkTranslation scans one row.
func scanWorkTranslation(row interface{ Scan(...any) error }) (store.WorkTranslation, error) {
	var t store.WorkTranslation
	if err := row.Scan(&t.Key, &t.Translation, &t.DeviceID, &t.UpdatedAt); err != nil {
		return t, err
	}
	return t, nil
}

// stampWorkTranslationTx returns the stamp a write under [tx] carries:
// the clock's now, unless the user's high-water mark stands at or
// beyond it — a clock stepped back, or two writes within one tick — in
// which case just past the mark. A stamp behind one a pull already
// returned would hide the row behind that pull's cursor for good, so
// stamps only ever move forward. The mark is its own row rather than
// the rows' maximum: the rows are a cache that eviction and work
// deletion take freely, and a mark that left with them would let a
// stepped-back clock write under a cursor another work's pull still
// holds. On first use the mark is seeded from the rows, for databases
// that grew before it existed.
//
// The comparison happens at PostgreSQL's own precision: now is
// truncated to the microsecond first, and the nudge past the mark is
// two microseconds — a smaller step can round back to the very stamp
// it is meant to pass.
func stampWorkTranslationTx(ctx context.Context, tx *sql.Tx, userID string, now time.Time) (time.Time, error) {
	now = now.UTC().Truncate(time.Microsecond)
	var high sql.NullTime
	err := tx.QueryRowContext(ctx, q(
		`SELECT high FROM work_translation_stamps WHERE user_id = ?`), userID).
		Scan(&high)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return now, err
	}
	if !high.Valid {
		// Seeded from the rows, not trusted again after that.
		if err := tx.QueryRowContext(ctx, q(
			`SELECT MAX(updated_at) FROM work_translations WHERE user_id = ?`), userID).
			Scan(&high); err != nil {
			return now, err
		}
	}
	if high.Valid && !now.After(high.Time) {
		now = high.Time.Add(2 * time.Microsecond)
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO work_translation_stamps (user_id, high) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET high = excluded.high`),
		userID, now); err != nil {
		return now, err
	}
	return now, nil
}

// PushWorkTranslations applies one batch atomically: insert if absent,
// update only when the text differs, evict over budget. See the Store
// interface for the contract.
func (s *Store) PushWorkTranslations(
	ctx context.Context, userID, workID, deviceID string,
	items []store.WorkTranslationWrite, budgetBytes int64,
) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		if exists, err := workExistsTx(ctx, tx, userID, workID); err != nil {
			return err
		} else if !exists {
			// The work is named by the path, not by an item, so the
			// refusal names the batch's first key: enough for a client
			// to log which push to set aside, and the work_id is what
			// it actually acts on.
			id := ""
			if len(items) > 0 {
				id = items[0].Key
			}
			return store.UnknownWork("translation", id, 0, workID)
		}
		now, err := stampWorkTranslationTx(ctx, tx, userID, time.Now())
		if err != nil {
			return err
		}
		for _, item := range items {
			var stored string
			err := tx.QueryRowContext(ctx, q(
				`SELECT translation FROM work_translations
				 WHERE user_id = ? AND work_id = ? AND key = ?`),
				userID, workID, item.Key).Scan(&stored)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				if _, err := tx.ExecContext(ctx, q(
					`INSERT INTO work_translations (user_id, work_id, key, translation,
					         device_id, updated_at)
					 VALUES (?, ?, ?, ?, ?, ?)`),
					userID, workID, item.Key, item.Translation, deviceID, now); err != nil {
					return err
				}
			case err != nil:
				return err
			case stored != item.Translation:
				// Last write wins, and only a real change is a write:
				// an identical push touches nothing, so updated_at does
				// not move and no puller ever re-downloads a row it
				// already holds.
				if _, err := tx.ExecContext(ctx, q(
					`UPDATE work_translations SET translation = ?, device_id = ?, updated_at = ?
					 WHERE user_id = ? AND work_id = ? AND key = ?`),
					item.Translation, deviceID, now, userID, workID, item.Key); err != nil {
					return err
				}
			}
		}
		return evictWorkTranslationsTx(ctx, tx, userID, budgetBytes)
	})
}

// evictWorkTranslationsTx enforces the per-user byte budget, evicting
// the oldest-updated rows first. Inside the push transaction, so two
// concurrent pushes cannot both spend the same headroom; and after the
// inserts, so a fresh row an eviction takes was the oldest thing there.
// A page of the oldest rows, ctid and length only, deleted by ctid: the
// delete cannot race a re-read of the same ordering, and a budget far
// over draws a handful of pages rather than one query per row.
func evictWorkTranslationsTx(ctx context.Context, tx *sql.Tx, userID string, budgetBytes int64) error {
	if budgetBytes <= 0 {
		return nil
	}
	for {
		var total sql.NullInt64
		// The budget is bytes and so is the measure: LENGTH counts
		// characters, OCTET_LENGTH what is actually stored.
		if err := tx.QueryRowContext(ctx, q(
			`SELECT SUM(OCTET_LENGTH(translation)) FROM work_translations WHERE user_id = ?`),
			userID).Scan(&total); err != nil {
			return err
		}
		excess := total.Int64 - budgetBytes
		if excess <= 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, q(
			`SELECT ctid::text, OCTET_LENGTH(translation) FROM work_translations
			 WHERE user_id = ? ORDER BY updated_at, key LIMIT 100`),
			userID)
		if err != nil {
			return err
		}
		type victim struct {
			id    string
			bytes int64
		}
		var victims []victim
		var freed int64
		for rows.Next() {
			var v victim
			if err := rows.Scan(&v.id, &v.bytes); err != nil {
				rows.Close()
				return err
			}
			victims = append(victims, v)
			freed += v.bytes
			if freed >= excess {
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(victims) == 0 {
			return nil
		}
		for _, v := range victims {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM work_translations WHERE ctid = $1::tid`, v.id); err != nil {
				return err
			}
		}
	}
}

// PullWorkTranslations returns one page of a work's rows, strictly after
// the cursor, in (updated_at, key) order. See the Store interface for
// the contract.
func (s *Store) PullWorkTranslations(
	ctx context.Context, userID, workID, after string, limit int,
) (store.WorkTranslationPage, error) {
	var page store.WorkTranslationPage
	var one int
	err := s.db.QueryRowContext(ctx, q(
		`SELECT 1 FROM works WHERE user_id = ? AND id = ?`), userID, workID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return page, store.ErrNotFound
	}
	if err != nil {
		return page, err
	}
	at, key, ok := store.SplitWorkTranslationCursor(after)
	if after != "" && !ok {
		return page, store.ErrInvalidInput
	}
	query := `SELECT ` + workTranslationCols + ` FROM work_translations
	          WHERE user_id = ? AND work_id = ?`
	args := []any{userID, workID}
	if after != "" {
		// The cursor's stamp is the fixed-width UTC spelling, so it
		// casts to timestamptz losslessly and the tie-break on the key
		// keeps equal timestamps from skipping or repeating a row.
		stamp := at.UTC().Format(store.WorkTranslationTimeFormat)
		query += ` AND (updated_at > ?::timestamptz OR (updated_at = ?::timestamptz AND key > ?))`
		args = append(args, stamp, stamp, key)
	}
	query += ` ORDER BY updated_at, key LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanWorkTranslation(rows)
		if err != nil {
			return page, err
		}
		page.Translations = append(page.Translations, t)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Translations) > limit {
		page.HasMore = true
		page.Translations = page.Translations[:limit]
	}
	if n := len(page.Translations); n > 0 {
		page.NextCursor = store.WorkTranslationCursor(page.Translations[n-1])
	}
	return page, nil
}

// moveWorkTranslationsTx carries one work's cached sentence
// translations to another work, inside a merge. Key collisions keep the
// survivor's row; what moves is stamped beyond anything the user holds,
// so a pull cursor on the survivor discovers it. A split does not call
// this: a translation is keyed by content, not by edition, so it stays
// with the work it was pushed to.
func moveWorkTranslationsTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM work_translations WHERE user_id = ? AND work_id = ? AND key IN
		   (SELECT key FROM work_translations WHERE user_id = ? AND work_id = ?)`),
		userID, fromWorkID, userID, intoWorkID); err != nil {
		return err
	}
	stamped, err := stampWorkTranslationTx(ctx, tx, userID, time.Now())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, q(
		`UPDATE work_translations SET work_id = ?, updated_at = ? WHERE user_id = ? AND work_id = ?`),
		intoWorkID, stamped, userID, fromWorkID)
	return err
}
