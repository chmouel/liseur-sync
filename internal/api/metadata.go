package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/cover"
	"github.com/chmouel/liseur-sync/internal/store"
)

// metadataFieldLimit bounds one override value in bytes. A title or an author
// is a line of text, a description a paragraph or two; past these the value is
// a paste accident rather than a correction.
func metadataFieldLimit(field string) (int, bool) {
	switch field {
	case store.MetadataTitle, store.MetadataAuthor:
		return 512, true
	case store.MetadataDescription:
		return 16384, true
	}
	return 0, false
}

func (s *Server) HandleWriteWorkMetadata(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		ID       string          `json:"write_id"`
		WriterID string          `json:"writer_id"`
		Revision int64           `json:"revision"`
		Field    string          `json:"field"`
		Value    json.RawMessage `json:"value"`
	}
	if decodeBatch(w, r, 65536, &body) {
		return
	}
	if strings.TrimSpace(body.WriterID) == "" || len(body.WriterID) > 64 || strings.ContainsRune(body.WriterID, 0) || body.Revision <= 0 || body.Revision > 9007199254740991 {
		writeError(w, http.StatusBadRequest, "writer_id and positive safe-integer revision required")
		return
	}
	if len(body.ID) == 0 || len(body.ID) > 64 || strings.ContainsRune(body.ID, 0) || len(body.Value) == 0 {
		writeError(w, http.StatusBadRequest, "write_id and value required")
		return
	}
	limit, ok := metadataFieldLimit(body.Field)
	if !ok {
		writeError(w, http.StatusBadRequest, "field must be title, author or description")
		return
	}
	canonical := "null"
	if string(body.Value) != "null" {
		var text string
		if !utf8.Valid(body.Value) || json.Unmarshal(body.Value, &text) != nil || strings.ContainsRune(text, 0) || len(text) > limit {
			writeError(w, http.StatusBadRequest, "value must be UTF-8 text within its field's byte limit, or null")
			return
		}
		encoded, _ := json.Marshal(text)
		canonical = string(encoded)
	}
	err := s.St.WriteWorkMetadata(r.Context(), tok.UserID, r.PathValue("id"), store.WorkMetadataWrite{
		ID: body.ID, WriterID: body.WriterID, Revision: body.Revision,
		Field: body.Field, Value: canonical,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "work not found")
	case errors.Is(err, store.ErrIDMismatch):
		writeError(w, http.StatusConflict, "write_id reused with different content")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "write failed")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	}
}

func (s *Server) HandleReadWorkMetadata(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	metadata, err := s.St.ReadWorkMetadata(r.Context(), tok.UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	fields := []map[string]any{}
	for _, field := range metadata.Fields {
		fields = append(fields, map[string]any{
			"field": field.Field, "value": json.RawMessage(field.Value),
			"write_id":   field.ID,
			"updated_at": field.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	out := map[string]any{"fields": fields}
	if metadata.Cover != nil {
		// A tombstone keeps its stamp: "no cover" is a choice, and the
		// difference between one and never having chosen is what a client
		// needs to stop asking.
		var digest any
		if metadata.Cover.SHA256 != "" {
			digest = metadata.Cover.SHA256
		}
		out["cover"] = map[string]any{
			"sha256":     digest,
			"updated_at": metadata.Cover.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandlePutWorkCover implements PUT /v1/works/{id}/cover. The image is
// rendered here rather than served from the bytes it arrived as, so a stored
// cover is one bounded JPEG whatever a client sent.
func (s *Server) HandlePutWorkCover(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	body := http.MaxBytesReader(w, r.Body, maxCoverSourceBytes)
	source, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "cover image too large")
			return
		}
		writeError(w, http.StatusBadRequest, "cover image could not be read")
		return
	}
	if len(source) == 0 {
		writeError(w, http.StatusBadRequest, "cover image required")
		return
	}
	rendered, err := cover.Render(source, cover.SizeFull, cover.DefaultLimits())
	if err != nil {
		writeError(w, http.StatusBadRequest, "cover image could not be decoded")
		return
	}
	sum := sha256.Sum256(rendered)
	digest := hex.EncodeToString(sum[:])
	updatedAt, err := s.St.PutWorkCover(r.Context(), tok.UserID, r.PathValue("id"), digest, rendered)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cover could not be stored")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "applied",
		"sha256":     digest,
		"updated_at": updatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) HandleGetWorkCover(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	// An absent parameter is the full image: a chosen cover is asked for
	// where a book is shown, not in a grid.
	size := cover.SizeFull
	if asked := r.URL.Query().Get("size"); asked != "" {
		parsed, ok := cover.ParseSize(asked)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown cover size")
			return
		}
		size = parsed
	}
	chosen, err := s.St.WorkCover(r.Context(), tok.UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no cover for this work")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	image := chosen.Data
	etag := `"` + chosen.SHA256 + `"`
	if size != cover.SizeFull {
		if image, err = cover.Render(chosen.Data, size, cover.DefaultLimits()); err != nil {
			writeError(w, http.StatusInternalServerError, "cover rendering failed")
			return
		}
		sum := sha256.Sum256(image)
		etag = `"` + hex.EncodeToString(sum[:]) + `"`
	}
	w.Header().Set("Content-Type", cover.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(image))
}

func (s *Server) HandleDeleteWorkCover(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	updatedAt, err := s.St.DeleteWorkCover(r.Context(), tok.UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "deleted",
		"updated_at": updatedAt.UTC().Format(time.RFC3339Nano),
	})
}
