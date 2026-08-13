package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const ingestJobColumns = `j.id, j.user_id, j.library_id, j.quota_user_id,
	j.source, j.client_key, j.request_fingerprint, j.promotion_fingerprint,
	j.artifacts_expired, j.artifact_cleanup_pending, j.state,
	j.bytes_received, j.content_sha256, j.staging_path, j.source_relative_path,
	j.extracted_embedded_metadata_json, j.book_id, j.error_code, j.error_detail,
	j.retry_count, j.revision,
	j.created_at, j.updated_at, j.expires_at`

func scanIngestJob(row interface{ Scan(...any) error }) (store.IngestJob, error) {
	var job store.IngestJob
	err := row.Scan(
		&job.ID, &job.UserID, &job.LibraryID, &job.QuotaUserID,
		&job.Source, &job.ClientKey, &job.RequestFingerprint,
		&job.PromotionFingerprint, &job.ArtifactsExpired,
		&job.ArtifactCleanupPending, &job.State,
		&job.BytesReceived, &job.ContentSHA256, &job.StagingPath,
		&job.SourceRelativePath, &job.ExtractedEmbeddedMetadataJSON, &job.BookID,
		&job.ErrorCode, &job.ErrorDetail, &job.RetryCount, &job.Revision,
		&job.CreatedAt, &job.UpdatedAt, &job.ExpiresAt,
	)
	return job, err
}

func sameOptionalString(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func ingestRequestMatches(job store.IngestJob, actorUserID string, request store.IngestJobRequest) bool {
	return job.UserID == actorUserID &&
		job.LibraryID == request.LibraryID &&
		job.Source == request.Source &&
		job.RequestFingerprint == request.RequestFingerprint &&
		sameOptionalString(job.ClientKey, request.ClientKey) &&
		sameOptionalString(job.SourceRelativePath, request.SourceRelativePath)
}

func ingestJobByIDTx(ctx context.Context, tx *sql.Tx, userID, jobID string) (store.IngestJob, error) {
	job, err := scanIngestJob(tx.QueryRowContext(ctx, q(
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.user_id = ? AND j.id = ?`),
		userID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return job, store.ErrNotFound
	}
	return job, err
}

func (s *Store) CreateIngestJob(
	ctx context.Context,
	actorUserID string,
	request store.IngestJobRequest,
) (store.IngestJob, bool, error) {
	if err := store.ValidateIngestJobRequest(request); err != nil {
		return store.IngestJob{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.IngestJob{}, false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, q(
		`INSERT INTO ingest_jobs
		 (id, user_id, library_id, quota_user_id, source, client_key,
		  request_fingerprint, state, source_relative_path, revision,
		  created_at, updated_at)
		 SELECT ?, ?, l.id, l.quota_user_id, ?, ?, ?, 'received', ?, 1, ?, ?
		 FROM libraries l
		 LEFT JOIN library_access a
		   ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage')
		   AND ((? = 'upload' AND l.kind = 'managed') OR
		        (? = 'watched' AND l.kind = 'watched')
		   )
		 ON CONFLICT DO NOTHING`),
		request.ID, actorUserID, string(request.Source), request.ClientKey,
		request.RequestFingerprint, request.SourceRelativePath,
		request.CreatedAt.UTC(), request.CreatedAt.UTC(),
		actorUserID, request.LibraryID, actorUserID,
		string(request.Source), string(request.Source))
	if err != nil {
		return store.IngestJob{}, false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return store.IngestJob{}, false, err
	}
	if inserted == 1 {
		job, err := ingestJobByIDTx(ctx, tx, actorUserID, request.ID)
		if err != nil {
			return store.IngestJob{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return store.IngestJob{}, false, err
		}
		return job, true, nil
	}

	var accessible int
	err = tx.QueryRowContext(ctx, q(
		`SELECT 1
		 FROM libraries l
		 LEFT JOIN library_access a
		   ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage')
		   AND ((? = 'upload' AND l.kind = 'managed') OR
		        (? = 'watched' AND l.kind = 'watched'))`),
		actorUserID, request.LibraryID, actorUserID,
		string(request.Source), string(request.Source)).Scan(&accessible)
	if errors.Is(err, sql.ErrNoRows) {
		return store.IngestJob{}, false, store.ErrNotFound
	}
	if err != nil {
		return store.IngestJob{}, false, err
	}

	existing, err := ingestJobByIDTx(ctx, tx, actorUserID, request.ID)
	if err == nil {
		if !ingestRequestMatches(existing, actorUserID, request) {
			return store.IngestJob{}, false, store.ErrIDMismatch
		}
		return existing, false, tx.Commit()
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.IngestJob{}, false, err
	}
	if request.ClientKey == nil {
		return store.IngestJob{}, false, store.ErrConflict
	}
	existing, err = scanIngestJob(tx.QueryRowContext(ctx, q(
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.user_id = ? AND j.library_id = ? AND j.client_key = ?`),
		actorUserID, request.LibraryID, *request.ClientKey))
	if errors.Is(err, sql.ErrNoRows) {
		return store.IngestJob{}, false, store.ErrConflict
	}
	if err != nil {
		return store.IngestJob{}, false, err
	}
	if !ingestRequestMatches(existing, actorUserID, request) {
		return store.IngestJob{}, false, store.ErrIdempotencyConflict
	}
	return existing, false, tx.Commit()
}

func (s *Store) IngestJobByID(ctx context.Context, actorUserID, jobID string) (store.IngestJob, error) {
	job, err := scanIngestJob(s.db.QueryRowContext(ctx, q(
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 JOIN libraries l ON l.id = j.library_id
		 LEFT JOIN library_access a
		   ON a.library_id = l.id AND a.user_id = ?
		 WHERE j.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage')`),
		actorUserID, jobID, actorUserID))
	if errors.Is(err, sql.ErrNoRows) {
		return job, store.ErrNotFound
	}
	return job, err
}

func (s *Store) ListIngestJobs(
	ctx context.Context,
	actorUserID, libraryID string,
	after *store.IngestJobCursor,
	limit int,
) ([]store.IngestJob, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("ingest job limit must be between 1 and 500")
	}
	if _, err := s.LibraryByID(ctx, actorUserID, libraryID, store.LibraryRoleManage); err != nil {
		return nil, err
	}
	query := `SELECT ` + ingestJobColumns + `
		FROM ingest_jobs j
		JOIN libraries l ON l.id = j.library_id
		LEFT JOIN library_access a
		  ON a.library_id = l.id AND a.user_id = ?
		WHERE j.library_id = ?
		  AND (l.owner_user_id = ? OR a.role = 'manage')`
	args := []any{actorUserID, libraryID, actorUserID}
	if after != nil {
		query += ` AND (j.created_at > ? OR (j.created_at = ? AND j.id > ?))`
		cursorTime := after.CreatedAt.UTC()
		args = append(args, cursorTime, cursorTime, after.ID)
	}
	query += ` ORDER BY j.created_at, j.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []store.IngestJob
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListIngestActivity answers "what happened to the file I just
// uploaded". Promoted jobs are excluded because a promoted job is a book
// — the catalog already shows it — which keeps this to the handful of
// rows that are still working or have gone wrong.
func (s *Store) ListIngestActivity(
	ctx context.Context,
	actorUserID, libraryID string,
	limit int,
) ([]store.IngestJob, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("ingest activity limit must be between 1 and 500")
	}
	if _, err := s.LibraryByID(ctx, actorUserID, libraryID, store.LibraryRoleManage); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q(`SELECT `+ingestJobColumns+`
		FROM ingest_jobs j
		JOIN libraries l ON l.id = j.library_id
		LEFT JOIN library_access a
		  ON a.library_id = l.id AND a.user_id = ?
		WHERE j.library_id = ?
		  AND (l.owner_user_id = ? OR a.role = 'manage')
		  AND j.state <> 'promoted'
		ORDER BY j.created_at DESC, j.id DESC
		LIMIT ?`), actorUserID, libraryID, actorUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []store.IngestJob
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) ListIngestRecoveryJobs(
	ctx context.Context,
	before time.Time,
	after *store.IngestRecoveryCursor,
	limit int,
) ([]store.IngestJob, error) {
	if before.IsZero() || limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	query := `SELECT ` + ingestJobColumns + `
		FROM ingest_jobs j
		WHERE j.state IN ('staged', 'validated', 'extracted')
		  AND j.artifacts_expired = FALSE
		  AND j.updated_at <= ?`
	args := []any{before.UTC()}
	if after != nil {
		cursorTime := after.UpdatedAt.UTC()
		query += ` AND (j.updated_at > ? OR (j.updated_at = ? AND j.id > ?))`
		args = append(args, cursorTime, cursorTime, after.ID)
	}
	query += ` ORDER BY j.updated_at, j.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []store.IngestJob
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) ListIngestWorkerJobs(
	ctx context.Context,
	state store.IngestState,
	limit int,
) ([]store.IngestJob, error) {
	if (state != store.IngestStaged &&
		state != store.IngestValidated &&
		state != store.IngestExtracted) ||
		limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	query := `SELECT ` + ingestJobColumns + `
		FROM ingest_jobs j
		WHERE j.state = ?
		  AND j.artifacts_expired = FALSE
		ORDER BY j.updated_at, j.id
		LIMIT ?`
	args := []any{state, limit}
	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []store.IngestJob
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) TransitionIngestJob(
	ctx context.Context,
	userID, jobID string,
	change store.IngestJobTransition,
) (store.IngestJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.IngestJob{}, err
	}
	defer tx.Rollback()
	current, err := ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestJob{}, err
	}
	next, err := store.ApplyIngestTransition(current, change)
	if err != nil {
		return store.IngestJob{}, err
	}
	res, err := tx.ExecContext(ctx, q(
		`UPDATE ingest_jobs
		 SET state = ?, bytes_received = ?, content_sha256 = ?,
		     staging_path = ?, extracted_embedded_metadata_json = ?,
		     error_code = ?, error_detail = ?,
		     retry_count = ?, revision = ?, updated_at = ?, expires_at = ?
		 WHERE user_id = ? AND id = ? AND state = ? AND revision = ?`),
		string(next.State), next.BytesReceived, next.ContentSHA256,
		next.StagingPath, next.ExtractedEmbeddedMetadataJSON,
		next.ErrorCode, next.ErrorDetail,
		next.RetryCount, next.Revision, next.UpdatedAt.UTC(), next.ExpiresAt,
		userID, jobID, string(change.ExpectedState), change.ExpectedRevision)
	if err != nil {
		return store.IngestJob{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return store.IngestJob{}, err
	}
	if n == 0 {
		return store.IngestJob{}, store.ErrStaleRevision
	}
	next, err = ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.IngestJob{}, err
	}
	return next, nil
}

func quotaUsageTx(
	ctx context.Context,
	tx *sql.Tx,
	quotaUserID, blobSHA string,
	blobBytes int64,
) (store.QuotaUsage, error) {
	var inconsistent int
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(1)
		 FROM (
		     SELECT blob_sha256
		     FROM (
		         SELECT blob_sha256, bytes FROM blob_reservations WHERE quota_user_id = ?
		         UNION ALL
		         SELECT blob_sha256, bytes FROM ingest_blob_holds WHERE quota_user_id = ?
		     ) entries
		     GROUP BY blob_sha256
		     HAVING MIN(bytes) <> MAX(bytes)
		 ) inconsistent`),
		quotaUserID, quotaUserID).Scan(&inconsistent); err != nil {
		return store.QuotaUsage{}, err
	}
	if inconsistent != 0 {
		return store.QuotaUsage{}, store.ErrInvariantViolation
	}
	var used int64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COALESCE(SUM(bytes), 0)
		 FROM (
		     SELECT blob_sha256, MAX(bytes) AS bytes
		     FROM (
		         SELECT blob_sha256, bytes FROM blob_reservations WHERE quota_user_id = ?
		         UNION ALL
		         SELECT blob_sha256, bytes FROM ingest_blob_holds WHERE quota_user_id = ?
		     ) entries
		     GROUP BY blob_sha256
		 ) usage`),
		quotaUserID, quotaUserID).Scan(&used); err != nil {
		return store.QuotaUsage{}, err
	}
	var count int
	var minBytes, maxBytes sql.NullInt64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT COUNT(1), MIN(bytes), MAX(bytes)
		 FROM (
		     SELECT bytes FROM blob_reservations
		     WHERE quota_user_id = ? AND blob_sha256 = ?
		     UNION ALL
		     SELECT bytes FROM ingest_blob_holds
		     WHERE quota_user_id = ? AND blob_sha256 = ?
		 ) entries`),
		quotaUserID, blobSHA, quotaUserID, blobSHA).
		Scan(&count, &minBytes, &maxBytes); err != nil {
		return store.QuotaUsage{}, err
	}
	additional := blobBytes
	if count != 0 {
		if !minBytes.Valid || !maxBytes.Valid ||
			minBytes.Int64 != blobBytes || maxBytes.Int64 != blobBytes {
			return store.QuotaUsage{}, store.ErrInvariantViolation
		}
		additional = 0
	}
	return store.QuotaUsage{
		UsedBytes: used + additional, AdditionalBytes: additional,
	}, nil
}

func lockQuotaPrincipal(ctx context.Context, tx *sql.Tx, quotaUserID string) error {
	var id string
	err := tx.QueryRowContext(ctx, q(
		`SELECT id FROM users WHERE id = ? FOR UPDATE`), quotaUserID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

func lockedIngestJobTx(ctx context.Context, tx *sql.Tx, userID, jobID string) (store.IngestJob, error) {
	job, err := scanIngestJob(tx.QueryRowContext(ctx, q(
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.user_id = ? AND j.id = ?
		 FOR UPDATE`), userID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return job, store.ErrNotFound
	}
	return job, err
}

func (s *Store) CommitIngestStage(
	ctx context.Context,
	userID, jobID string,
	request store.CommitIngestStageRequest,
) (store.CommitIngestStageResult, error) {
	if err := store.ValidateCommitIngestStage(jobID, request); err != nil {
		return store.CommitIngestStageResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	defer tx.Rollback()
	job, err := ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	if err := lockQuotaPrincipal(ctx, tx, job.QuotaUserID); err != nil {
		return store.CommitIngestStageResult{}, err
	}
	job, err = lockedIngestJobTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	if job.State != store.IngestReceived ||
		job.Revision != request.ExpectedRevision {
		return store.CommitIngestStageResult{}, store.ErrStaleRevision
	}
	if request.UpdatedAt.Before(job.UpdatedAt) {
		return store.CommitIngestStageResult{}, store.ErrInvalidTransition
	}
	usage, err := quotaUsageTx(ctx, tx, job.QuotaUserID,
		request.Artifact.SHA256, request.Artifact.SizeBytes)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	if limit := request.QuotaLimitBytes; limit != nil &&
		usage.AdditionalBytes > 0 &&
		(usage.UsedBytes-usage.AdditionalBytes > *limit ||
			usage.AdditionalBytes > *limit-(usage.UsedBytes-usage.AdditionalBytes)) {
		return store.CommitIngestStageResult{}, &store.QuotaExceededError{
			LimitBytes: *limit, UsedBytes: usage.UsedBytes - usage.AdditionalBytes,
			AdditionalBytes: usage.AdditionalBytes,
		}
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO ingest_blob_holds
		 (job_id, quota_user_id, blob_sha256, bytes, created_at)
		 VALUES (?, ?, ?, ?, ?)`),
		job.ID, job.QuotaUserID, request.Artifact.SHA256,
		request.Artifact.SizeBytes, request.UpdatedAt.UTC()); err != nil {
		if isUniqueErr(err) {
			return store.CommitIngestStageResult{}, store.ErrInvariantViolation
		}
		return store.CommitIngestStageResult{}, err
	}
	res, err := tx.ExecContext(ctx, q(
		`UPDATE ingest_jobs
		 SET state = 'staged', bytes_received = ?, content_sha256 = ?,
		     staging_path = ?, revision = revision + 1, updated_at = ?
		 WHERE user_id = ? AND id = ? AND state = 'received' AND revision = ?`),
		request.Artifact.SizeBytes, request.Artifact.SHA256, request.StagingPath,
		request.UpdatedAt.UTC(), userID, jobID, request.ExpectedRevision)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	if n == 0 {
		return store.CommitIngestStageResult{}, store.ErrStaleRevision
	}
	job, err = ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.CommitIngestStageResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.CommitIngestStageResult{}, err
	}
	return store.CommitIngestStageResult{Job: job, Quota: usage}, nil
}

const bookFileColumns = `f.id, f.library_id, f.book_id, f.blob_sha256,
	f.source, f.source_relative_path, f.original_filename, f.media_type,
	f.partial_md5, f.dc_identifier, f.availability, f.created_at, f.updated_at`

func scanBookFile(row interface{ Scan(...any) error }) (store.BookFile, error) {
	var file store.BookFile
	err := row.Scan(
		&file.ID, &file.LibraryID, &file.BookID, &file.BlobSHA256,
		&file.Source, &file.SourceRelativePath, &file.OriginalFilename,
		&file.MediaType, &file.PartialMD5, &file.DCIdentifier,
		&file.Availability, &file.CreatedAt, &file.UpdatedAt,
	)
	return file, err
}

func insertPromotionBookTx(ctx context.Context, tx *sql.Tx, book store.CatalogBook) error {
	if book.UpdatedAt.IsZero() {
		book.UpdatedAt = book.CreatedAt
	}
	_, err := tx.ExecContext(ctx, q(
		`INSERT INTO books (
		     id, library_id, status,
		     title, title_source, title_locked,
		     subtitle, subtitle_source, subtitle_locked,
		     description, description_source, description_locked,
		     publisher, publisher_source, publisher_locked,
		     published_date, published_date_source, published_date_locked,
		     raw_metadata_json, created_at, updated_at, trashed_at, trash_expires_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		           ?, ?, ?, ?, ?)`),
		book.ID, book.LibraryID, string(book.Status),
		book.Title, string(book.TitleSource), book.TitleLocked,
		book.Subtitle, string(book.SubtitleSource), book.SubtitleLocked,
		book.Description, string(book.DescriptionSource), book.DescriptionLocked,
		book.Publisher, string(book.PublisherSource), book.PublisherLocked,
		book.PublishedDate, string(book.PublishedDateSource), book.PublishedDateLocked,
		book.RawMetadataJSON, book.CreatedAt.UTC(), book.UpdatedAt.UTC(),
		book.TrashedAt, book.TrashExpiresAt)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func promotionReplayTx(
	ctx context.Context,
	tx *sql.Tx,
	job store.IngestJob,
	request store.CommitNewBookPromotionRequest,
	fingerprint string,
) (store.IngestPromotionResult, error) {
	if job.PromotionFingerprint == nil ||
		*job.PromotionFingerprint != fingerprint ||
		job.BookID == nil || *job.BookID != request.Book.ID {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	book, err := scanCatalogBook(tx.QueryRowContext(ctx, q(
		`SELECT `+bookColumns+` FROM books b
		 WHERE b.library_id = ? AND b.id = ?`),
		job.LibraryID, *job.BookID))
	if err != nil {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	file, err := scanBookFile(tx.QueryRowContext(ctx, q(
		`SELECT `+bookFileColumns+` FROM book_files f
		 WHERE f.library_id = ? AND f.id = ? AND f.book_id = ?`),
		job.LibraryID, request.File.ID, *job.BookID))
	if err != nil {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	var size int64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT size_bytes FROM blobs WHERE sha256 = ?`),
		file.BlobSHA256).Scan(&size); err != nil {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	if file.BlobSHA256 != request.Blob.SHA256 ||
		size != request.Blob.SizeBytes {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	return store.IngestPromotionResult{
		Job: job, Book: book, File: file,
		Blob:     store.BlobInfo{SHA256: file.BlobSHA256, SizeBytes: size},
		Replayed: true,
	}, nil
}

func (s *Store) CommitNewBookPromotion(
	ctx context.Context,
	userID, jobID string,
	request store.CommitNewBookPromotionRequest,
) (store.IngestPromotionResult, error) {
	if err := store.ValidateNewBookPromotion(request); err != nil {
		return store.IngestPromotionResult{}, err
	}
	promotionFingerprint, err := store.NewBookPromotionFingerprint(request)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	defer tx.Rollback()
	job, err := ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if err := lockQuotaPrincipal(ctx, tx, job.QuotaUserID); err != nil {
		return store.IngestPromotionResult{}, err
	}
	job, err = lockedIngestJobTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if job.State == store.IngestPromoted {
		return promotionReplayTx(ctx, tx, job, request, promotionFingerprint)
	}
	if job.State != store.IngestExtracted ||
		job.Revision != request.ExpectedRevision {
		return store.IngestPromotionResult{}, store.ErrStaleRevision
	}
	if request.UpdatedAt.Before(job.UpdatedAt) {
		return store.IngestPromotionResult{}, store.ErrInvalidTransition
	}
	if job.ContentSHA256 == nil || job.StagingPath == nil ||
		*job.ContentSHA256 != request.Blob.SHA256 ||
		job.BytesReceived != request.Blob.SizeBytes ||
		request.Book.LibraryID != job.LibraryID ||
		request.File.LibraryID != job.LibraryID ||
		request.File.BookID != request.Book.ID ||
		request.File.BlobSHA256 != request.Blob.SHA256 ||
		request.File.Source != job.Source ||
		!sameOptionalString(request.File.SourceRelativePath, job.SourceRelativePath) {
		return store.IngestPromotionResult{}, store.ErrContentMismatch
	}
	var holdSHA string
	var holdBytes int64
	err = tx.QueryRowContext(ctx, q(
		`SELECT blob_sha256, bytes FROM ingest_blob_holds
		 WHERE job_id = ? AND quota_user_id = ?`),
		job.ID, job.QuotaUserID).Scan(&holdSHA, &holdBytes)
	if err != nil || holdSHA != request.Blob.SHA256 ||
		holdBytes != request.Blob.SizeBytes {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO blobs (sha256, size_bytes, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (sha256) DO NOTHING`),
		request.Blob.SHA256, request.Blob.SizeBytes,
		request.UpdatedAt.UTC()); err != nil {
		return store.IngestPromotionResult{}, err
	}
	var blobBytes int64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT size_bytes FROM blobs WHERE sha256 = ?`),
		request.Blob.SHA256).Scan(&blobBytes); err != nil ||
		blobBytes != request.Blob.SizeBytes {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE blobs
		 SET orphaned_at = NULL, missing_at = NULL
		 WHERE sha256 = ?`),
		request.Blob.SHA256); err != nil {
		return store.IngestPromotionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO blob_reservations
		 (quota_user_id, blob_sha256, bytes, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (quota_user_id, blob_sha256) DO NOTHING`),
		job.QuotaUserID, request.Blob.SHA256, request.Blob.SizeBytes,
		request.UpdatedAt.UTC()); err != nil {
		return store.IngestPromotionResult{}, err
	}
	var reservedBytes int64
	if err := tx.QueryRowContext(ctx, q(
		`SELECT bytes FROM blob_reservations
		 WHERE quota_user_id = ? AND blob_sha256 = ?`),
		job.QuotaUserID, request.Blob.SHA256).Scan(&reservedBytes); err != nil ||
		reservedBytes != request.Blob.SizeBytes {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	if err := insertPromotionBookTx(ctx, tx, request.Book); err != nil {
		return store.IngestPromotionResult{}, err
	}
	mediaType := request.File.MediaType
	if mediaType == "" {
		mediaType = "application/epub+zip"
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO book_files
		 (id, library_id, book_id, blob_sha256, source,
		  source_relative_path, original_filename, media_type,
		  partial_md5, dc_identifier, availability, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		request.File.ID, request.File.LibraryID, request.File.BookID,
		request.File.BlobSHA256, string(request.File.Source),
		request.File.SourceRelativePath, request.File.OriginalFilename,
		mediaType, request.File.PartialMD5, request.File.DCIdentifier,
		string(request.File.Availability), request.File.CreatedAt.UTC(),
		request.File.UpdatedAt.UTC()); err != nil {
		if isUniqueErr(err) {
			return store.IngestPromotionResult{}, store.ErrConflict
		}
		return store.IngestPromotionResult{}, err
	}
	res, err := tx.ExecContext(ctx, q(
		`UPDATE ingest_jobs
		 SET state = 'promoted', book_library_id = ?, book_id = ?,
		     promotion_fingerprint = ?, revision = revision + 1, updated_at = ?
		 WHERE user_id = ? AND id = ? AND state = 'extracted' AND revision = ?`),
		job.LibraryID, request.Book.ID, promotionFingerprint,
		request.UpdatedAt.UTC(),
		userID, jobID, request.ExpectedRevision)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return store.IngestPromotionResult{}, err
	} else if n == 0 {
		return store.IngestPromotionResult{}, store.ErrStaleRevision
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM ingest_blob_holds WHERE job_id = ?`), job.ID); err != nil {
		return store.IngestPromotionResult{}, err
	}
	job, err = ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	book, err := scanCatalogBook(tx.QueryRowContext(ctx, q(
		`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`), request.Book.ID))
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	file, err := scanBookFile(tx.QueryRowContext(ctx, q(
		`SELECT `+bookFileColumns+` FROM book_files f WHERE f.id = ?`),
		request.File.ID))
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.IngestPromotionResult{}, err
	}
	return store.IngestPromotionResult{
		Job: job, Book: book, File: file, Blob: request.Blob,
	}, nil
}

func (s *Store) PurgeExpiredIngestArtifacts(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]store.IngestJob, error) {
	if before.IsZero() || limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, q(
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.artifact_cleanup_pending = TRUE
		    OR (j.artifacts_expired = FALSE
		        AND j.state IN ('failed', 'quarantined')
		        AND j.expires_at IS NOT NULL AND j.expires_at <= ?
		        AND j.staging_path IS NOT NULL)
		 ORDER BY j.expires_at, j.id
		 LIMIT ?
		 FOR UPDATE SKIP LOCKED`),
		before.UTC(), limit)
	if err != nil {
		return nil, err
	}
	var jobs []store.IngestJob
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i, job := range jobs {
		if job.ArtifactCleanupPending {
			continue
		}
		res, err := tx.ExecContext(ctx, q(
			`UPDATE ingest_jobs
			 SET artifacts_expired = TRUE, artifact_cleanup_pending = TRUE,
			     revision = revision + 1
			 WHERE id = ? AND user_id = ? AND state = ? AND revision = ?
			   AND expires_at IS NOT NULL AND expires_at <= ?
			   AND staging_path IS NOT NULL AND artifacts_expired = FALSE
			   AND artifact_cleanup_pending = FALSE`),
			job.ID, job.UserID, string(job.State), job.Revision, before.UTC())
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, store.ErrStaleRevision
		}
		res, err = tx.ExecContext(ctx, q(
			`DELETE FROM ingest_blob_holds WHERE job_id = ?`), job.ID)
		if err != nil {
			return nil, err
		}
		if n, err = res.RowsAffected(); err != nil {
			return nil, err
		} else if n != 1 {
			return nil, store.ErrInvariantViolation
		}
		jobs[i].ArtifactsExpired = true
		jobs[i].ArtifactCleanupPending = true
		jobs[i].Revision++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) CompleteIngestArtifactCleanup(
	ctx context.Context,
	jobID, stagingPath string,
) error {
	if jobID == "" || stagingPath == "" {
		return store.ErrInvalidTransition
	}
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE ingest_jobs
		 SET bytes_received = 0, content_sha256 = NULL, staging_path = NULL,
		     artifact_cleanup_pending = FALSE, revision = revision + 1
		 WHERE id = ? AND staging_path = ?
		   AND artifacts_expired = TRUE AND artifact_cleanup_pending = TRUE`),
		jobID, stagingPath)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var expired, pending bool
	var currentPath sql.NullString
	err = s.db.QueryRowContext(ctx, q(
		`SELECT artifacts_expired, artifact_cleanup_pending, staging_path
		 FROM ingest_jobs WHERE id = ?`), jobID).
		Scan(&expired, &pending, &currentPath)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if expired && !pending && !currentPath.Valid {
		return nil
	}
	return store.ErrStaleRevision
}
