package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.Ingest == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this server is running without a folder watcher")
		return
	}
	folder, err := s.St.FolderByID(r.Context(), r.PathValue("folder"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such folder")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder lookup failed")
		return
	}
	if !folder.AcceptsUploads {
		writeError(w, http.StatusForbidden,
			"this folder does not accept uploads")
		return
	}

	spooled, err := s.spoolUpload(w, r)
	if err != nil {
		return // spoolUpload has answered.
	}
	defer spooled.cleanup()

	// The catalog may already hold these bytes, in this folder or
	// another. Either way the upload is done and this is the book.
	existing, err := s.St.CatalogBookByDigest(r.Context(), spooled.sha)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"book_id":        existing.ID,
			"folder_id":      existing.FolderID,
			"content_sha256": existing.ContentSHA256,
			"duplicate":      true,
		})
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "catalog lookup failed")
		return
	}

	if _, err := spooled.file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "upload could not be read")
		return
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
		writeError(w, http.StatusForbidden, "this folder does not accept uploads")
		return
	}
	if errors.Is(err, calibre.ErrLocked) {
		writeError(w, http.StatusConflict,
			"that Calibre library is open in Calibre; close it and try again")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the book could not be written to that folder")
		return
	}

	// Rules 1 and 2 of ADR-0017 mean the pass may legitimately have
	// concluded nothing. The file is on the disk either way, so a
	// missing row is "not yet", not "failed": the watcher will come
	// back to it and the client resolves the book later.
	book, err := s.St.CatalogBookByDigest(r.Context(), spooled.sha)
	if err != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"folder_id":      folder.ID,
			"relative_path":  relative,
			"content_sha256": spooled.sha,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"book_id":        book.ID,
		"folder_id":      book.FolderID,
		"relative_path":  book.RelativePath,
		"content_sha256": book.ContentSHA256,
		"duplicate":      false,
	})
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

// spoolUpload reads the multipart body, hashes and validates it, and
// answers the request itself on every failure. A non-nil error means the
// response is already written.
func (s *Server) spoolUpload(
	w http.ResponseWriter, r *http.Request,
) (spooledUpload, error) {
	limit := s.Cfg.Content.MaxUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	part, filename, err := multipartPublication(r)
	if err != nil {
		writeOversizeOr(w, limit, err, http.StatusBadRequest, err.Error())
		return spooledUpload{}, err
	}
	defer part.Close()

	dir := s.Cfg.Content.CacheDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "upload could not be stored")
		return spooledUpload{}, err
	}
	file, err := os.CreateTemp(dir, "upload-*.epub")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload could not be stored")
		return spooledUpload{}, err
	}
	spooled := spooledUpload{file: file, filename: filename}

	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), part)
	if err != nil {
		spooled.cleanup()
		writeOversizeOr(w, limit, err,
			http.StatusBadRequest, "the upload did not finish")
		return spooledUpload{}, err
	}
	if size == 0 {
		spooled.cleanup()
		err := errors.New("the upload was empty")
		writeError(w, http.StatusBadRequest, err.Error())
		return spooledUpload{}, err
	}

	result, err := epub.Validate(r.Context(), file, size, s.Cfg.EPUBLimits())
	if err != nil {
		spooled.cleanup()
		code, _ := epub.ErrorCode(err)
		writeError(w, http.StatusUnprocessableEntity,
			"that file is not a readable EPUB: "+string(code))
		return spooledUpload{}, err
	}
	spooled.sha = hex.EncodeToString(digest.Sum(nil))
	spooled.size = size
	spooled.meta = result.Metadata
	return spooled, nil
}

// writeOversizeOr answers 413 when the body ran past the bound and the
// caller's own answer otherwise.
//
// The bound can be reached while reading the multipart headers rather
// than the publication, and a client that sent too much should be told
// the same thing either way rather than being told its request was
// malformed.
func writeOversizeOr(
	w http.ResponseWriter, limit int64, err error, status int, msg string,
) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("a book may be at most %d bytes", limit))
		return
	}
	writeError(w, status, msg)
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
