package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Series renames (ADR-0020). A rename is a display layer over the name a
// scan observed, in the same two scopes a claim uses.

type seriesNameBody struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

// seriesEntity is the entity a rename route addresses, answering the
// request itself when the kind is not one that can be renamed. Only a
// series has layers to rename in: a tag or a contributor is whatever the
// scan said it was.
func seriesEntity(w http.ResponseWriter, r *http.Request) (string, bool) {
	kind, ok := entityRequest(w, r)
	if !ok {
		return "", false
	}
	if kind != store.EntitySeries {
		writeError(w, http.StatusNotFound, "only a series can be renamed")
		return "", false
	}
	return r.PathValue("entity"), true
}

// writeSeriesNameError maps the store's refusals onto precise 4xx. A
// conflict is its own answer: the name is taken, and merging two shelves
// is not something this route does.
func writeSeriesNameError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "series not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "series rename failed")
	}
}

// HandleSetSeriesName implements PUT /v1/entities/series/{entity}/name.
func (s *Server) HandleSetSeriesName(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	entityID, ok := seriesEntity(w, r)
	if !ok {
		return
	}
	var body seriesNameBody
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes),
	).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	scope, ok := claimScope(w, r, body.Scope)
	if !ok {
		return
	}
	if len(body.Name) > store.MaxSeriesNameBytes {
		writeError(w, http.StatusBadRequest, "series name is too long")
		return
	}
	if err := s.St.SetSeriesName(
		r.Context(), tok.UserID, entityID, scope, body.Name, time.Now(),
	); err != nil {
		writeSeriesNameError(w, err)
		return
	}
	s.writeSeriesName(w, r, tok.UserID, entityID)
}

// HandleClearSeriesName implements
// DELETE /v1/entities/series/{entity}/name. It drops one layer's rename
// and answers with the name the series fell back to.
func (s *Server) HandleClearSeriesName(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	entityID, ok := seriesEntity(w, r)
	if !ok {
		return
	}
	scope, ok := claimScope(w, r, r.URL.Query().Get("scope"))
	if !ok {
		return
	}
	if err := s.St.ClearSeriesName(
		r.Context(), tok.UserID, entityID, scope,
	); err != nil {
		writeSeriesNameError(w, err)
		return
	}
	s.writeSeriesName(w, r, tok.UserID, entityID)
}

func (s *Server) writeSeriesName(
	w http.ResponseWriter, r *http.Request, userID, entityID string,
) {
	entity, err := s.St.CatalogEntityByID(
		r.Context(), userID, entityID, store.EntitySeries)
	if err != nil {
		writeSeriesNameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entityJSON(entity))
}

// entityJSON is one entity as every entity route renders it. The scanned
// name and the layer the display name came from are what a client needs
// to show a rename as a rename and to offer a revert.
func entityJSON(entity store.CatalogEntity) map[string]any {
	return map[string]any{
		"id":           entity.ID,
		"name":         entity.Name,
		"scanned_name": entity.ScannedName,
		"name_source":  string(entity.NameSource),
		"book_count":   entity.BookCount,
	}
}
