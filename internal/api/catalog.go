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

// BlobStore is the read side of the CAS: what a download needs and nothing
// more.
type BlobStore interface {
	OpenBlob(ctx context.Context, sha256 string) (*os.File, int64, error)
}

// HandleLibraries implements GET /v1/libraries — every library the caller
// can read, with the role they hold in each. This is where a client starts,
// because every other catalog route needs a library id.
func (s *Server) HandleLibraries(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libs, err := s.St.ListLibraries(r.Context(), tok.UserID, store.LibraryRoleRead)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "library lookup failed")
		return
	}
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, map[string]any{
			"library_id": l.Library.ID,
			"name":       l.Library.Name,
			"source":     string(l.Library.Source),
			"storage":    string(l.Library.Storage),
			"refresh":    string(l.Library.Refresh),
			"role":       string(l.Role),
			"created_at": l.Library.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

// HandleLibraryBooks implements GET /v1/libraries/{library}/books.
func (s *Server) HandleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
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

	books, err := list(r.Context(), tok.UserID, libraryID, after, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		out = append(out, catalogBookJSON(b))
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

// HandleBook implements GET /v1/books/{id}, the detail view. It
// includes the book's files so a client can choose what to download
// without a second round trip.
func (s *Server) HandleBook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), tok.UserID, bookID, store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	files, err := s.St.ListBookFiles(r.Context(), tok.UserID, bookID, store.LibraryRoleRead)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "book lookup failed")
		return
	}
	body := catalogBookJSON(book)
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		if f.Availability != store.BookFileAvailable {
			continue
		}
		out = append(out, map[string]any{
			"file_id":    f.ID,
			"media_type": f.MediaType,
			"sha256":     f.BlobSHA256,
			"filename":   f.OriginalFilename,
		})
	}
	body["files"] = out
	writeJSON(w, http.StatusOK, body)
}

// HandleBookDownload implements GET and HEAD
// /v1/books/{id}/download. It hands the open file to
// http.ServeContent, which is what gives us range requests, conditional
// requests and HEAD without writing any of that by hand.
func (s *Server) HandleBookDownload(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.ServeBookDownload(w, r, tok.UserID, r.PathValue("id"))
}

// ServeBookDownload is the download itself, without the token. The web
// UI authenticates with a cookie session but must serve bytes under the
// same rules — the media-type allowlist and the filename sanitizing
// below are what keep a hostile upload from becoming a hostile
// download, and there must not be a second copy of them.
func (s *Server) ServeBookDownload(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) {
	if s.Blobs == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	// The book is looked up before its files because a trashed book keeps
	// its files — that is what makes restore a relink — while the catalog
	// is what decides whether anything may be served. Asking the files
	// alone would happily hand back a deleted book's bytes.
	if _, err := s.St.CatalogBookByID(
		r.Context(), userID, bookID, store.LibraryRoleRead,
	); err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	files, err := s.St.ListBookFiles(r.Context(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	var file *store.BookFile
	for i := range files {
		if files[i].Availability == store.BookFileAvailable {
			file = &files[i]
			break
		}
	}
	if file == nil {
		// The book exists but its bytes do not — a file marked missing by
		// reconciliation, or superseded by a newer one that has gone. That
		// is not a 404 on the book; it is the content being gone.
		writeError(w, http.StatusGone, "no downloadable file for this book")
		return
	}

	// ServeContent finds the length by seeking, so the size the CAS
	// reports is redundant here.
	blob, _, err := s.Blobs.OpenBlob(r.Context(), file.BlobSHA256)
	if err != nil {
		if errors.Is(err, content.ErrStageMissing) {
			writeError(w, http.StatusGone, "content is no longer stored")
			return
		}
		writeError(w, http.StatusInternalServerError, "download failed")
		return
	}
	defer blob.Close()

	w.Header().Set("Content-Type", downloadMediaType(file.MediaType))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(downloadFilename(*file)))
	// The digest names the content, so it is a strong validator and the
	// content behind it can never change.
	w.Header().Set("ETag", `"`+file.BlobSHA256+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(&catalogErrorWriter{ResponseWriter: w}, r, "",
		file.UpdatedAt.UTC(), blob)
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

func catalogBookJSON(b store.CatalogBook) map[string]any {
	out := map[string]any{
		"book_id":    b.ID,
		"library_id": b.LibraryID,
		"title":      b.Title,
		"status":     string(b.Status),
		"created_at": b.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": b.UpdatedAt.UTC().Format(time.RFC3339Nano),
		// Offered for every book rather than only for books known to have
		// one. Knowing in advance would mean recording it at ingest for
		// every book ever uploaded; a client that asks and gets 404 has
		// learned the same thing at the same cost.
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
	return out
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
	CreatedAt string `json:"t"`
	ID        string `json:"i"`
}

func encodeCatalogCursor(c store.CatalogBookCursor) string {
	raw, _ := json.Marshal(catalogCursor{
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        c.ID,
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
	return &store.CatalogBookCursor{CreatedAt: at, ID: c.ID}, nil
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
// filename is attacker-influenced — it comes from a multipart header — so
// it is never used as a path, only as a label, and only after being
// stripped of anything that could act as one.
func downloadFilename(f store.BookFile) string {
	name := sanitizeFilename(f.OriginalFilename)
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

// HandleTrashBook implements DELETE /v1/books/{id}. Deletion is a two
// step operation: this moves the book out of the catalog and starts its
// retention window, and the bytes go only when that window closes. A
// caller who deleted the wrong book has until then to say so.
func (s *Server) HandleTrashBook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	now := time.Now().UTC()
	retention := time.Duration(s.Cfg.Content.TrashRetentionHours) * time.Hour
	book, err := s.St.TrashCatalogBook(
		r.Context(), tok.UserID, r.PathValue("id"), now, now.Add(retention))
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			writeError(w, http.StatusConflict, "book is already deleted")
			return
		}
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, catalogBookJSON(book))
}

// HandleRestoreBook implements POST /v1/books/{id}/restore, the undo for
// HandleTrashBook. It works only inside the retention window: past it the
// book is waiting to be purged and its bytes may already be gone.
func (s *Server) HandleRestoreBook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	book, err := s.St.RestoreCatalogBook(
		r.Context(), tok.UserID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			writeError(w, http.StatusConflict,
				"book is not in the trash, or its retention window has closed")
			return
		}
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, catalogBookJSON(book))
}

// HandleLibraryDuplicates reports books in one library that hold
// identical bytes.
//
// It is a read, not a repair: a second catalog entry for deduplicated
// content is something a user may have meant, so the server says what it
// knows and leaves the decision alone. Resolving a group is an ordinary
// delete of whichever entry the client chooses.
func (s *Server) HandleLibraryDuplicates(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	books, err := s.St.ListDuplicateContentBooks(
		r.Context(), tok.UserID, libraryID, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "duplicate listing failed")
		return
	}
	// Grouped by digest rather than returned flat, because a client that
	// had to group them itself would have to know the ordering rule to do
	// it, and one that guessed would show a book duplicating itself.
	groups := make([]map[string]any, 0)
	digest := ""
	for i, duplicate := range books {
		if i == 0 || duplicate.SHA256 != digest {
			digest = duplicate.SHA256
			groups = append(groups, map[string]any{
				"sha256": digest,
				"books":  make([]map[string]any, 0, 2),
			})
		}
		last := groups[len(groups)-1]
		last["books"] = append(
			last["books"].([]map[string]any), catalogBookJSON(duplicate.Book))
	}
	// A group cut in half by the limit is dropped: one book on its own is
	// not a duplicate of anything, and saying so would be wrong rather
	// than merely incomplete.
	if n := len(groups); n > 0 {
		if last, _ := groups[n-1]["books"].([]map[string]any); len(last) < 2 {
			groups = groups[:n-1]
		}
	}
	// The weaker report rides along rather than living at its own route:
	// it answers the same question a librarian came here with, and two
	// routes would mean a page that shows one and not the other.
	similar, err := s.St.ListSimilarBooks(r.Context(), tok.UserID, libraryID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "duplicate listing failed")
		return
	}
	similarOut := make([]map[string]any, 0, len(similar))
	for _, group := range similar {
		books := make([]map[string]any, 0, len(group.Books))
		for _, b := range group.Books {
			books = append(books, catalogBookJSON(b))
		}
		similarOut = append(similarOut, map[string]any{
			"normalized_title": group.Title, "books": books,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicates": groups, "similar": similarOut,
	})
}
