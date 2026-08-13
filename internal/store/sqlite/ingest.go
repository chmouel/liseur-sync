package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chmouel/liseur-sync/internal/store"
)

const ingestJobColumns = `j.id, j.user_id, j.library_id, j.quota_user_id,
	j.source, j.client_key, j.request_fingerprint, j.state,
	j.bytes_received, j.content_sha256, j.staging_path, j.source_relative_path,
	j.book_id, j.error_code, j.error_detail, j.retry_count, j.revision,
	j.created_at, j.updated_at, j.expires_at`

func scanIngestJob(row interface{ Scan(...any) error }) (store.IngestJob, error) {
	var job store.IngestJob
	var clientKey, contentSHA, stagingPath, sourcePath sql.NullString
	var bookID, errorCode, errorDetail, expires sql.NullString
	var created, updated string
	err := row.Scan(
		&job.ID, &job.UserID, &job.LibraryID, &job.QuotaUserID,
		&job.Source, &clientKey, &job.RequestFingerprint, &job.State,
		&job.BytesReceived, &contentSHA, &stagingPath, &sourcePath,
		&bookID, &errorCode, &errorDetail, &job.RetryCount, &job.Revision,
		&created, &updated, &expires,
	)
	if err != nil {
		return job, err
	}
	assign := func(value sql.NullString) *string {
		if !value.Valid {
			return nil
		}
		return &value.String
	}
	job.ClientKey = assign(clientKey)
	job.ContentSHA256 = assign(contentSHA)
	job.StagingPath = assign(stagingPath)
	job.SourceRelativePath = assign(sourcePath)
	job.BookID = assign(bookID)
	job.ErrorCode = assign(errorCode)
	job.ErrorDetail = assign(errorDetail)
	if job.CreatedAt, err = parseTime(created); err != nil {
		return job, err
	}
	if job.UpdatedAt, err = parseTime(updated); err != nil {
		return job, err
	}
	if expires.Valid {
		value, err := parseTime(expires.String)
		if err != nil {
			return job, err
		}
		job.ExpiresAt = &value
	}
	return job, nil
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
	job, err := scanIngestJob(tx.QueryRowContext(ctx,
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.user_id = ? AND j.id = ?`,
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

	res, err := tx.ExecContext(ctx,
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
		        (? = 'watched' AND l.kind = 'watched'))
		 ON CONFLICT DO NOTHING`,
		request.ID, actorUserID, string(request.Source), nullStr(request.ClientKey),
		request.RequestFingerprint, nullStr(request.SourceRelativePath),
		formatTime(request.CreatedAt), formatTime(request.CreatedAt),
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
	err = tx.QueryRowContext(ctx,
		`SELECT 1
		 FROM libraries l
		 LEFT JOIN library_access a
		   ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage')
		   AND ((? = 'upload' AND l.kind = 'managed') OR
		        (? = 'watched' AND l.kind = 'watched'))`,
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
	existing, err = scanIngestJob(tx.QueryRowContext(ctx,
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 WHERE j.user_id = ? AND j.library_id = ? AND j.client_key = ?`,
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
	job, err := scanIngestJob(s.db.QueryRowContext(ctx,
		`SELECT `+ingestJobColumns+`
		 FROM ingest_jobs j
		 JOIN libraries l ON l.id = j.library_id
		 LEFT JOIN library_access a
		   ON a.library_id = l.id AND a.user_id = ?
		 WHERE j.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage')`,
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
		cursorTime := formatTime(after.CreatedAt)
		args = append(args, cursorTime, cursorTime, after.ID)
	}
	query += ` ORDER BY j.created_at, j.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
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
	res, err := tx.ExecContext(ctx,
		`UPDATE ingest_jobs
		 SET state = ?, bytes_received = ?, content_sha256 = ?,
		     staging_path = ?, error_code = ?, error_detail = ?,
		     retry_count = ?, revision = ?, updated_at = ?, expires_at = ?
		 WHERE user_id = ? AND id = ? AND state = ? AND revision = ?`,
		string(next.State), next.BytesReceived, nullStr(next.ContentSHA256),
		nullStr(next.StagingPath), nullStr(next.ErrorCode), nullStr(next.ErrorDetail),
		next.RetryCount, next.Revision, formatTime(next.UpdatedAt),
		formatTimePtr(next.ExpiresAt), userID, jobID,
		string(change.ExpectedState), change.ExpectedRevision)
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
