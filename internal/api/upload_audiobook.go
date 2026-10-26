package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/m4b"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandleUploadAudiobook implements POST /v1/books/{id}/audiobook.
//
// ADR-0058: ADR-0023's upload, aimed at a book rather than a folder. The
// .m4b lands beside the book in the book's own folder, under the same
// rule that lets a book be uploaded there, and the pass that follows
// indexes and pairs it. This handler writes no catalog row either.
func (s *Server) HandleUploadAudiobook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	result, err := s.ReceiveAudiobook(w, r, tok.UserID, r.PathValue("id"))
	if err != nil {
		writeUploadError(w, err)
		return
	}
	body := map[string]any{
		"book_id":        result.BookID,
		"content_sha256": result.ContentSHA256,
	}
	// As with a book, a pass that concluded nothing yet is "not yet":
	// the file is on the disk and the watcher will come back to it.
	if result.AudiobookID == "" {
		writeJSON(w, http.StatusAccepted, body)
		return
	}
	body["audiobook_id"] = result.AudiobookID
	body["state"] = result.State
	writeJSON(w, http.StatusCreated, body)
}

// AudiobookUploadResult is what came of receiving one audiobook. An
// empty AudiobookID is the "not yet" case; an empty State with an
// AudiobookID is an audiobook the pass indexed but did not pair with
// this book.
type AudiobookUploadResult struct {
	BookID        string
	AudiobookID   string
	State         string
	ContentSHA256 string
}

// ReceiveAudiobook writes one .m4b from a multipart body beside a book
// and reconciles its folder. Both the JSON route and the web UI's form
// call it, so the rules exist once. Every refusal is a *WriteError.
func (s *Server) ReceiveAudiobook(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) (AudiobookUploadResult, error) {
	book, folder, err := s.audiobookUploadTarget(r.Context(), userID, bookID)
	if err != nil {
		return AudiobookUploadResult{}, err
	}
	spooled, err := s.spoolAudiobook(w, r)
	if err != nil {
		var refusal *WriteError
		if errors.As(err, &refusal) {
			slog.Info("audiobook upload: refused",
				"book", book.ID, "status", refusal.Status,
				"reason", refusal.Message, "user", userID)
		}
		return AudiobookUploadResult{}, err
	}
	defer spooled.cleanup()

	s.uploading.Lock()
	defer s.uploading.Unlock()

	// Checked again under the lock: another upload may have finished
	// while this one was spooling.
	if err := s.refuseLinkedBook(r.Context(), book.ID); err != nil {
		return AudiobookUploadResult{}, err
	}
	switch _, err := s.St.CatalogAudiobookByDigest(r.Context(), userID, spooled.sha); {
	case err == nil:
		return AudiobookUploadResult{}, uploadErr(http.StatusConflict,
			"that audiobook is already in the library")
	case !errors.Is(err, store.ErrNotFound):
		return AudiobookUploadResult{}, uploadErr(http.StatusInternalServerError,
			"catalog lookup failed")
	}

	if _, err := spooled.file.Seek(0, io.SeekStart); err != nil {
		return AudiobookUploadResult{}, uploadErr(http.StatusInternalServerError,
			"upload could not be read")
	}
	_, err = s.Ingest.IngestAudiobook(r.Context(), folder, book,
		content.AudiobookUpload{Source: spooled.file, Size: spooled.size})
	switch {
	case errors.Is(err, content.ErrUploadsRefused):
		return AudiobookUploadResult{}, uploadErr(http.StatusForbidden,
			"this folder does not accept uploads")
	case errors.Is(err, content.ErrAudiobookExists):
		return AudiobookUploadResult{}, uploadErr(http.StatusConflict,
			"this book already has an audiobook")
	case errors.Is(err, calibre.ErrLocked):
		return AudiobookUploadResult{}, uploadErr(http.StatusConflict,
			"that Calibre library is open in Calibre; close it and try again")
	case errors.Is(err, calibre.ErrNoSuchBook):
		return AudiobookUploadResult{}, uploadErr(http.StatusGone,
			"that book is no longer in its Calibre library")
	case err != nil:
		slog.Error("audiobook upload: could not write", "book", book.ID, "err", err)
		return AudiobookUploadResult{}, uploadErr(http.StatusInternalServerError,
			"the audiobook could not be written to that folder")
	}

	result := AudiobookUploadResult{BookID: book.ID, ContentSHA256: spooled.sha}
	if ab, err := s.St.CatalogAudiobookByDigest(r.Context(), userID, spooled.sha); err == nil {
		result.AudiobookID = ab.ID
	}
	if link, ab, err := s.St.BookAudiobookByBookID(r.Context(), userID, book.ID); err == nil &&
		ab.ContentSHA256 == spooled.sha {
		result.AudiobookID = ab.ID
		result.State = string(link.State)
	}
	return result, nil
}

// ReceiveAudiobookTo is ReceiveAudiobook in the shape the web UI needs:
// whether the pass paired the file with the book, and nothing a JSON
// body alone has a use for.
func (s *Server) ReceiveAudiobookTo(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) (string, error) {
	result, err := s.ReceiveAudiobook(w, r, userID, bookID)
	return result.State, err
}

// audiobookUploadTarget resolves the book and its folder, and refuses
// everything that can be refused before a byte of a multi-gigabyte body
// is read.
func (s *Server) audiobookUploadTarget(
	ctx context.Context, userID, bookID string,
) (store.CatalogBook, store.Folder, error) {
	if s.Ingest == nil {
		return store.CatalogBook{}, store.Folder{}, uploadErr(http.StatusServiceUnavailable,
			"this server is running without a folder watcher")
	}
	if bookID == "" || len(bookID) > maxFolderIDBytes {
		return store.CatalogBook{}, store.Folder{}, uploadErr(http.StatusNotFound, "book not found")
	}
	book, err := s.St.CatalogBookByID(ctx, userID, bookID)
	if errors.Is(err, store.ErrNotFound) {
		return store.CatalogBook{}, store.Folder{}, uploadErr(http.StatusNotFound, "book not found")
	}
	if err != nil {
		return store.CatalogBook{}, store.Folder{}, uploadErr(http.StatusInternalServerError,
			"book lookup failed")
	}
	if book.Status != store.BookActive {
		return store.CatalogBook{}, store.Folder{}, uploadErr(http.StatusGone,
			"no active file for this book")
	}
	folder, err := s.uploadFolder(ctx, userID, book.FolderID)
	if err != nil {
		return store.CatalogBook{}, store.Folder{}, err
	}
	if err := s.refuseLinkedBook(ctx, book.ID); err != nil {
		return store.CatalogBook{}, store.Folder{}, err
	}
	return book, folder, nil
}

// refuseLinkedBook refuses a book that already has an audiobook. It
// asks without the viewer's grants: an audiobook in a folder this reader
// cannot see is still that book's audiobook, and a second one would
// take its pairing away from everybody who can.
func (s *Server) refuseLinkedBook(ctx context.Context, bookID string) error {
	switch _, _, err := s.St.BookAudiobookByBookID(ctx, "", bookID); {
	case err == nil:
		return uploadErr(http.StatusConflict, "this book already has an audiobook")
	case errors.Is(err, store.ErrNotFound):
		return nil
	default:
		return uploadErr(http.StatusInternalServerError, "catalog lookup failed")
	}
}

type spooledAudiobook struct {
	file *os.File
	sha  string
	size int64
}

func (u spooledAudiobook) cleanup() {
	name := u.file.Name()
	u.file.Close()
	_ = os.Remove(name)
}

// spoolAudiobook reads the multipart body into the cache directory,
// hashes it, and satisfies itself that it is an M4B with a duration.
func (s *Server) spoolAudiobook(
	w http.ResponseWriter, r *http.Request,
) (spooledAudiobook, error) {
	limit := s.Cfg.Content.MaxAudiobookUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	tooLarge := func(err error, status int, msg string) *WriteError {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return uploadErr(http.StatusRequestEntityTooLarge,
				fmt.Sprintf("an audiobook may be at most %d bytes", limit))
		}
		return uploadErr(status, msg)
	}

	part, _, err := multipartPublication(r)
	if err != nil {
		return spooledAudiobook{}, tooLarge(err, http.StatusBadRequest, err.Error())
	}
	defer part.Close()

	dir := s.Cfg.Content.CacheDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return spooledAudiobook{}, uploadErr(http.StatusInternalServerError,
			"upload could not be stored")
	}
	file, err := os.CreateTemp(dir, "upload-*.m4b")
	if err != nil {
		return spooledAudiobook{}, uploadErr(http.StatusInternalServerError,
			"upload could not be stored")
	}
	spooled := spooledAudiobook{file: file}
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), part)
	if err != nil {
		spooled.cleanup()
		if errors.Is(r.Context().Err(), context.Canceled) {
			return spooledAudiobook{}, uploadErr(clientClosedRequest, "the upload was cancelled")
		}
		return spooledAudiobook{}, tooLarge(err, http.StatusBadRequest, "the upload did not finish")
	}
	if size == 0 {
		spooled.cleanup()
		return spooledAudiobook{}, uploadErr(http.StatusBadRequest, "the upload was empty")
	}
	meta, err := m4b.ReadMetadataSeeker(file, size)
	if err == nil && meta.Duration > 0 {
		err = m4b.ValidateAudio(r.Context(), m4b.SeekerRangeReader(file, size), size)
	}
	if err != nil || meta.Duration <= 0 {
		spooled.cleanup()
		return spooledAudiobook{}, uploadErr(http.StatusUnprocessableEntity,
			"that file is not a playable M4B audiobook")
	}
	spooled.sha = hex.EncodeToString(digest.Sum(nil))
	spooled.size = size
	return spooled, nil
}
