package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandlePublication serves an EPUB's Readium manifest, positions, or one
// resource. All three pass through the same grant and file-version checks.
func (s *Server) HandlePublication(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	book, err := s.St.CatalogBookByID(r.Context(), readerID(r), r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	if book.Status != store.BookActive {
		writeError(w, http.StatusGone, "no readable file for this book")
		return
	}
	if digest := r.PathValue("digest"); digest != "" && digest != book.ContentSHA256 {
		writeError(w, http.StatusConflict, "publication changed; reload the book")
		return
	}
	file, size, err := s.Files.OpenBook(r.Context(), book)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	defer file.Close()
	idx, err := s.publicationIndex(r.Context(), book.ContentSHA256, file, size)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid or unsupported EPUB")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	resource := r.PathValue("resource")
	if resource != "" {
		if !idx.HasResource(resource) {
			writeError(w, http.StatusNotFound, "publication resource not found")
			return
		}
		data, size, err := epub.OpenIndexedResource(file, size, idx, resource)
		if errors.Is(err, epub.ErrPublicationChanged) {
			if s.PublicationIndexes != nil {
				s.PublicationIndexes.Evict(book.ContentSHA256)
			}
			writeError(w, http.StatusConflict, "publication changed; reload the book")
			return
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid publication resource")
			return
		}
		defer data.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="resource"`)
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		if r.Method != http.MethodHead {
			_, _ = io.CopyN(w, data, size)
		}
		return
	}
	base := "/v1/books/" + url.PathEscape(book.ID) + "/publication/"
	version := base + book.ContentSHA256 + "/"
	var value any
	mediaType := "application/webpub+json"
	if strings.HasSuffix(r.URL.Path, "/positions.json") {
		value = map[string]any{"total": len(idx.Positions), "positions": idx.Positions}
		mediaType = "application/vnd.readium.position-list+json"
	} else {
		value = idx.Manifest
	}
	// Use the toolkit's serializers, then add transport URLs. Stored locators
	// remain publication-relative; the reader removes this transport prefix.
	encoded, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "publication encoding failed")
		return
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		writeError(w, http.StatusInternalServerError, "publication encoding failed")
		return
	}
	publicationURLs(body, version+"resources/", idx)
	if mediaType == "application/webpub+json" {
		body["links"] = []map[string]any{
			{"rel": "self", "href": base + "manifest.json", "type": mediaType},
			{"rel": "http://readium.org/position-list", "href": version + "positions.json", "type": "application/vnd.readium.position-list+json"},
			{"rel": "package", "href": version + "resources/" + (&url.URL{Path: idx.PackagePath}).String(), "type": "application/oebps-package+xml"},
		}
	}
	encoded, err = json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "publication encoding failed")
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(encoded)
	}
}

// publicationIndex returns the cached epub.Index for digest, or opens and
// parses the archive and caches the result. This is the one place a
// publication resource request pays for preflightZIP, control document
// validation and the toolkit parser; every other request for the same
// digest reuses the result.
func (s *Server) publicationIndex(ctx context.Context, digest string, file io.ReaderAt, size int64) (*epub.Index, error) {
	if s.PublicationIndexes != nil {
		if idx, ok := s.PublicationIndexes.Get(digest); ok {
			return idx, nil
		}
	}
	p, err := epub.OpenPublication(ctx, file, size, epub.DefaultLimits())
	if err != nil {
		return nil, err
	}
	defer p.Close()
	idx := p.Index(ctx)
	if s.PublicationIndexes != nil && len(idx.Positions) <= maxCachedPublicationPositions {
		s.PublicationIndexes.Put(digest, idx)
	}
	return idx, nil
}

// maxCachedPublicationPositions bounds what the index cache retains. The
// toolkit generates one position per 1,024 bytes of reading-order content,
// so a publication near this server's own upper size limits can produce
// well over a million of them; retaining that list across the cache's
// bounded entry count could itself become the memory problem the cache
// exists to avoid. A publication over this bound still serves correctly —
// its index is simply never cached, so the one request that built it pays
// the reparse cost the cache is meant to save, rather than every request
// paying to keep it in memory indefinitely.
const maxCachedPublicationPositions = 200_000

func publicationURLs(value any, prefix string, idx *epub.Index) {
	switch v := value.(type) {
	case map[string]any:
		if href, ok := v["href"].(string); ok {
			if u, err := url.Parse(href); err == nil && !u.IsAbs() && u.Host == "" && idx.HasResource(u.Path) {
				v["href"] = prefix + u.String()
			}
		}
		for _, child := range v {
			publicationURLs(child, prefix, idx)
		}
	case []any:
		for _, child := range v {
			publicationURLs(child, prefix, idx)
		}
	}
}
