package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// maxSearchTextBytes bounds what a caller may type. The store keeps only
// the first handful of words anyway, so a longer string is never a more
// precise question — only a bigger one to carry.
const maxSearchTextBytes = 512

// maxSearchEntityFilters bounds how many facets one query may stack.
// Every filter is another membership lookup, and a query narrowed eight
// ways has already found its book.
const maxSearchEntityFilters = 8

// HandleLibrarySearch implements GET /v1/libraries/{library}/search.
//
// The answer is unpaged on purpose. A relevance order has no stable
// cursor, and search answers "where is that book" rather than "show me
// everything"; the result says whether it was cut so a client can ask
// the person to narrow it instead of implying it found all there was.
//
// There is no reading-state filter and there will not be one (ADR-0004).
// A catalog-only credential must not be able to observe reading state,
// and the surest way to keep that true is for this route to have no
// vocabulary for it.
func (s *Server) HandleLibrarySearch(w http.ResponseWriter, r *http.Request) {
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
	query := r.URL.Query()
	text := query.Get("q")
	if len(text) > maxSearchTextBytes {
		writeError(w, http.StatusBadRequest, "search text is too long")
		return
	}
	entities := query["entity"]
	if len(entities) > maxSearchEntityFilters {
		writeError(w, http.StatusBadRequest, "too many entity filters")
		return
	}
	for _, id := range entities {
		if id == "" || len(id) > maxLibraryIDBytes {
			writeError(w, http.StatusBadRequest, "malformed entity filter")
			return
		}
	}
	limit, err := searchLimit(query.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.St.SearchCatalogBooks(r.Context(), tok.UserID, store.SearchQuery{
		LibraryID: libraryID,
		Text:      text,
		Entities:  entities,
		Limit:     limit,
	})
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	books, err := s.catalogBooksJSON(r.Context(), tok.UserID, result.Books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	facets := make([]map[string]any, 0, len(result.Facets))
	for _, f := range result.Facets {
		facets = append(facets, map[string]any{
			"kind": string(f.Kind), "id": f.ID, "name": f.Name,
			"book_count": f.BookCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"books":     books,
		"facets":    facets,
		"truncated": result.Truncated,
	})
}

// searchLimit reads the caller's limit, refusing a bad one rather than
// quietly substituting a number they did not ask for.
func searchLimit(raw string) (int, error) {
	if raw == "" {
		return store.MaxSearchLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if limit > store.MaxSearchLimit {
		return 0, fmt.Errorf("limit must be at most %d", store.MaxSearchLimit)
	}
	return limit, nil
}
