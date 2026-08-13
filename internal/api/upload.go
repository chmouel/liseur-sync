package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/google/uuid"
)

// uploadNS derives a job id from the caller's idempotency key. Deriving it
// rather than generating one is what makes a retry safe: the same key
// produces the same id, so a client that never saw our response re-enters
// the existing job instead of creating a second one for the same book.
var uploadNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0005")

// maxIdempotencyKeyBytes bounds the client-supplied key. The store allows
// 256; rejecting longer keys here turns a storage-layer error into a
// precise 400.
const maxIdempotencyKeyBytes = 256

// maxLibraryIDBytes bounds the {library} path value. Ids are uuids; this
// leaves room for other id shapes without letting the fingerprint overflow.
const maxLibraryIDBytes = 128

// uploadFormField is the multipart field carrying the publication.
const uploadFormField = "file"

// ContentStore is the subset of the CAS the API needs. Uploads stage bytes;
// everything after that belongs to the ingest workers. RemoveStage is here
// because a stage that is never committed is unreachable from the database:
// no recovery or GC pass can find it, so the request that created it has to
// clean it up.
type ContentStore interface {
	Stage(ctx context.Context, jobID string, src io.Reader, maxBytes int64) (content.StagedBlob, error)
	RemoveStage(ctx context.Context, stagingPath string) error
}

// uploadEnvelopeSlack allows for multipart boundaries, part headers and any
// non-file parts on top of the file itself, so that a body within the
// configured limit is never cut off by the transport bound.
const uploadEnvelopeSlack = 1 << 20

// HandleUpload implements POST /v1/libraries/{library}/upload.
//
// The handler's whole job is to get bytes onto disk durably and record that
// it did. It does not validate the EPUB, read its metadata, or create a
// book: those are the ingest worker's passes, and doing them here would tie
// a large, slow, attacker-influenced parse to the lifetime of one HTTP
// request. A caller gets a job back and follows it.
func (s *Server) HandleUpload(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.Content == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" {
		writeError(w, http.StatusBadRequest, "library id required")
		return
	}
	// Bound the path value. It goes into the request fingerprint, which
	// the store rejects past 512 bytes with an error we cannot classify —
	// so an over-long library id would answer 500 where every other
	// unknown library answers 404.
	if len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key header required")
		return
	}
	if len(key) > maxIdempotencyKeyBytes {
		writeError(w, http.StatusBadRequest, "Idempotency-Key too long")
		return
	}
	if !isMultipartForm(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType,
			"multipart/form-data body required")
		return
	}
	// Bound the transport, not just the file. Without this a client can
	// make the server read forever: multipart.Part.Close drains the rest
	// of a part, and skipping a non-file part reads all of it, so both
	// happen before and after the size check that is supposed to stop them.
	r.Body = http.MaxBytesReader(w, r.Body,
		s.Cfg.Content.MaxUploadBytes+uploadEnvelopeSlack)

	part, err := uploadPart(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Not deferred: multipart.Part.Close drains the rest of the part,
	// which for a refused upload is exactly the data we declined to
	// read. StageUpload reports whether it got as far as reading, so
	// closing stays on the paths where the body is already consumed.
	job, replayed, err := s.StageUpload(r.Context(),
		tok.UserID, libraryID, key, part)
	if err != nil {
		if !errors.Is(err, errUploadBodyUnread) {
			part.Close()
		}
		writeUploadError(w, err)
		return
	}
	part.Close()
	if replayed {
		writeJSON(w, http.StatusOK, uploadJobJSON(job))
		return
	}
	writeJSON(w, http.StatusAccepted, uploadJobJSON(job))
}

// StageUpload is the whole of an upload that is not HTTP: it opens or
// re-enters the idempotent job, streams the body into the CAS, and
// records that it did. The web UI drives the same path with a cookie
// session instead of a token, and must not grow a second copy of this —
// the cleanup rules below are subtle enough once.
//
// It reports `replayed` when the job already carried its bytes, so a
// caller can distinguish "stored now" from "stored earlier".
func (s *Server) StageUpload(
	ctx context.Context, userID, libraryID, key string, body io.Reader,
) (store.IngestJob, bool, error) {
	if s.Content == nil {
		return store.IngestJob{}, false, errUploadNoStorage
	}
	now := time.Now().UTC()
	job, created, err := s.St.CreateIngestJob(ctx, userID,
		store.IngestJobRequest{
			ID:                 uploadJobID(userID, libraryID, key),
			LibraryID:          libraryID,
			Source:             store.IngestUpload,
			ClientKey:          &key,
			RequestFingerprint: uploadFingerprint(userID, libraryID, key),
			CreatedAt:          now,
		})
	if err != nil {
		return store.IngestJob{}, false, uploadPhaseError{phase: uploadPhaseJob, err: err}
	}

	// A replay that already carries its bytes must not read the body again.
	// Staging is keyed by job id, so re-reading would either re-hash the
	// original stage and discard what the client just sent, or contradict a
	// digest the database has already committed.
	if !created && job.State != store.IngestReceived {
		return job, true, nil
	}

	staged, err := s.Content.Stage(ctx, job.ID, body, s.Cfg.Content.MaxUploadBytes)
	if err != nil {
		return store.IngestJob{}, false, uploadPhaseError{phase: uploadPhaseStage, err: err}
	}

	result, err := s.St.CommitIngestStage(ctx, userID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact:         store.BlobInfo{SHA256: staged.SHA256, SizeBytes: staged.Size},
			StagingPath:      staged.Path,
			QuotaLimitBytes:  s.uploadQuotaLimit(),
			UpdatedAt:        time.Now().UTC(),
		})
	if err != nil {
		// The bytes are on disk but the database does not point at them.
		// Unless someone else won the race and committed this very stage,
		// nothing will ever find them again, so drop them here — otherwise
		// a user sitting at their quota could fill the disk by retrying
		// with a fresh key each time. Removing it also keeps the next
		// attempt honest: Stage replays an existing stage without reading
		// the body, so a leftover would answer a later upload with the
		// wrong file's digest.
		winner, orphaned, raced := s.resolveCommitFailure(ctx, userID, job.ID, err)
		if orphaned {
			s.removeStage(ctx, staged.Path)
		}
		if raced {
			return winner, true, nil
		}
		return store.IngestJob{}, false, uploadPhaseError{phase: uploadPhaseCommit, err: err}
	}
	return result.Job, false, nil
}

// HandleIngestJob implements GET /v1/ingest/jobs/{id}, which is how a
// client learns that the book it uploaded became a book — or why it did
// not.
func (s *Server) HandleIngestJob(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	job, err := s.St.IngestJobByID(r.Context(), tok.UserID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "job lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, uploadJobJSON(job))
}

func (s *Server) uploadQuotaLimit() *int64 {
	if s.Cfg.Content.QuotaBytes <= 0 {
		return nil
	}
	limit := s.Cfg.Content.QuotaBytes
	return &limit
}

func uploadJobID(userID, libraryID, key string) string {
	return uuid.NewSHA1(uploadNS,
		[]byte(userID+"|"+libraryID+"|"+key)).String()
}

// uploadFingerprint is what the store compares when a key is replayed. It
// deliberately covers only the request envelope, because the handler cannot
// know the content hash until the body has been streamed — and by then the
// job must already exist to name the staging path.
func uploadFingerprint(userID, libraryID, key string) string {
	return "upload|" + userID + "|" + libraryID + "|" + key
}

func isMultipartForm(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "multipart/form-data"
}

// uploadPart returns the publication part without buffering it. Streaming
// matters: ParseMultipartForm would spool the whole upload to a temporary
// file first, doubling the write and putting a copy outside the CAS.
func uploadPart(r *http.Request) (*multipart.Part, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("malformed multipart body")
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no " + uploadFormField + " part in body")
		}
		if err != nil {
			return nil, errors.New("malformed multipart body")
		}
		if part.FormName() == uploadFormField {
			return part, nil
		}
		part.Close()
	}
}

// uploadPhase says where an upload gave up. The same store error means
// different things in different places — ErrConflict on job creation is a
// reused idempotency key, ErrConflict on commit is a lost race — so the
// phase travels with the error rather than being guessed from it.
type uploadPhase int

const (
	uploadPhaseJob uploadPhase = iota
	uploadPhaseStage
	uploadPhaseCommit
)

type uploadPhaseError struct {
	phase uploadPhase
	err   error
}

func (e uploadPhaseError) Error() string { return e.err.Error() }
func (e uploadPhaseError) Unwrap() error { return e.err }

// errUploadNoStorage means the instance has no content root configured.
var errUploadNoStorage = errors.New("content storage is unavailable")

// errUploadBodyUnread marks the failures that happened before or during
// the read, where draining the rest of the request is precisely what the
// caller must not do.
var errUploadBodyUnread = errors.New("upload body was not consumed")

func (e uploadPhaseError) Is(target error) bool {
	return target == errUploadBodyUnread &&
		(e.phase == uploadPhaseJob || e.phase == uploadPhaseStage)
}

// writeUploadError maps a staging failure onto the HTTP contract. It is
// the only place that decides a status, so the web UI and the API agree
// on what each failure means even though they render it differently.
func writeUploadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUploadNoStorage) {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	var phased uploadPhaseError
	if !errors.As(err, &phased) {
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	switch phased.phase {
	case uploadPhaseJob:
		writeUploadJobError(w, phased.err)
	case uploadPhaseStage:
		writeStageError(w, phased.err)
	default:
		writeCommitError(w, phased.err)
	}
}

func writeUploadJobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Covers both "no such library" and "no manage access", and is
		// also what a watched library returns: uploads only target managed
		// ones. Distinguishing them would tell an unauthorized caller
		// which libraries exist.
		writeError(w, http.StatusNotFound, "library not found or not writable")
	case errors.Is(err, store.ErrIDMismatch),
		errors.Is(err, store.ErrIdempotencyConflict),
		errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict,
			"idempotency key already used for a different request")
	default:
		writeError(w, http.StatusInternalServerError, "upload failed")
	}
}

func writeStageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, content.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds the size limit")
	case errors.Is(err, context.Canceled), errors.Is(err, io.ErrUnexpectedEOF):
		// The client went away mid-upload. Nothing was committed, and the
		// partial file is removed by Stage itself.
		writeError(w, http.StatusBadRequest, "upload interrupted")
	default:
		writeError(w, http.StatusInternalServerError, "staging failed")
	}
}

func writeCommitError(w http.ResponseWriter, err error) {
	var quota *store.QuotaExceededError
	if errors.As(err, &quota) {
		// 413 rather than 507: the request is refused because of its size,
		// and ADR-0005 requires envelope failures to be 4xx.
		writeError(w, http.StatusRequestEntityTooLarge, "storage quota exceeded")
		return
	}
	writeError(w, http.StatusInternalServerError, "upload failed")
}

// resolveCommitFailure handles the gap between durable bytes and a durable
// record. Losing this race is normal — two tabs, or a retry arriving while
// the first request still runs — and it is not an error for the caller:
// the bytes are staged either way, so we report the job the winner created.
//
// It reports the winning job, whether the staged bytes are now orphaned
// and should be deleted, and whether the failure was a race at all.
func (s *Server) resolveCommitFailure(
	ctx context.Context, userID, jobID string, err error,
) (store.IngestJob, bool, bool) {
	var quota *store.QuotaExceededError
	if errors.As(err, &quota) {
		return store.IngestJob{}, true, false
	}
	if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrInvalidTransition) {
		if job, lookupErr := s.St.IngestJobByID(ctx, userID, jobID); lookupErr == nil {
			// A concurrent request committed first. The stage it committed
			// is the one we just wrote — the path is a function of the job
			// id — so it is referenced now and must not be removed.
			return job, job.StagingPath == nil, true
		}
	}
	return store.IngestJob{}, true, false
}

// removeStage drops staged bytes that no database row references. It runs
// on a context detached from the request, because a client that hangs up
// mid-upload is exactly the case that leaves an orphan behind.
func (s *Server) removeStage(ctx context.Context, path string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Content.RemoveStage(ctx, path); err != nil {
		slog.Error("orphaned upload stage left on disk",
			"path", path, "error", err)
	}
}

func uploadJobJSON(job store.IngestJob) map[string]any {
	out := map[string]any{
		"job_id":     job.ID,
		"library_id": job.LibraryID,
		"state":      string(job.State),
		"source":     string(job.Source),
		"created_at": job.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if job.ContentSHA256 != nil {
		out["sha256"] = *job.ContentSHA256
	}
	if job.BytesReceived > 0 {
		out["size_bytes"] = job.BytesReceived
	}
	if job.BookID != nil {
		out["book_id"] = *job.BookID
	}
	if job.ErrorCode != nil {
		out["error_code"] = *job.ErrorCode
	}
	return out
}
