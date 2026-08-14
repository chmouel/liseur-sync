package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/cover"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// maxCoverSourceBytes bounds the cover entry read out of an archive.
// Sixteen megabytes is far past any real cover and still small enough that
// a request cannot be used to make the server hold an arbitrary amount of
// an attacker-controlled file in memory.
const maxCoverSourceBytes = 16 << 20

// CoverCache is the rendered-cover side of the CAS. It is a cache, so
// every method may fail without failing the request: a miss is re-rendered
// and a failed write is re-attempted next time.
type CoverCache interface {
	OpenCover(ctx context.Context, sha256, variant string) (*os.File, int64, error)
	StoreCover(ctx context.Context, sha256, variant string, data []byte) error
	MarkCoverAbsent(ctx context.Context, sha256 string) error
}

// HandleBookCover implements GET /v1/books/{id}/cover.
func (s *Server) HandleBookCover(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.ServeBookCover(w, r, tok.UserID, r.PathValue("id"))
}

// ServeBookCover is the cover itself, without the token, so the web UI can
// serve it under a cookie session without a second copy of the rules.
//
// A cover is derived from the blob rather than recorded when the book was
// ingested. The blob is immutable, so the derivation is deterministic and
// can be repeated at any time; recording it instead would mean a schema
// change, a promotion path change, and a backfill for every book already
// in the catalog, to store something that can simply be recomputed.
func (s *Server) ServeBookCover(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) {
	if s.Blobs == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	size, ok := cover.ParseSize(r.URL.Query().Get("size"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown cover size")
		return
	}
	file, ok := s.coverBookFile(w, r, userID, bookID)
	if !ok {
		return
	}

	if s.Covers != nil {
		cached, _, err := s.Covers.OpenCover(r.Context(), file.ContentSHA256, string(size))
		switch {
		case err == nil:
			defer cached.Close()
			s.writeCover(w, r, file, size, cached, file.UpdatedAt)
			return
		case errors.Is(err, content.ErrNoCover):
			// Already established: this publication has no cover this
			// server can serve. Answered without opening the book.
			writeError(w, http.StatusNotFound, "no cover for this book")
			return
		}
	}

	rendered, err := s.renderCover(r.Context(), file, size)
	if err != nil {
		if errors.Is(err, content.ErrSourceChanged) {
			s.flagChangedSource(r.Context(), file)
		}
		s.writeCoverError(r.Context(), w, file.ContentSHA256, err)
		return
	}
	if s.Covers != nil {
		// A cache that cannot be written still serves: the bytes are in
		// hand, and a full disk should cost the next request a re-render
		// rather than cost this one its answer.
		_ = s.Covers.StoreCover(r.Context(), file.ContentSHA256, string(size), rendered)
	}
	s.writeCover(w, r, file, size,
		bytes.NewReader(rendered), file.UpdatedAt)
}

// coverBookFile resolves a book to the file its cover comes from, using
// the same order as a download: the catalog decides whether anything may
// be served, and only then are the files consulted.
func (s *Server) coverBookFile(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) (store.BookFile, bool) {
	if _, err := s.St.CatalogBookByID(
		r.Context(), userID, bookID, store.LibraryRoleRead,
	); err != nil {
		writeCatalogError(w, err, "book not found")
		return store.BookFile{}, false
	}
	files, err := s.St.ListBookFiles(r.Context(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return store.BookFile{}, false
	}
	for i := range files {
		if files[i].Availability == store.BookFileAvailable {
			return files[i], true
		}
	}
	writeError(w, http.StatusGone, "no downloadable file for this book")
	return store.BookFile{}, false
}

// renderCover produces one variant from the book's own bytes, wherever
// they live.
func (s *Server) renderCover(
	ctx context.Context, file store.BookFile, size cover.Size,
) ([]byte, error) {
	blob, blobSize, err := s.Blobs.OpenBookFile(ctx, file)
	if err != nil {
		return nil, err
	}
	defer blob.Close()

	image, err := epub.ReadCover(ctx, blob, blobSize,
		epub.DefaultLimits(), maxCoverSourceBytes)
	if err != nil {
		return nil, err
	}
	rendered, err := cover.Render(image.Data, size, cover.DefaultLimits())
	if err != nil {
		return nil, err
	}
	return rendered, nil
}

// writeCoverError maps a failed render onto a status, and remembers the
// permanent failures so the next request does not repeat the work. A
// publication that declares no cover, declares one this server cannot
// decode, or is not a readable archive will never produce one, and the
// blob cannot change: that answer is cacheable forever.
func (s *Server) writeCoverError(
	ctx context.Context, w http.ResponseWriter, digest string, err error,
) {
	switch {
	case errors.Is(err, content.ErrStageMissing):
		// Not permanent in the same sense: the blob may come back from a
		// backup, so this is not remembered.
		writeError(w, http.StatusGone, "content is no longer stored")
	case errors.Is(err, epub.ErrNoCover), errors.Is(err, cover.ErrUnsupported),
		isValidationFailure(err):
		if s.Covers != nil {
			_ = s.Covers.MarkCoverAbsent(ctx, digest)
		}
		writeError(w, http.StatusNotFound, "no cover for this book")
	default:
		writeError(w, http.StatusInternalServerError, "cover rendering failed")
	}
}

// isValidationFailure reports whether an archive was refused rather than
// merely lacking a cover. Both mean the same thing here — nothing to
// serve — but only this branch knows the difference, which is why the
// caller does not have to.
func isValidationFailure(err error) bool {
	var failure *epub.ValidationError
	return errors.As(err, &failure)
}

func (s *Server) writeCover(
	w http.ResponseWriter, r *http.Request, file store.BookFile,
	size cover.Size, body io.ReadSeeker, modified time.Time,
) {
	digest := file.ContentSHA256
	// The type is what this server chose to produce, never what the
	// publication claimed. With nosniff, that is what stops a cover from
	// being interpreted as anything else.
	w.Header().Set("Content-Type", cover.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A cover is derived from immutable bytes by a fixed pipeline, so the
	// digest and the variant together name the result exactly.
	if file.Storage == store.LibraryStorageInPlace {
		// The bytes belong to somebody else, who may replace them, so the
		// derived image is revalidated rather than pinned for a year.
		w.Header().Set("ETag", `W/"`+digest+`-`+string(size)+`"`)
		w.Header().Set("Cache-Control", "private, no-cache")
	} else {
		w.Header().Set("ETag", `"`+digest+`-`+string(size)+`"`)
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	// An image is displayed, not saved, so there is no filename here and
	// nothing to sanitize.
	http.ServeContent(&catalogErrorWriter{ResponseWriter: w}, r, "", modified, body)
}
