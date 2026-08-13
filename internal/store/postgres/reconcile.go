package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func scanBlobRecord(row interface{ Scan(...any) error }) (store.BlobRecord, error) {
	var record store.BlobRecord
	err := row.Scan(
		&record.SHA256, &record.SizeBytes,
		&record.OrphanedAt, &record.MissingAt)
	return record, err
}

func (s *Store) ListBlobRecords(
	ctx context.Context,
	afterSHA256 string,
	limit int,
) ([]store.BlobRecord, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if afterSHA256 != "" {
		if err := store.ValidateBlobInfo(store.BlobInfo{
			SHA256: afterSHA256,
		}); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT sha256, size_bytes, orphaned_at, missing_at
		 FROM blobs
		 WHERE sha256 > ?
		 ORDER BY sha256
		 LIMIT ?`),
		afterSHA256, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []store.BlobRecord
	for rows.Next() {
		record, err := scanBlobRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ReconcileBlob(
	ctx context.Context,
	blob store.BlobInfo,
	present bool,
	at time.Time,
) (store.BlobReconcileResult, error) {
	var result store.BlobReconcileResult
	if err := store.ValidateBlobInfo(blob); err != nil {
		return result, err
	}
	if at.IsZero() {
		return result, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if present {
		res, err := tx.ExecContext(ctx, q(
			`INSERT INTO blobs
			 (sha256, size_bytes, created_at, orphaned_at, missing_at)
			 VALUES (?, ?, ?, ?, NULL)
			 ON CONFLICT (sha256) DO NOTHING`),
			blob.SHA256, blob.SizeBytes, at.UTC(), at.UTC())
		if err != nil {
			return result, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return result, err
		} else if n == 1 {
			result.Inserted = true
			result.OrphanMarked = true
		}
	}
	record, err := scanBlobRecord(tx.QueryRowContext(ctx, q(
		`SELECT sha256, size_bytes, orphaned_at, missing_at
		 FROM blobs WHERE sha256 = ? FOR UPDATE`),
		blob.SHA256))
	if errors.Is(err, sql.ErrNoRows) {
		return result, store.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if record.SizeBytes != blob.SizeBytes {
		return result, store.ErrInvariantViolation
	}
	var references int64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(1) FROM book_files WHERE blob_sha256 = ?`),
		blob.SHA256).Scan(&references); err != nil {
		return result, err
	}
	orphanedAt := record.OrphanedAt
	missingAt := record.MissingAt
	if references == 0 && orphanedAt == nil {
		orphanedAt = &at
		result.OrphanMarked = true
	} else if references > 0 && orphanedAt != nil {
		orphanedAt = nil
		result.OrphanCleared = true
	}
	if present && missingAt != nil {
		missingAt = nil
		result.MissingCleared = true
	} else if !present && missingAt == nil {
		missingAt = &at
		result.MissingMarked = true
	}
	if result.OrphanMarked || result.OrphanCleared ||
		result.MissingMarked || result.MissingCleared {
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE blobs SET orphaned_at = ?, missing_at = ?
			 WHERE sha256 = ?`),
			orphanedAt, missingAt, blob.SHA256); err != nil {
			return result, err
		}
	}
	record.OrphanedAt = orphanedAt
	record.MissingAt = missingAt
	result.Record = record
	if err := tx.Commit(); err != nil {
		return store.BlobReconcileResult{}, err
	}
	return result, nil
}
