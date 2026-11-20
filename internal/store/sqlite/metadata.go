package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) WriteWorkMetadata(ctx context.Context, userID, workID string, item store.WorkMetadataWrite) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		if exists, err := workExistsTx(ctx, tx, userID, workID); err != nil {
			return err
		} else if !exists {
			return store.ErrNotFound
		}
		sum := sha256.Sum256([]byte(item.WriterID + "\x00" + strconv.FormatInt(item.Revision, 10) + "\x00" + item.Field + "\x00" + item.Value))
		fingerprint := hex.EncodeToString(sum[:])
		var priorWork, priorFingerprint string
		err := tx.QueryRowContext(ctx, `SELECT work_id, fingerprint FROM metadata_override_receipts WHERE user_id = ? AND write_id = ?`, userID, item.ID).Scan(&priorWork, &priorFingerprint)
		switch {
		case err == nil:
			if priorWork != workID || priorFingerprint != fingerprint {
				return store.ErrIDMismatch
			}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var superseded bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM metadata_override_receipts WHERE user_id = ? AND work_id = ? AND writer_id = ? AND field = ? AND revision > ?)`, userID, workID, item.WriterID, item.Field, item.Revision).Scan(&superseded); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_override_receipts (user_id, write_id, work_id, fingerprint, writer_id, field, revision) VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, item.ID, workID, fingerprint, item.WriterID, item.Field, item.Revision); err != nil {
			return err
		}
		// A request may first arrive after a newer edit, even after their works merge.
		if superseded {
			return nil
		}
		if item.Value == "null" {
			_, err = tx.ExecContext(ctx, `DELETE FROM work_metadata_overrides WHERE user_id = ? AND work_id = ? AND field = ?`, userID, workID, item.Field)
			return err
		}
		stamped, err := stampWorkTranslationTx(ctx, tx, userID, time.Now())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO work_metadata_overrides (user_id, work_id, field, value, write_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)
   ON CONFLICT (user_id, work_id, field) DO UPDATE SET value = excluded.value, write_id = excluded.write_id, updated_at = excluded.updated_at`, userID, workID, item.Field, item.Value, item.ID, stamped)
		return err
	})
}

func (s *Store) ReadWorkMetadata(ctx context.Context, userID, workID string) (store.WorkMetadata, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM works WHERE user_id = ? AND id = ?`, userID, workID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.WorkMetadata{}, store.ErrNotFound
		}
		return store.WorkMetadata{}, err
	}
	result := store.WorkMetadata{Fields: []store.WorkMetadataField{}}
	rows, err := s.db.QueryContext(ctx, `SELECT field, value, write_id, updated_at FROM work_metadata_overrides WHERE user_id = ? AND work_id = ? ORDER BY field`, userID, workID)
	if err != nil {
		return store.WorkMetadata{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var field store.WorkMetadataField
		var stamp string
		if err := rows.Scan(&field.Field, &field.Value, &field.ID, &stamp); err != nil {
			return store.WorkMetadata{}, err
		}
		if field.UpdatedAt, err = parseTime(stamp); err != nil {
			return store.WorkMetadata{}, err
		}
		result.Fields = append(result.Fields, field)
	}
	if err := rows.Err(); err != nil {
		return store.WorkMetadata{}, err
	}

	var digest sql.NullString
	var stamp string
	err = s.db.QueryRowContext(ctx, `SELECT sha256, updated_at FROM work_cover_overrides WHERE user_id = ? AND work_id = ?`, userID, workID).Scan(&digest, &stamp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result, nil
	case err != nil:
		return store.WorkMetadata{}, err
	}
	cover := store.WorkCoverOverride{SHA256: digest.String}
	if cover.UpdatedAt, err = parseTime(stamp); err != nil {
		return store.WorkMetadata{}, err
	}
	result.Cover = &cover
	return result, nil
}

func (s *Store) PutWorkCover(ctx context.Context, userID, workID, digest string, data []byte) (time.Time, error) {
	var updatedAt time.Time
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		if exists, err := workExistsTx(ctx, tx, userID, workID); err != nil {
			return err
		} else if !exists {
			return store.ErrNotFound
		}
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_cover_overrides (user_id, work_id, sha256, data, updated_at) VALUES (?, ?, ?, ?, ?)
   ON CONFLICT (user_id, work_id) DO UPDATE SET sha256 = excluded.sha256, data = excluded.data, updated_at = excluded.updated_at`, userID, workID, digest, data, formatTime(now)); err != nil {
			return err
		}
		updatedAt = now
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return updatedAt, nil
}

func (s *Store) DeleteWorkCover(ctx context.Context, userID, workID string) (time.Time, error) {
	var updatedAt time.Time
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		if exists, err := workExistsTx(ctx, tx, userID, workID); err != nil {
			return err
		} else if !exists {
			return store.ErrNotFound
		}
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_cover_overrides (user_id, work_id, sha256, data, updated_at) VALUES (?, ?, NULL, NULL, ?)
   ON CONFLICT (user_id, work_id) DO UPDATE SET sha256 = NULL, data = NULL, updated_at = excluded.updated_at`, userID, workID, formatTime(now)); err != nil {
			return err
		}
		updatedAt = now
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return updatedAt, nil
}

func (s *Store) WorkCover(ctx context.Context, userID, workID string) (store.WorkCoverOverride, error) {
	var digest sql.NullString
	var data []byte
	var stamp string
	err := s.db.QueryRowContext(ctx, `SELECT sha256, data, updated_at FROM work_cover_overrides WHERE user_id = ? AND work_id = ?`, userID, workID).Scan(&digest, &data, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return store.WorkCoverOverride{}, store.ErrNotFound
	}
	if err != nil {
		return store.WorkCoverOverride{}, err
	}
	if !digest.Valid || len(data) == 0 {
		return store.WorkCoverOverride{}, store.ErrNotFound
	}
	cover := store.WorkCoverOverride{SHA256: digest.String, Data: data}
	if cover.UpdatedAt, err = parseTime(stamp); err != nil {
		return store.WorkCoverOverride{}, err
	}
	return cover, nil
}

// moveWorkMetadataOverridesTx carries a reader's overrides onto the
// survivor of a merge, exactly as moveWorkEvaluationsTx does for ratings and
// reviews: a head superseded by a higher revision from the same writer keeps
// neither copy, and a field with no eligible head stops the merge.
func moveWorkMetadataOverridesTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT h.field, h.value, h.write_id, h.updated_at,
   EXISTS (SELECT 1 FROM metadata_override_receipts newer
    WHERE newer.user_id = h.user_id AND newer.work_id IN (?, ?)
     AND newer.writer_id = receipt.writer_id AND newer.field = h.field AND newer.revision > receipt.revision)
  FROM work_metadata_overrides h JOIN metadata_override_receipts receipt ON receipt.user_id = h.user_id AND receipt.write_id = h.write_id
  WHERE h.user_id = ? AND h.work_id IN (?, ?)`, fromWorkID, intoWorkID, userID, fromWorkID, intoWorkID)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	chosen := map[string]store.WorkMetadataField{}
	for rows.Next() {
		var field store.WorkMetadataField
		var superseded bool
		var stamp string
		if err := rows.Scan(&field.Field, &field.Value, &field.ID, &stamp, &superseded); err != nil {
			rows.Close()
			return err
		}
		if field.UpdatedAt, err = parseTime(stamp); err != nil {
			rows.Close()
			return err
		}
		present[field.Field] = true
		current, exists := chosen[field.Field]
		if !superseded && (!exists || field.UpdatedAt.After(current.UpdatedAt)) {
			chosen[field.Field] = field
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(chosen) != len(present) {
		return store.ErrMetadataMergeConflict
	}
	for _, field := range chosen {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_metadata_overrides (user_id, work_id, field, value, write_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)
   ON CONFLICT (user_id, work_id, field) DO UPDATE SET value = excluded.value, write_id = excluded.write_id, updated_at = excluded.updated_at`, userID, intoWorkID, field.Field, field.Value, field.ID, formatTime(field.UpdatedAt)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE metadata_override_receipts SET work_id = ? WHERE user_id = ? AND work_id = ?`, intoWorkID, userID, fromWorkID)
	return err
}

// moveWorkCoverOverrideTx moves a chosen cover the same way an Audiobookshelf
// link moves: the survivor's own wins, and a work that never had one takes the
// source's, tombstone included.
func moveWorkCoverOverrideTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO work_cover_overrides (user_id, work_id, sha256, data, updated_at)
   SELECT user_id, ?, sha256, data, updated_at FROM work_cover_overrides WHERE user_id = ? AND work_id = ?
   ON CONFLICT (user_id, work_id) DO NOTHING`, intoWorkID, userID, fromWorkID)
	return err
}
