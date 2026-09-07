package api

import (
	"encoding/json"
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
	p, err := epub.OpenPublication(r.Context(), file, size, epub.DefaultLimits())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid or unsupported EPUB")
		return
	}
	defer p.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	resource := r.PathValue("resource")
	if resource != "" {
		if !p.HasResource(resource) {
			writeError(w, http.StatusNotFound, "publication resource not found")
			return
		}
		data, size, err := p.OpenResource(resource)
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
		locators := p.Positions(r.Context())
		value = map[string]any{"total": len(locators), "positions": locators}
		mediaType = "application/vnd.readium.position-list+json"
	} else {
		value = p.Manifest
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
	publicationURLs(body, version+"resources/", p)
	if mediaType == "application/webpub+json" {
		body["links"] = []map[string]any{
			{"rel": "self", "href": base + "manifest.json", "type": mediaType},
			{"rel": "http://readium.org/position-list", "href": version + "positions.json", "type": "application/vnd.readium.position-list+json"},
			{"rel": "package", "href": version + "resources/" + (&url.URL{Path: p.PackagePath}).String(), "type": "application/oebps-package+xml"},
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

func publicationURLs(value any, prefix string, p *epub.Publication) {
	switch v := value.(type) {
	case map[string]any:
		if href, ok := v["href"].(string); ok {
			if u, err := url.Parse(href); err == nil && !u.IsAbs() && u.Host == "" && p.HasResource(u.Path) {
				v["href"] = prefix + u.String()
			}
		}
		for _, child := range v {
			publicationURLs(child, prefix, p)
		}
	case []any:
		for _, child := range v {
			publicationURLs(child, prefix, p)
		}
	}
}
