package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/cover"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// BookIngest writes a publication into a folder and reconciles it. It is
// an interface so the API package does not depend on the content
// package's platform files, and so a nil value disables the route
// rather than panicking, exactly as Files does for downloads.
type BookIngest interface {
	Ingest(
		ctx context.Context, folder store.Folder, up content.Upload,
	) (string, error)
}

// HandleUploadBook implements POST /v1/folders/{folder}/books.
//
// ADR-0023: an upload is a file written into a folder. This handler
// never touches a catalog table. It checks that somebody asked for this
// folder to be writable, spools the body somewhere that is not a folder
// root, satisfies itself that the bytes are an EPUB, and then hands them
// to a pass — which is still the only thing that writes the catalog.
//
// The digest does the work a job table used to. A publication the
// catalog already holds is answered with the book it already made, so a
// client that retries a transfer over a bad connection costs one indexed
// lookup and creates nothing.
func (s *Server) HandleUploadBook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	folder, err := s.uploadFolder(r.Context(), tok.UserID, r.PathValue("folder"))
	if err != nil {
		writeUploadError(w, err)
		return
	}
	result, err := s.ReceiveUpload(w, r, folder, tok.UserID)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	if result.Duplicate {
		writeJSON(w, http.StatusOK, map[string]any{
			"book_id":        result.BookID,
			"folder_id":      result.FolderID,
			"content_sha256": result.ContentSHA256,
			"duplicate":      true,
		})
		return
	}
	// Rules 1 and 2 of ADR-0017 mean the pass may legitimately have
	// concluded nothing. The file is on the disk either way, so a
	// missing row is "not yet", not "failed": the watcher will come
	// back to it and the client resolves the book later.
	if result.BookID == "" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"folder_id":      result.FolderID,
			"relative_path":  result.RelativePath,
			"content_sha256": result.ContentSHA256,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"book_id":        result.BookID,
		"folder_id":      result.FolderID,
		"relative_path":  result.RelativePath,
		"content_sha256": result.ContentSHA256,
		"duplicate":      false,
	})
}

// UploadResult is what came of receiving one publication.
//
// An empty BookID with no Duplicate is the "not yet" case: the bytes are
// on the disk and the pass has not catalogued them.
type UploadResult struct {
	BookID        string
	FolderID      string
	RelativePath  string
	ContentSHA256 string
	Duplicate     bool
}

// WriteError is a refusal with the status and the sentence that goes
// with it, from either of the two writes this server makes to a folder:
// putting a book in (ADR-0023) and taking one out (ADR-0025). Every
// message is written to be shown to whoever asked, so a browser form
// can print it as it stands and the JSON API can put it in an error
// body.
type WriteError struct {
	Status  int
	Message string
}

func (e *WriteError) Error() string { return e.Message }

func uploadErr(status int, msg string) *WriteError {
	return &WriteError{Status: status, Message: msg}
}

func writeUploadError(w http.ResponseWriter, err error) {
	writeWriteError(w, err, "the upload failed")
}

// writeWriteError turns a refusal into an answer, and anything else
// into a 500 with the given sentence — never the underlying error,
// which may name a path on this server's disk.
func writeWriteError(w http.ResponseWriter, err error, fallback string) {
	var refusal *WriteError
	if errors.As(err, &refusal) {
		writeError(w, refusal.Status, refusal.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, fallback)
}

// uploadFolder resolves a folder and satisfies itself that somebody
// asked for it to be writable.
func (s *Server) uploadFolder(ctx context.Context, viewerID, id string) (store.Folder, error) {
	if s.Ingest == nil {
		return store.Folder{}, uploadErr(http.StatusServiceUnavailable,
			"this server is running without a folder watcher")
	}
	folder, err := s.St.FolderByID(ctx, viewerID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Folder{}, uploadErr(http.StatusNotFound, "no such folder")
	}
	if err != nil {
		return store.Folder{}, uploadErr(http.StatusInternalServerError,
			"folder lookup failed")
	}
	if !folder.AcceptsUploads {
		return store.Folder{}, uploadErr(http.StatusForbidden,
			"this folder does not accept uploads")
	}
	return folder, nil
}

// ReceiveUpload writes one publication from a multipart body into a
// folder and reconciles it, and is the whole of what an upload is. Both
// surfaces call it — the JSON route above and the browser form in the
// web UI — so the rules about what may be written where exist once.
//
// The response writer is here only so the body can be bounded; nothing
// is written to it. Every refusal comes back as an *WriteError holding
// the status and a sentence fit to show.
//
// userID is who is sending it, and is what lets the book be joined to
// the work they may already be syncing a position for. It is the only
// part of this that is per-reader; everything else about an upload is a
// file written into a folder.
func (s *Server) ReceiveUpload(
	w http.ResponseWriter, r *http.Request, folder store.Folder, userID string,
) (UploadResult, error) {
	result, err := s.receiveUpload(w, r, folder, userID)
	// Both answers that carry a book — the one just written and the one
	// the catalog already held — are joined here, so a retry over a bad
	// connection settles the work as surely as the first attempt did.
	if err == nil && result.BookID != "" {
		s.nameUploadedWork(r.Context(), userID, result.BookID)
	}
	return result, err
}

// nameUploadedWork joins a book just received to the sender's own work.
//
// Without this the server ends up holding both halves of one book and
// no link between them: a work with the reader's position and no file,
// because they had been syncing it from the device for weeks, and a
// catalog entry with the file and no reading. The library draws them
// side by side, which is how it was noticed.
//
// The client makes the same join when it resolves the book, but only a
// client that knows it uploaded anything. A book sent from the browser
// has no such client, and this is the only chance to do it.
//
// Never fatal, and never visible: the bytes are written and catalogued,
// which is all the upload promised. A join that failed is one the next
// resolve will make.
func (s *Server) nameUploadedWork(ctx context.Context, userID, bookID string) {
	if userID == "" {
		return
	}
	// Not confirmed: a title-and-author match is a guess, and sending a
	// file is not the reader saying yes to it. A digest match needs
	// nobody's permission, and is the case this exists for.
	result, _, err := s.resolveBookWork(ctx, userID, bookID, false, time.Now())
	switch {
	case err != nil:
		slog.Warn("upload: could not name the work", "book", bookID, "err", err)
	case len(result.ConflictingWorkIDs) > 0:
		slog.Info("upload: identifiers name more than one work", "book", bookID)
	}
}

func (s *Server) receiveUpload(
	w http.ResponseWriter, r *http.Request, folder store.Folder, userID string,
) (UploadResult, error) {
	if s.Ingest == nil {
		return UploadResult{}, uploadErr(http.StatusServiceUnavailable,
			"this server is running without a folder watcher")
	}
	spooled, err := s.spoolUpload(w, r)
	if err != nil {
		return UploadResult{}, err
	}
	defer spooled.cleanup()

	// From here to the write, one at a time: the check below is only
	// worth anything if nothing can be written between it and the
	// write it guards.
	s.uploading.Lock()
	defer s.uploading.Unlock()

	// The catalog may already hold these bytes, in this folder or
	// another. Either way the upload is done and this is the book.
	existing, err := s.St.CatalogBookByDigest(r.Context(), userID, spooled.sha)
	if err == nil {
		return UploadResult{
			BookID:        existing.ID,
			FolderID:      existing.FolderID,
			RelativePath:  existing.RelativePath,
			ContentSHA256: existing.ContentSHA256,
			Duplicate:     true,
		}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return UploadResult{}, uploadErr(http.StatusInternalServerError,
			"catalog lookup failed")
	}

	if _, err := spooled.file.Seek(0, io.SeekStart); err != nil {
		return UploadResult{}, uploadErr(http.StatusInternalServerError,
			"upload could not be read")
	}
	up := content.Upload{
		Source: spooled.file,
		Size:   spooled.size,
		Base: content.BookFilenameFrom(
			spooled.meta.Title, firstAuthor(spooled.meta), spooled.filename),
		Meta: spooled.meta,
	}
	// A Calibre book records whether it has a cover and Calibre draws
	// the cover.jpg beside it, so the cover has to be extracted here,
	// while the bytes are still in hand. A publication without one is
	// not an error: Calibre draws a placeholder.
	if folder.Kind == store.FolderCalibre {
		up.Cover = s.uploadedCover(r.Context(), spooled)
	}
	relative, err := s.Ingest.Ingest(r.Context(), folder, up)
	if errors.Is(err, content.ErrUploadsRefused) {
		return UploadResult{}, uploadErr(http.StatusForbidden,
			"this folder does not accept uploads")
	}
	if errors.Is(err, calibre.ErrLocked) {
		return UploadResult{}, uploadErr(http.StatusConflict,
			"that Calibre library is open in Calibre; close it and try again")
	}
	if err != nil {
		return UploadResult{}, uploadErr(http.StatusInternalServerError,
			"the book could not be written to that folder")
	}

	result := UploadResult{
		FolderID:      folder.ID,
		RelativePath:  relative,
		ContentSHA256: spooled.sha,
	}
	if book, err := s.St.CatalogBookByDigest(r.Context(), userID, spooled.sha); err == nil {
		result.BookID = book.ID
		result.RelativePath = book.RelativePath
	}
	return result, nil
}

// ReceiveUploadTo is ReceiveUpload in the shape the web UI needs: the
// book's id and whether the catalog already held these bytes, without
// the parts only a JSON body has a use for. It lives here so that the
// web package can delegate to these rules while still depending on
// nothing but the store.
func (s *Server) ReceiveUploadTo(
	w http.ResponseWriter, r *http.Request, folder store.Folder, userID string,
) (string, bool, error) {
	result, err := s.ReceiveUpload(w, r, folder, userID)
	return result.BookID, result.Duplicate, err
}

// spooledUpload is a validated publication in a temporary file, which is
// under the cache directory and therefore not under any folder root. A
// transfer that is abandoned half way is thus never visible to a pass —
// which is the whole of what the old abandoned-upload sweep did, done by
// writing somewhere else instead of by a background worker.
type spooledUpload struct {
	file     *os.File
	sha      string
	size     int64
	filename string
	meta     epub.Metadata
}

// uploadedCover renders the publication's cover as the JPEG Calibre
// keeps beside a book. Every failure is nil and silent: a cover is
// decoration, and refusing an upload because its artwork would not
// decode would be refusing the book over the picture on it.
func (s *Server) uploadedCover(
	ctx context.Context, spooled spooledUpload,
) []byte {
	image, err := epub.ReadCover(ctx, spooled.file, spooled.size,
		s.Cfg.EPUBLimits(), maxCoverSourceBytes)
	if err != nil {
		return nil
	}
	rendered, err := cover.Render(image.Data, cover.SizeFull, cover.DefaultLimits())
	if err != nil {
		return nil
	}
	return rendered
}

func (u spooledUpload) cleanup() {
	name := u.file.Name()
	u.file.Close()
	_ = os.Remove(name)
}

// spoolUpload reads the multipart body, hashes and validates it. Every
// refusal comes back as an *WriteError; nothing is written to the
// response writer, which is here only to bound the body.
func (s *Server) spoolUpload(
	w http.ResponseWriter, r *http.Request,
) (spooledUpload, error) {
	limit := s.Cfg.Content.MaxUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	part, filename, err := multipartPublication(r)
	if err != nil {
		return spooledUpload{}, oversizeOr(limit, err,
			http.StatusBadRequest, err.Error())
	}
	defer part.Close()

	dir := s.Cfg.Content.CacheDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return spooledUpload{}, uploadErr(http.StatusInternalServerError,
			"upload could not be stored")
	}
	file, err := os.CreateTemp(dir, "upload-*.epub")
	if err != nil {
		return spooledUpload{}, uploadErr(http.StatusInternalServerError,
			"upload could not be stored")
	}
	spooled := spooledUpload{file: file, filename: filename}

	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), part)
	if err != nil {
		spooled.cleanup()
		return spooledUpload{}, oversizeOr(limit, err,
			http.StatusBadRequest, "the upload did not finish")
	}
	if size == 0 {
		spooled.cleanup()
		return spooledUpload{}, uploadErr(http.StatusBadRequest,
			"the upload was empty")
	}

	result, err := epub.Validate(r.Context(), file, size, s.Cfg.EPUBLimits())
	if err != nil {
		spooled.cleanup()
		code, _ := epub.ErrorCode(err)
		return spooledUpload{}, uploadErr(http.StatusUnprocessableEntity,
			"that file is not a readable EPUB: "+string(code))
	}
	spooled.sha = hex.EncodeToString(digest.Sum(nil))
	spooled.size = size
	spooled.meta = result.Metadata
	return spooled, nil
}

// oversizeOr answers 413 when the body ran past the bound and the
// caller's own refusal otherwise.
//
// The bound can be reached while reading the multipart headers rather
// than the publication, and a client that sent too much should be told
// the same thing either way rather than being told its request was
// malformed.
func oversizeOr(limit int64, err error, status int, msg string) *WriteError {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return uploadErr(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("a book may be at most %d bytes", limit))
	}
	return uploadErr(status, msg)
}

// multipartPublication finds the one part that matters. The field is
// named "file"; anything else in the body is ignored rather than
// refused, so a browser form that also posts a CSRF token works without
// this handler knowing about forms.
func multipartPublication(r *http.Request) (io.ReadCloser, string, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, "", errors.New("the body must be multipart/form-data")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", fmt.Errorf("the body must be multipart/form-data: %w", err)
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, "", errors.New("the body has no \"file\" part")
		}
		if err != nil {
			return nil, "", fmt.Errorf("the body could not be read: %w", err)
		}
		if part.FormName() == "file" {
			return part, filepath.Base(part.FileName()), nil
		}
		part.Close()
	}
}

// firstAuthor is the name a filename is built from. A publication with
// several contributors gets the first, because a filename is a
// convenience and the catalog keeps all of them anyway.
func firstAuthor(m epub.Metadata) string {
	for _, c := range m.Contributors {
		if c.Role == "aut" || c.Role == "author" {
			return c.Name
		}
	}
	if len(m.Contributors) > 0 {
		return m.Contributors[0].Name
	}
	return ""
}
