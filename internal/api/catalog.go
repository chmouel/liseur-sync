package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// defaultCatalogPageSize is what a client gets when it does not ask. The
// cap matches the store's own bound.
const (
	defaultCatalogPageSize = 50
	maxCatalogPageSize     = 200
)

// maxFolderIDBytes bounds a folder id taken from a path. An id is a
// UUID; anything longer is not one, and refusing it here keeps an
// oversized string out of every query below.
const maxFolderIDBytes = 128

// BookFiles is the read side of content: what a download and a cover
// need and nothing more. A book names a path relative to its folder's
// root, and resolving that root and opening under it read-only is the
// content package's job, not a handler's (ADR-0017).
type BookFiles interface {
	OpenBook(ctx context.Context, book store.CatalogBook) (*os.File, int64, error)
	// OpenBookCover opens the cover a folder's curator chose for this
	// book, where there is one. It is separate from OpenBook because the
	// cover is not the publication: it lives beside it under the folder
	// root and has its own digest.
	OpenBookCover(ctx context.Context, book store.CatalogBook) (*os.File, int64, error)
}

// HandleFolders implements GET /v1/folders — every folder this server
// watches. This is where a client starts, because every other catalog
// route needs a folder id.
//
// root_path is deliberately absent: it is a filesystem oracle and no
// part of reading a catalog. Only the admin UI shows it.
func (s *Server) HandleFolders(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	after := r.URL.Query().Get("after")
	if len(after) > 512 {
		writeError(w, http.StatusBadRequest, "cursor is too long")
		return
	}
	folders, err := s.St.ListFolders(r.Context(), after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder lookup failed")
		return
	}
	out := make([]map[string]any, 0, len(folders))
	for _, f := range folders {
		out = append(out, map[string]any{
			"folder_id":  f.ID,
			"name":       f.Name,
			"kind":       string(f.Kind),
			"created_at": f.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	body := map[string]any{"folders": out}
	if len(folders) == limit {
		last := folders[len(folders)-1]
		body["next_after"] = store.FolderCursor(last)
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleFolderBooks implements GET /v1/folders/{folder}/books.
func (s *Server) HandleFolderBooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	folderID := r.PathValue("folder")
	if folderID == "" || len(folderID) > maxFolderIDBytes {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	after, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The order is a parameter rather than a second route because it is
	// the same collection either way; only "which end" changes. An
	// unrecognized value is refused rather than defaulted, so a client
	// with a typo learns about it instead of silently reading the
	// catalog backwards.
	list := s.St.ListCatalogBooks
	switch order := r.URL.Query().Get("order"); order {
	case "", "oldest":
	case "recent":
		list = s.St.ListRecentCatalogBooks
	default:
		writeError(w, http.StatusBadRequest, "order must be oldest or recent")
		return
	}

	books, err := list(r.Context(), folderID, after, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	out, err := s.catalogBooksJSON(r.Context(), books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	body := map[string]any{"books": out}
	// Only advertise a cursor on a full page. A short page is the end of
	// the catalog, and handing back a cursor there invites a pointless
	// round trip that returns nothing.
	if len(books) == limit {
		last := books[len(books)-1]
		body["next_cursor"] = encodeCatalogCursor(store.CatalogBookCursor{
			CreatedAt: last.CreatedAt, ID: last.ID,
		})
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleBook implements GET /v1/books/{id}, the detail view. It returns
// the same shape as a listing row — files included — so a client parses
// one representation of a book and not two.
func (s *Server) HandleBook(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	book, err := s.St.CatalogBookByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	body, err := s.catalogBookBodyJSON(r.Context(), book)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "book lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleBookDownload implements GET and HEAD
// /v1/books/{id}/download. It hands the open file to
// http.ServeContent, which is what gives us range requests, conditional
// requests and HEAD without writing any of that by hand.
func (s *Server) HandleBookDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.ServeBookDownload(w, r, r.PathValue("id"))
}

// ServeBookDownload is the download itself, without the token. The web
// UI authenticates with a cookie session but must serve bytes under the
// same rules — the media-type allowlist and the filename sanitizing
// below are what keep a hostile file from becoming a hostile download,
// and there must not be a second copy of them.
func (s *Server) ServeBookDownload(
	w http.ResponseWriter, r *http.Request, bookID string,
) {
	if s.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	book, err := s.St.CatalogBookByID(r.Context(), bookID)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	if book.Status != store.BookActive {
		// The book is catalogued but the last pass could not find its
		// file. That is not a 404 on the book; it is the content being
		// gone.
		writeError(w, http.StatusGone, "no downloadable file for this book")
		return
	}

	// ServeContent finds the length by seeking, so the size reported
	// here is redundant.
	file, _, err := s.Files.OpenBook(r.Context(), book)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", downloadMediaType(book.MediaType))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(downloadFilename(book)))
	// Nobody here owns those bytes: their owner may replace them between
	// two requests, so the validator is weak and the answer is
	// revalidated rather than cached as immutable.
	w.Header().Set("ETag", `W/"`+book.ContentSHA256+`"`)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(&catalogErrorWriter{ResponseWriter: w}, r, "",
		book.UpdatedAt.UTC(), file)
}

// writeDownloadError maps an open failure onto the status that says what
// actually happened. A file the catalog knows about but the disk no
// longer holds is gone, not missing; one whose bytes changed under it is
// a conflict the next pass resolves.
func writeDownloadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, content.ErrStageMissing),
		errors.Is(err, content.ErrRootMissing),
		errors.Is(err, content.ErrUnsafePath):
		writeError(w, http.StatusGone, "content is no longer stored")
	case errors.Is(err, content.ErrSourceChanged):
		// The bytes at that path are not the ones catalogued, so they
		// are not this book's until the next pass says otherwise.
		writeError(w, http.StatusConflict,
			"the file behind this book changed on disk")
	default:
		writeError(w, http.StatusInternalServerError, "download failed")
	}
}

// catalogErrorWriter keeps http.ServeContent inside this package's error
// contract. ServeContent parses the Range header itself, and answers a
// malformed one with a plain-text "invalid range" body — client-controlled
// input producing a non-JSON error, which every other route here refuses
// to do. It also leaves the Content-Disposition we set in place, so a
// failed request still looks like an attachment.
//
// Anything below 400 passes through untouched: the 200, 206 and 304 paths
// are exactly what ServeContent exists to get right.
type catalogErrorWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *catalogErrorWriter) WriteHeader(status int) {
	if status < 400 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.failed = true
	w.Header().Del("Content-Disposition")
	// Content-Range survives on a 416: it tells the client the real
	// length, which is how it recovers.
	writeError(w.ResponseWriter, status, statusErrorMessage(status))
}

func (w *catalogErrorWriter) Write(p []byte) (int, error) {
	if w.failed {
		// Swallow ServeContent's own plain-text body; ours is already
		// written.
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func statusErrorMessage(status int) string {
	switch status {
	case http.StatusRequestedRangeNotSatisfiable:
		return "requested range is not satisfiable"
	default:
		return "download failed"
	}
}

func writeCatalogError(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, notFound)
		return
	}
	writeError(w, http.StatusInternalServerError, "catalog lookup failed")
}

// catalogBookJSON renders the one shape every book-bearing route
// returns (ADR-0015). There is a single representation of a catalog
// book: list, search, entity listings, duplicates and detail all render
// this, so a client parses one shape rather than two, and the one it got
// wrong would have been the listing it draws a thousand times.
//
// rel is the book's slice of a batched relations read. A book with no
// contributors, no series or no available files still carries the
// fields, empty: an absent field and an empty one are different bugs to
// a client, and only one of them is true here.
func catalogBookJSON(b store.CatalogBook, rel store.CatalogBookRelations) map[string]any {
	out := map[string]any{
		"book_id":   b.ID,
		"folder_id": b.FolderID,
		"title":     b.Title,
		"status":    string(b.Status),
		// The content digest — what the bytes are — so a client can match
		// its own copy of a book against the server's.
		"sha256": b.ContentSHA256,
		// The length those bytes had when the server last read them. It
		// is a fact about the last look, not a promise about now; the
		// download is still the truth.
		"size_bytes": b.SizeBytes,
		"media_type": b.MediaType,
		"filename":   b.OriginalFilename,
		"created_at": b.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": b.UpdatedAt.UTC().Format(time.RFC3339Nano),
		// Offered for every book rather than only for books known to have
		// one. Knowing in advance would mean opening every publication on
		// the way past; a client that asks and gets 404 has learned the
		// same thing at the same cost.
		"cover_url": "/v1/books/" + url.PathEscape(b.ID) + "/cover",
	}
	// Omit empty optional metadata rather than sending empty strings: a
	// client showing "Publisher: " for every book is worse than one that
	// knows the field is absent.
	for key, value := range map[string]string{
		"subtitle":       b.Subtitle,
		"description":    b.Description,
		"publisher":      b.Publisher,
		"published_date": b.PublishedDate,
	} {
		if value != "" {
			out[key] = value
		}
	}

	// Every contributor in every role, not just the authors: which of
	// them a shelf shows is the client's decision, and the two clients we
	// have disagree about it. Roles are normalized on the way in, so a
	// client selects authors by role rather than by guessing.
	contributors := make([]map[string]any, 0, len(rel.Contributors[b.ID]))
	for _, c := range rel.Contributors[b.ID] {
		contributors = append(contributors, map[string]any{
			"id": c.ContributorID, "name": c.Name, "role": c.Role,
		})
	}
	out["contributors"] = contributors

	// Every series row, because the catalog has always allowed several
	// and a payload reporting one of them silently picks a winner.
	series := make([]map[string]any, 0, len(rel.Series[b.ID]))
	for _, s := range rel.Series[b.ID] {
		row := map[string]any{"id": s.SeriesID, "name": s.Name}
		// A position nobody recorded is left out rather than sent as
		// zero, which would read as "first in the series".
		if s.Position != nil {
			row["position"] = *s.Position
		}
		series = append(series, row)
	}
	out["series"] = series
	return out
}

// catalogBooksJSON renders a page of books, reading their relations in
// one batch rather than one lookup per row: a page of books is a bounded
// number of rows, and must be a bounded number of queries too.
func (s *Server) catalogBooksJSON(
	ctx context.Context, books []store.CatalogBook,
) ([]map[string]any, error) {
	ids := make([]string, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	rel, err := s.St.CatalogBookRelationsForBooks(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		out = append(out, catalogBookJSON(b, rel))
	}
	return out, nil
}

// catalogBookBodyJSON is the same shape for a single book. Detail is not
// a richer shape than a listing row; it is the same one.
func (s *Server) catalogBookBodyJSON(
	ctx context.Context, book store.CatalogBook,
) (map[string]any, error) {
	out, err := s.catalogBooksJSON(ctx, []store.CatalogBook{book})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func catalogPageSize(raw string) (int, error) {
	if raw == "" {
		return defaultCatalogPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if n > maxCatalogPageSize {
		return 0, fmt.Errorf("limit must be at most %d", maxCatalogPageSize)
	}
	return n, nil
}

// catalogCursor is the wire form of a page cursor. It is opaque on purpose:
// clients must round-trip it rather than construct it, so the sort key can
// change without breaking them.
type catalogCursor struct {
	CreatedAt string   `json:"t"`
	ID        string   `json:"i"`
	Position  *float64 `json:"p,omitempty"`
}

func encodeCatalogCursor(c store.CatalogBookCursor) string {
	raw, _ := json.Marshal(catalogCursor{
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        c.ID,
		Position:  c.SeriesPosition,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCatalogCursor(raw string) (*store.CatalogBookCursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("malformed cursor")
	}
	var c catalogCursor
	if err := json.Unmarshal(decoded, &c); err != nil {
		return nil, errors.New("malformed cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
	if err != nil || c.ID == "" {
		return nil, errors.New("malformed cursor")
	}
	return &store.CatalogBookCursor{
		CreatedAt: at, ID: c.ID, SeriesPosition: c.Position,
	}, nil
}

// downloadMediaType refuses to echo a stored media type that could make a
// browser treat the download as something executable.
func downloadMediaType(stored string) string {
	parsed, _, err := mime.ParseMediaType(stored)
	if err != nil || parsed == "" {
		return "application/epub+zip"
	}
	switch parsed {
	case "application/epub+zip", "application/octet-stream":
		return parsed
	default:
		return "application/octet-stream"
	}
}

// downloadFilename derives a name for the saved file. The stored original
// filename is somebody else's, taken off their disk, so
// it is never used as a path, only as a label, and only after being
// stripped of anything that could act as one.
func downloadFilename(b store.CatalogBook) string {
	name := sanitizeFilename(b.OriginalFilename)
	if name == "" {
		name = "book.epub"
	}
	return name
}

func sanitizeFilename(raw string) string {
	// Take the last path element under either separator, so neither
	// "../../etc/passwd" nor a Windows path survives as a directory.
	raw = strings.ReplaceAll(raw, "\\", "/")
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		raw = raw[idx+1:]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r < 0x20 || r == 0x7f:
			// Control characters, which could forge header lines.
		case r == '"' || r == ';' || r == '%':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), " .")
	if name == "" || name == ".." {
		return ""
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// contentDisposition emits both a plain and an RFC 5987 filename, because
// the plain one cannot carry non-ASCII and older clients ignore the
// encoded one.
func contentDisposition(name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r > 0x7f {
			ascii = append(ascii, '_')
			continue
		}
		ascii = append(ascii, r)
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		string(ascii), url.PathEscape(name))
}
