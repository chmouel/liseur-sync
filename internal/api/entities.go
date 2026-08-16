package api

import (
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// entityKinds maps the plural path segment a client uses to the kind the
// store knows. Reading the kind out of a fixed table is what keeps a
// caller's string away from the table names the store builds queries
// from.
var entityKinds = map[string]store.EntityKind{
	"series":       store.EntitySeries,
	"contributors": store.EntityContributor,
	"tags":         store.EntityTag,
}

// entityRequest pulls the kind every entity route needs, answering the
// request itself when it is wrong. Entities are library-wide
// (ADR-0019), so there is no folder to read.
func entityRequest(w http.ResponseWriter, r *http.Request) (store.EntityKind, bool) {
	kind, ok := entityKinds[r.PathValue("kind")]
	if !ok {
		writeError(w, http.StatusNotFound, "no such kind of entity")
		return "", false
	}
	return kind, true
}

// HandleEntities implements GET /v1/entities/{kind}.
func (s *Server) HandleEntities(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The cursor is the last normalized name of the previous page, which
	// the client already has, so it needs no encoding of its own.
	after := r.URL.Query().Get("after")
	if len(after) > 512 {
		writeError(w, http.StatusBadRequest, "cursor is too long")
		return
	}
	entities, err := s.St.ListCatalogEntities(
		r.Context(), readerID(r), kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	out := make([]map[string]any, 0, len(entities))
	for _, entity := range entities {
		out = append(out, map[string]any{
			"id": entity.ID, "name": entity.Name,
			"book_count": entity.BookCount,
		})
	}
	body := map[string]any{"entities": out}
	if len(entities) == limit {
		body["next_after"] = entities[len(entities)-1].NormalizedName
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleEntityBooks implements
// GET /v1/entities/{kind}/{entity}/books.
func (s *Server) HandleEntityBooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	kind, ok := entityRequest(w, r)
	if !ok {
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
	entity, err := s.St.CatalogEntityByID(
		r.Context(), readerID(r), r.PathValue("entity"), kind)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	books, next, err := s.St.ListBooksByEntity(
		r.Context(), readerID(r), entity.ID, kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	out, err := s.catalogBooksJSON(r.Context(), readerID(r), books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	body := map[string]any{
		"entity": map[string]any{
			"id": entity.ID, "name": entity.Name,
			"book_count": entity.BookCount,
		},
		"books": out,
	}
	if next != nil {
		body["next_cursor"] = encodeCatalogCursor(*next)
	}
	writeJSON(w, http.StatusOK, body)
}
