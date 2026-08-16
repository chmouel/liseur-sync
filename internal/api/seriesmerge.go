package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Merging and splitting a series (ADR-0021). Both speak for the whole
// library — they change what a shelf *is*, not what one reader calls it
// — so both take admin on top of library-manage, and neither has a
// personal form to fall back to.

type seriesMergeBody struct {
	Into string `json:"into"`
}

type seriesSplitBody struct {
	FolderID string `json:"folder_id"`
	Name     string `json:"name"`
}

// requireSeriesAdmin answers the request itself unless the caller is an
// admin addressing a series. A tag has no shelf to merge and a
// contributor has no folder to split off.
func requireSeriesAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return "", "", false
	}
	entityID, ok := seriesEntity(w, r)
	if !ok {
		return "", "", false
	}
	if !tok.Scopes.Contains(store.ScopeAdmin) {
		writeError(w, http.StatusForbidden,
			"merging and splitting a series is an admin capability")
		return "", "", false
	}
	return tok.UserID, entityID, true
}

// HandleMergeSeries implements POST /v1/entities/series/{entity}/merge.
// The addressed series is the one absorbed; the body names the survivor,
// which is what the answer describes.
func (s *Server) HandleMergeSeries(w http.ResponseWriter, r *http.Request) {
	userID, entityID, ok := requireSeriesAdmin(w, r)
	if !ok {
		return
	}
	var body seriesMergeBody
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes),
	).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if body.Into == "" {
		writeError(w, http.StatusBadRequest, "into must name the surviving series")
		return
	}
	survivor, err := s.St.MergeSeries(r.Context(), userID, entityID, body.Into, time.Now())
	if err != nil {
		writeSeriesNameError(w, err)
		return
	}
	s.writeSeriesName(w, r, userID, survivor)
}

// HandleSplitSeries implements POST /v1/entities/series/{entity}/split.
// It answers with the new series, which is the one the caller now wants
// to look at.
func (s *Server) HandleSplitSeries(w http.ResponseWriter, r *http.Request) {
	userID, entityID, ok := requireSeriesAdmin(w, r)
	if !ok {
		return
	}
	var body seriesSplitBody
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes),
	).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if body.FolderID == "" {
		writeError(w, http.StatusBadRequest,
			"folder_id must name the folder whose books are leaving")
		return
	}
	if len(body.Name) > store.MaxSeriesNameBytes {
		writeError(w, http.StatusBadRequest, "series name is too long")
		return
	}
	newID, err := s.St.SplitSeriesFolder(
		r.Context(), userID, entityID, body.FolderID, body.Name, time.Now())
	if err != nil {
		writeSeriesNameError(w, err)
		return
	}
	s.writeSeriesName(w, r, userID, newID)
}

// HandleSeriesBindings implements
// GET /v1/entities/series/{entity}/bindings: the names that fold into
// this shelf, which is what a client shows to offer an undo.
func (s *Server) HandleSeriesBindings(w http.ResponseWriter, r *http.Request) {
	userID, entityID, ok := requireSeriesAdmin(w, r)
	if !ok {
		return
	}
	if _, err := s.St.CatalogEntityByID(
		r.Context(), userID, entityID, store.EntitySeries,
	); err != nil {
		writeSeriesNameError(w, err)
		return
	}
	bindings, err := s.St.SeriesBindings(r.Context(), entityID)
	if err != nil {
		writeSeriesNameError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, map[string]any{
			"binding_id": b.ID,
			"name":       b.Name,
			// Absent rather than empty when the binding speaks for the
			// whole library: a client should not have to know that ""
			// means "everywhere".
			"folder_id":   emptyToNil(b.FolderID),
			"folder_name": emptyToNil(b.FolderName),
			"created_at":  b.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": out})
}

// HandleDeleteSeriesBinding implements
// DELETE /v1/entities/series/{entity}/bindings/{binding}. Removing a
// binding does not move a book: the next pass over the folder that
// observes the freed name is what puts the shelf back.
func (s *Server) HandleDeleteSeriesBinding(w http.ResponseWriter, r *http.Request) {
	_, _, ok := requireSeriesAdmin(w, r)
	if !ok {
		return
	}
	if err := s.St.DeleteSeriesBinding(
		r.Context(), r.PathValue("binding"),
	); err != nil {
		writeSeriesNameError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
