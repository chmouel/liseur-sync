package api

import (
	"encoding/json"
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

// HandleResolve implements POST /v1/works/resolve. One transaction:
// resolution in alias-priority order, first hit wins; all supplied
// identifiers registered as aliases of the resolved work; conflicting
// identifiers resolving to multiple distinct works -> 409 with no
// mutation.
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

	// Normalize + validate.
	ids := make([]store.Identifier, 0, len(req.Identifiers))
	for _, in := range req.Identifiers {
		kind := strings.TrimSpace(in.Kind)
		value := strings.TrimSpace(in.Value)
		if kind == "" || value == "" || len(value) > 512 {
			writeError(w, http.StatusBadRequest, "invalid identifier")
			return
		}
		switch kind {
		case "sha256", "partial-md5", "source", "dc", "ta":
		default:
			writeError(w, http.StatusBadRequest, "unknown identifier kind "+kind)
			return
		}
		if kind == "sha256" || kind == "partial-md5" {
			value = strings.ToLower(value)
		}
		ids = append(ids, store.Identifier{Kind: kind, Value: value})
	}

	hits, err := s.St.ResolveAliases(r.Context(), tok.UserID, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve failed")
		return
	}

	// Order hits by alias priority, collect distinct works.
	type hit struct {
		kind, value, workID string
	}
	var ordered []hit
	distinct := map[string]bool{}
	for _, kind := range aliasOrder {
		for _, id := range ids {
			if id.Kind != kind {
				continue
			}
			if wid, ok := hits[kind+":"+id.Value]; ok {
				ordered = append(ordered, hit{kind, id.Value, wid})
				distinct[wid] = true
			}
		}
	}

	if len(distinct) > 1 {
		// Alias conflict: supplied identifiers resolve to more than one
		// distinct work. No mutation; client confirms a merge.
		works := make([]string, 0, len(distinct))
		for wid := range distinct {
			works = append(works, wid)
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "identifiers resolve to multiple works",
			"works": works,
		})
		return
	}

	ctx := r.Context()
	if len(distinct) == 1 {
		// All hits agree on one work: register remaining identifiers as
		// aliases. Confidence comes from the highest-priority hit, so a
		// sha256 match stays "high" even when a ta alias also hit.
		workID := ordered[0].workID
		conf := "high"
		if ordered[0].kind == "ta" {
			conf = "low"
		}
		var toAdd []store.Identifier
		for _, id := range ids {
			if _, ok := hits[id.Kind+":"+id.Value]; !ok {
				toAdd = append(toAdd, id)
			}
		}
		if len(toAdd) > 0 {
			if err := s.St.AddAliases(ctx, tok.UserID, workID, toAdd); err != nil {
				writeError(w, http.StatusInternalServerError, "alias registration failed")
				return
			}
		}
		// A pending work hit that resolves with real identifiers flips
		// to established.
		_ = s.St.ClearPending(ctx, tok.UserID, workID)
		writeJSON(w, http.StatusOK, resolveResponse{WorkID: workID, Confidence: conf, Created: false})
		return
	}

	// No hit: create work + edition (if sha256 present) + aliases.
	workID, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	wk := store.Work{
		ID: workID, UserID: tok.UserID,
		Title: req.Title, Author: req.Author, CreatedAt: time.Now(),
	}
	var ed *store.Edition
	for _, id := range ids {
		if id.Kind == "sha256" {
			ed = &store.Edition{UserID: tok.UserID, SHA256: id.Value, WorkID: workID}
			break
		}
	}
	if err := s.St.CreateWork(ctx, wk, ed, ids); err != nil {
		writeError(w, http.StatusInternalServerError, "work creation failed")
		return
	}
	writeJSON(w, http.StatusCreated, resolveResponse{WorkID: workID, Confidence: "high", Created: true})
}

func newID() (string, error) { return auth.NewSecret() }
