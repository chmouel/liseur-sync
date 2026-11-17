package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Server) HandleWriteEvaluation(w http.ResponseWriter, r *http.Request) {
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
	canonical := "null"
	switch body.Field {
	case "rating":
		if string(body.Value) != "null" {
			var rating int
			if json.Unmarshal(body.Value, &rating) != nil || rating < 2 || rating > 10 {
				writeError(w, http.StatusBadRequest, "rating must be 2 through 10 half-stars, or null")
				return
			}
			canonical = strconv.Itoa(rating)
		}
	case "review":
		if string(body.Value) != "null" {
			var review string
			if !utf8.Valid(body.Value) || json.Unmarshal(body.Value, &review) != nil || strings.ContainsRune(review, 0) || len(review) > 8192 || strings.TrimSpace(review) == "" {
				writeError(w, http.StatusBadRequest, "review must be non-empty UTF-8, at most 8192 bytes, or null")
				return
			}
			encoded, _ := json.Marshal(review)
			canonical = string(encoded)
		}
	default:
		writeError(w, http.StatusBadRequest, "field must be rating or review")
		return
	}
	err := s.St.WriteEvaluation(r.Context(), tok.UserID, r.PathValue("id"), store.EvaluationWrite{ID: body.ID, WriterID: body.WriterID, Revision: body.Revision, Field: body.Field, Value: canonical})
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

func (s *Server) HandleReadEvaluation(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	fields, err := s.St.ReadEvaluation(r.Context(), tok.UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	out := []map[string]any{}
	for _, field := range fields {
		out = append(out, map[string]any{"field": field.Field, "value": json.RawMessage(field.Value), "write_id": field.ID, "updated_at": field.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": out})
}
