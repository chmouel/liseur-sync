package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

type catalogResolveRequest struct {
	// Confirmed is the reader's word that a title/author match is the
	// same book, exactly as on /v1/works/resolve.
	Confirmed bool `json:"confirmed,omitempty"`
}

type catalogResolveResponse struct {
	BookID      string           `json:"book_id"`
	WorkID      string           `json:"work_id"`
	Confidence  string           `json:"confidence"`
	Created     bool             `json:"created"`
	Identifiers []identifierJSON `json:"identifiers"`
}

// HandleResolveBookWork implements POST /v1/books/{id}/resolve: it joins a
// catalog book to the caller's own sync work so that positions and sessions
// can be reported for a book that was downloaded rather than side-loaded.
//
// The client sends no identifiers. It cannot: the catalog knows the file's
// digests and embedded ids, and a client that has only browsed the catalog
// has not seen the bytes. So the server collects them, which also means two
// devices resolve the same book from the same evidence instead of from
// whatever each happened to compute.
//
// The mapping is per user. Two readers of one shared book get two work IDs,
// which is what keeps reading history private (ADR-0003).
func (s *Server) HandleResolveBookWork(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req catalogResolveRequest
	// An empty body means "not confirmed", which is the safe default and
	// saves every client from sending {} to resolve a book.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).
			Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), bookID)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	bookIDs, author, err := workident.Evidence(r.Context(), s.St, bookID)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}

	workID, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	proposed, editions, ids := workident.Plan(tok.UserID, workID, book, bookIDs, author)
	proposed.CreatedAt = time.Now()

	result, err := s.St.ResolveCatalogBookWork(r.Context(), tok.UserID, bookID,
		proposed, editions, ids, req.Confirmed, time.Now())
	if err != nil {
		// The read above already found the book, so reaching this means
		// it was replaced or its folder removed in between. A race is
		// still a 404, not a 500.
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "work resolution conflict")
			return
		}
		writeError(w, http.StatusInternalServerError, "resolve failed")
		return
	}
	if len(result.ConflictingWorkIDs) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "identifiers resolve to multiple works",
			"works": result.ConflictingWorkIDs,
		})
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, catalogResolveResponse{
		BookID: bookID, WorkID: result.WorkID,
		Confidence: result.Confidence, Created: result.Created,
		Identifiers: identifiersJSON(ids),
	})
}

func identifiersJSON(ids []store.Identifier) []identifierJSON {
	out := make([]identifierJSON, 0, len(ids))
	for _, id := range ids {
		out = append(out, identifierJSON{Kind: id.Kind, Value: id.Value})
	}
	return out
}
