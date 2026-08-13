package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func scanBlobRecord(row interface{ Scan(...any) error }) (store.BlobRecord, error) {
	var record store.BlobRecord
	var orphaned, missing sql.NullString
	if err := row.Scan(
		&record.SHA256, &record.SizeBytes, &orphaned, &missing); err != nil {
		return record, err
	}
	if orphaned.Valid {
		value, err := parseTime(orphaned.String)
		if err != nil {
			return record, err
		}
		record.OrphanedAt = &value
	}
	if missing.Valid {
		value, err := parseTime(missing.String)
		if err != nil {
			return record, err
		}
		record.MissingAt = &value
	}
	return record, nil
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT sha256, size_bytes, orphaned_at, missing_at
		 FROM blobs
		 WHERE sha256 > ?
		 ORDER BY sha256
		 LIMIT ?`,
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
	record, err := scanBlobRecord(tx.QueryRowContext(ctx,
		`SELECT sha256, size_bytes, orphaned_at, missing_at
		 FROM blobs WHERE sha256 = ?`,
		blob.SHA256))
	if errors.Is(err, sql.ErrNoRows) {
		if !present {
			return result, store.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blobs
			 (sha256, size_bytes, created_at, orphaned_at, missing_at)
			 VALUES (?, ?, ?, ?, NULL)`,
			blob.SHA256, blob.SizeBytes, formatTime(at), formatTime(at)); err != nil {
			return result, err
		}
		record = store.BlobRecord{
			BlobInfo: blob, OrphanedAt: &at,
		}
		result = store.BlobReconcileResult{
			Record: record, Inserted: true, OrphanMarked: true,
		}
		if err := tx.Commit(); err != nil {
			return store.BlobReconcileResult{}, err
		}
		return result, nil
	}

	if err != nil {
		return result, err
	}
	if record.SizeBytes != blob.SizeBytes {
		return result, store.ErrInvariantViolation
	}
	var references int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM book_files WHERE blob_sha256 = ?`,
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE blobs SET orphaned_at = ?, missing_at = ?
			 WHERE sha256 = ?`,
			formatTimePtr(orphanedAt), formatTimePtr(missingAt),
			blob.SHA256); err != nil {
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

func (s *Store) PurgeOrphanedBlobRecords(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]store.BlobRecord, error) {
	if before.IsZero() || limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT b.sha256, b.size_bytes, b.orphaned_at, b.missing_at
		 FROM blobs b
		 WHERE b.orphaned_at IS NOT NULL
		   AND (
		       CAST(strftime('%s', b.orphaned_at) AS INTEGER) < ?
		       OR (
		           CAST(strftime('%s', b.orphaned_at) AS INTEGER) = ?
		           AND CASE
		               WHEN instr(b.orphaned_at, '.') = 0 THEN 0
		               ELSE CAST(substr(
		                   substr(
		                       b.orphaned_at,
		                       instr(b.orphaned_at, '.') + 1,
		                       instr(b.orphaned_at, 'Z') -
		                           instr(b.orphaned_at, '.') - 1
		                   ) || '000000000',
		                   1, 9
		               ) AS INTEGER)
		           END <= ?
		       )
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM book_files f WHERE f.blob_sha256 = b.sha256
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM ingest_blob_holds h
		       WHERE h.blob_sha256 = b.sha256
		   )
		 ORDER BY b.sha256
		 LIMIT ?`,
		before.UTC().Unix(), before.UTC().Unix(), before.UTC().Nanosecond(), limit)
	if err != nil {
		return nil, err
	}
	var candidates []store.BlobRecord
	for rows.Next() {
		record, err := scanBlobRecord(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, record)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	purged := make([]store.BlobRecord, 0, len(candidates))
	for _, record := range candidates {
		if record.OrphanedAt == nil {
			return nil, store.ErrInvariantViolation
		}
		result, err := tx.ExecContext(ctx,
			`DELETE FROM blobs
			 WHERE sha256 = ?
			   AND orphaned_at = ?
			   AND NOT EXISTS (
			       SELECT 1 FROM book_files f
			       WHERE f.blob_sha256 = blobs.sha256
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM ingest_blob_holds h
			       WHERE h.blob_sha256 = blobs.sha256
			   )`,
			record.SHA256, formatTime(*record.OrphanedAt))
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 1 {
			purged = append(purged, record)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return purged, nil
}
