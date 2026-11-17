package postgres

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

func (s *Store) WriteEvaluation(ctx context.Context, userID, workID string, item store.EvaluationWrite) error {
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
		err := tx.QueryRowContext(ctx, q(`SELECT work_id, fingerprint FROM evaluation_receipts WHERE user_id = ? AND write_id = ?`), userID, item.ID).Scan(&priorWork, &priorFingerprint)
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
		if err = tx.QueryRowContext(ctx, q(`SELECT EXISTS (SELECT 1 FROM evaluation_receipts WHERE user_id = ? AND work_id = ? AND writer_id = ? AND field = ? AND revision > ?)`), userID, workID, item.WriterID, item.Field, item.Revision).Scan(&superseded); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, q(`INSERT INTO evaluation_receipts (user_id, write_id, work_id, fingerprint, writer_id, field, revision) VALUES (?, ?, ?, ?, ?, ?, ?)`), userID, item.ID, workID, fingerprint, item.WriterID, item.Field, item.Revision); err != nil {
			return err
		}
		// A request may first arrive after a newer edit, even after their works merge.
		if superseded {
			return nil
		}
		stamped, err := stampWorkTranslationTx(ctx, tx, userID, time.Now())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, q(`INSERT INTO work_evaluations (user_id, work_id, field, value, write_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)
   ON CONFLICT (user_id, work_id, field) DO UPDATE SET value = excluded.value, write_id = excluded.write_id, updated_at = excluded.updated_at`), userID, workID, item.Field, item.Value, item.ID, stamped)
		return err
	})
}

func (s *Store) ReadEvaluation(ctx context.Context, userID, workID string) ([]store.EvaluationField, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, q(`SELECT 1 FROM works WHERE user_id = ? AND id = ?`), userID, workID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q(`SELECT field, value, write_id, updated_at FROM work_evaluations WHERE user_id = ? AND work_id = ? ORDER BY field`), userID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.EvaluationField{}
	for rows.Next() {
		var field store.EvaluationField
		if err := rows.Scan(&field.Field, &field.Value, &field.ID, &field.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, field)
	}
	return result, rows.Err()
}

func moveWorkEvaluationsTx(ctx context.Context, tx *sql.Tx, userID, fromWorkID, intoWorkID string) error {
	rows, err := tx.QueryContext(ctx, q(`SELECT h.field, h.value, h.write_id, h.updated_at,
   EXISTS (SELECT 1 FROM evaluation_receipts newer
    WHERE newer.user_id = h.user_id AND newer.work_id IN (?, ?)
     AND newer.writer_id = receipt.writer_id AND newer.field = h.field AND newer.revision > receipt.revision)
  FROM work_evaluations h JOIN evaluation_receipts receipt ON receipt.user_id = h.user_id AND receipt.write_id = h.write_id
  WHERE h.user_id = ? AND h.work_id IN (?, ?)`), fromWorkID, intoWorkID, userID, fromWorkID, intoWorkID)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	chosen := map[string]store.EvaluationField{}
	for rows.Next() {
		var field store.EvaluationField
		var superseded bool
		if err := rows.Scan(&field.Field, &field.Value, &field.ID, &field.UpdatedAt, &superseded); err != nil {
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
	// Receipts have no old values to recover if both heads are superseded.
	if len(chosen) != len(present) {
		return store.ErrEvaluationMergeConflict
	}
	for _, field := range chosen {
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO work_evaluations (user_id, work_id, field, value, write_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)
   ON CONFLICT (user_id, work_id, field) DO UPDATE SET value = excluded.value, write_id = excluded.write_id, updated_at = excluded.updated_at`), userID, intoWorkID, field.Field, field.Value, field.ID, field.UpdatedAt); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, q(`UPDATE evaluation_receipts SET work_id = ? WHERE user_id = ? AND work_id = ?`), intoWorkID, userID, fromWorkID)
	return err
}
