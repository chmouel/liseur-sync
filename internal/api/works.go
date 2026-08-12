package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// identifierJSON is one (kind, value) alias on the wire.
type identifierJSON struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type resolveRequest struct {
	Identifiers []identifierJSON `json:"identifiers"`
	Title       string           `json:"title,omitempty"`
	Author      string           `json:"author,omitempty"`
	// Confirmed is the reader's word that a fuzzy match is the same
	// book: a ta hit resolves high and registers the stronger
	// identifiers, which an unconfirmed guess must never do.
	Confirmed bool `json:"confirmed,omitempty"`
}

type resolveResponse struct {
	WorkID     string `json:"work_id"`
	Confidence string `json:"confidence"` // "high" | "low"
	Created    bool   `json:"created"`
}

// aliasOrder resolves in decreasing strength. "source" is the catalog
// server's own id for the book (e.g. "komga:<id>"): two devices browsing
// the same catalog hold the same one, so it identifies the book without
// either of them having downloaded the file.
var aliasOrder = []string{"sha256", "partial-md5", "source", "dc", "ta"}

// HandleResolve implements POST /v1/works/resolve. One store transaction
// checks that all matching aliases agree, creates the work when none match,
// and promotes missing aliases only for a high-confidence or confirmed hit.
func (s *Server) HandleResolve(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var req resolveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Identifiers) == 0 {
		writeError(w, http.StatusBadRequest, "identifiers required")
		return
	}

	ids, invalid := normalizeIdentifiers(req.Identifiers)
	if invalid != "" {
		writeError(w, http.StatusBadRequest, invalid)
		return
	}
	ids = orderIdentifiers(ids)

	workID, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	proposed := store.Work{
		ID: workID, UserID: tok.UserID,
		Title: req.Title, Author: req.Author, CreatedAt: time.Now(),
	}
	var editions []store.Edition
	for _, id := range ids {
		if id.Kind == "sha256" {
			editions = append(editions, store.Edition{
				UserID: tok.UserID, SHA256: id.Value, WorkID: workID,
			})
		}
	}
	result, err := s.St.ResolveWork(r.Context(), tok.UserID, proposed, editions, ids, req.Confirmed)
	if err != nil {
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
	writeJSON(w, status, resolveResponse{
		WorkID: result.WorkID, Confidence: result.Confidence, Created: result.Created,
	})
}

func normalizeIdentifiers(inputs []identifierJSON) ([]store.Identifier, string) {
	ids := make([]store.Identifier, 0, len(inputs))
	for _, in := range inputs {
		kind := strings.TrimSpace(in.Kind)
		value := strings.TrimSpace(in.Value)
		if kind == "" || value == "" || len(value) > 512 {
			return nil, "invalid identifier"
		}
		switch kind {
		case "sha256", "partial-md5", "source", "dc", "ta":
		default:
			return nil, "unknown identifier kind " + kind
		}
		if kind == "sha256" || kind == "partial-md5" {
			value = strings.ToLower(value)
		}
		ids = append(ids, store.Identifier{Kind: kind, Value: value})
	}
	return ids, ""
}

func newID() (string, error) { return auth.NewSecret() }

func orderIdentifiers(ids []store.Identifier) []store.Identifier {
	ordered := make([]store.Identifier, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, kind := range aliasOrder {
		for _, id := range ids {
			key := id.Kind + ":" + id.Value
			if id.Kind == kind && !seen[key] {
				ordered = append(ordered, id)
				seen[key] = true
			}
		}
	}
	return ordered
}
