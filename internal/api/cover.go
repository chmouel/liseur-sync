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

// CoverCache holds rendered covers. It is a cache and nothing else, so
// every method may fail without failing the request: a miss is
// re-rendered and a failed write is re-attempted next time. Deleting the
// whole of it while the server runs costs re-renders and nothing more.
type CoverCache interface {
	OpenCover(ctx context.Context, sha256, variant string) (*os.File, int64, error)
	StoreCover(ctx context.Context, sha256, variant string, data []byte) error
	MarkCoverAbsent(ctx context.Context, sha256 string) error
}

// HandleBookCover implements GET /v1/books/{id}/cover.
func (s *Server) HandleBookCover(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.ServeBookCover(w, r, readerID(r), r.PathValue("id"))
}

// ServeBookCover is the cover itself, without the token, so the web UI
// can serve it under a cookie session without a second copy of the
// rules.
//
// A cover is derived on demand rather than extracted when the book was
// found. A pass records where the image is and what it hashes to and
// nothing else: writing rendered images during a scan would mean the
// server doing work for books nobody looks at, and a cache that has to
// be kept in step with a folder somebody else edits.
func (s *Server) ServeBookCover(
	w http.ResponseWriter, r *http.Request, viewerID, bookID string,
) {
	if s.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	size, ok := cover.ParseSize(r.URL.Query().Get("size"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown cover size")
		return
	}
	book, err := s.St.CatalogBookByID(r.Context(), viewerID, bookID)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	if book.Status != store.BookActive {
		writeError(w, http.StatusGone, "no downloadable file for this book")
		return
	}
	key := coverCacheKey(book)

	if s.Covers != nil {
		cached, _, err := s.Covers.OpenCover(r.Context(), key, string(size))
		switch {
		case err == nil:
			defer cached.Close()
			s.writeCover(w, r, size, key, cached, book.UpdatedAt)
			return
		case errors.Is(err, content.ErrNoCover):
			// Already established: this publication has no cover this
			// server can serve. Answered without opening the book.
			if s.writePlaceholderIcon(w, r, size) {
				return
			}
			writeError(w, http.StatusNotFound, "no cover for this book")
			return
		}
	}

	// What comes back says which image it is, which is not always the
	// one the row named: a chosen cover that has gone missing falls back
	// to the publication's. Caching that under the chosen cover's key
	// would answer for the cover once it returned, and marking it absent
	// there would do so permanently.
	rendered, renderedKey, err := s.renderCover(r.Context(), book, size)
	if err != nil {
		s.writeCoverError(r.Context(), w, r, size, book, renderedKey, err)
		return
	}
	if s.Covers != nil {
		// A cache that cannot be written still serves: the bytes are in
		// hand, and a full disk should cost the next request a re-render
		// rather than cost this one its answer.
		_ = s.Covers.StoreCover(r.Context(), renderedKey, string(size), rendered)
	}
	s.writeCover(w, r, size, renderedKey,
		bytes.NewReader(rendered), book.UpdatedAt)
}

// coverCacheKey names the rendered image exactly.
//
// It is the publication's digest for a book whose cover comes out of the
// EPUB, and the cover's own digest for one whose folder holds a chosen
// cover beside it. The distinction is load-bearing: two Calibre books can
// share one EPUB and have different covers, so a publication-keyed cache
// would serve one book's cover for the other, and would cache "this book
// has no cover" for both.
func coverCacheKey(book store.CatalogBook) string {
	if book.CoverSHA256 != "" {
		return book.CoverSHA256
	}
	return book.ContentSHA256
}

// renderCover produces one variant from whichever image this book's
// cover is: the one its folder holds beside it, or the one the
// publication declares.
func (s *Server) renderCover(
	ctx context.Context, book store.CatalogBook, size cover.Size,
) ([]byte, string, error) {
	if book.CoverSHA256 != "" && book.CoverRelativePath != nil {
		return s.renderStoredCover(ctx, book, size)
	}
	file, fileSize, err := s.Files.OpenBook(ctx, book)
	if err != nil {
		return nil, book.ContentSHA256, err
	}
	defer file.Close()

	image, err := epub.ReadCover(ctx, file, fileSize,
		epub.DefaultLimits(), maxCoverSourceBytes)
	if err != nil {
		return nil, book.ContentSHA256, err
	}
	rendered, err := cover.Render(image.Data, size, cover.DefaultLimits())
	if err != nil {
		return nil, book.ContentSHA256, err
	}
	return rendered, book.ContentSHA256, nil
}

// renderStoredCover renders the image the folder itself holds — the
// cover.jpg beside a Calibre book. A cover that has gone away, or whose
// bytes no longer match the digest recorded for it, falls back to the
// publication's own: the book is still there, and a side file its owner
// changed is not a reason to stop showing it.
func (s *Server) renderStoredCover(
	ctx context.Context, book store.CatalogBook, size cover.Size,
) ([]byte, string, error) {
	image, imageSize, err := s.Files.OpenBookCover(ctx, book)
	if err != nil {
		stripped := book
		stripped.CoverSHA256, stripped.CoverRelativePath = "", nil
		return s.renderCover(ctx, stripped, size)
	}
	defer image.Close()
	if imageSize > maxCoverSourceBytes {
		return nil, book.CoverSHA256, cover.ErrUnsupported
	}
	data, err := io.ReadAll(io.LimitReader(image, maxCoverSourceBytes))
	if err != nil {
		return nil, book.CoverSHA256, err
	}
	rendered, err := cover.Render(data, size, cover.DefaultLimits())
	if err != nil {
		return nil, book.CoverSHA256, err
	}
	return rendered, book.CoverSHA256, nil
}

// writeCoverError maps a failed render onto a status, and remembers the
// permanent failures so the next request does not repeat the work. A
// publication that declares no cover, declares one this server cannot
// decode, or is not a readable archive will never produce one, and the
// blob cannot change: that answer is cacheable forever.
func (s *Server) writeCoverError(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	size cover.Size, book store.CatalogBook, digest string, err error,
) {
	switch {
	case errors.Is(err, content.ErrStageMissing),
		errors.Is(err, content.ErrRootMissing),
		errors.Is(err, content.ErrUnsafePath):
		// Not permanent in the same sense: an unmounted folder comes
		// back, so this is not remembered.
		writeError(w, http.StatusGone, "content is no longer stored")
	case errors.Is(err, content.ErrSourceChanged):
		// The bytes at that path are not the ones catalogued, so no
		// image derived from them is this book's cover. The next pass
		// re-reads the file.
		writeError(w, http.StatusConflict,
			"the file behind this book changed on disk")
	case errors.Is(err, epub.ErrNoCover), errors.Is(err, cover.ErrUnsupported),
		isValidationFailure(err):
		if s.Covers != nil {
			_ = s.Covers.MarkCoverAbsent(ctx, digest)
		}
		if s.writePlaceholderIcon(w, r, size) {
			return
		}
		writeError(w, http.StatusNotFound, "no cover for this book")
	default:
		writeError(w, http.StatusInternalServerError, "cover rendering failed")
	}
}

// writePlaceholderIcon answers "no cover" with a drawn card when the
// asker needed an icon rather than an answer. A tab with no icon probes
// /favicon.ico instead, which is the same request by a longer road, so
// the icon variant — and only it — degrades to a placeholder rather
// than a 404. It reports whether it answered.
func (s *Server) writePlaceholderIcon(
	w http.ResponseWriter, r *http.Request, size cover.Size,
) bool {
	if size != cover.SizeIcon {
		return false
	}
	body := cover.PlaceholderIcon()
	if body == nil {
		return false
	}
	// The placeholder is fixed, so the usual validators do not apply to
	// it; a fresh fetch costs nothing at this size.
	w.Header().Set("Content-Type", cover.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	return true
}

// isValidationFailure reports whether an archive was refused rather than
// merely lacking a cover. Both mean the same thing here — nothing to
// serve — but only this branch knows the difference, which is why the
// caller does not have to.
func isValidationFailure(err error) bool {
	var failure *epub.ValidationError
	return errors.As(err, &failure)
}

// writeCover sends one rendered variant. `digest` names the image the
// bytes came from — the chosen cover's, or the publication's when that
// is what was rendered — so a fallback never borrows the ETag of a cover
// it did not use.
func (s *Server) writeCover(
	w http.ResponseWriter, r *http.Request,
	size cover.Size, digest string, body io.ReadSeeker, modified time.Time,
) {
	// The type is what this server chose to produce, never what the
	// publication claimed. With nosniff, that is what stops a cover from
	// being interpreted as anything else.
	w.Header().Set("Content-Type", cover.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A cover is derived from immutable bytes by a fixed pipeline, so the
	// digest and the variant together name the result exactly.
	// The bytes belong to somebody else, who may replace them, so the
	// derived image is revalidated rather than pinned for a year.
	w.Header().Set("ETag", `W/"`+digest+`-`+string(size)+`"`)
	w.Header().Set("Cache-Control", "private, no-cache")
	// An image is displayed, not saved, so there is no filename here and
	// nothing to sanitize.
	http.ServeContent(&catalogErrorWriter{ResponseWriter: w}, r, "", modified, body)
}
