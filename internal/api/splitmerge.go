package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandleSplit implements POST /v1/works/{id}/split with an explicit
// alias list.
func (s *Server) HandleSplit(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	var req struct {
		EditionSHA string           `json:"edition_sha"`
		Aliases    []identifierJSON `json:"aliases"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.EditionSHA == "" {
		writeError(w, http.StatusBadRequest, "edition_sha required")
		return
	}
	if _, err := s.St.WorkByID(r.Context(), tok.UserID, workID); err != nil {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	var aliases []store.Identifier
	for _, a := range req.Aliases {
		aliases = append(aliases, store.Identifier{Kind: a.Kind, Value: a.Value})
	}
	newIDStr, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	nw := store.Work{ID: newIDStr, UserID: tok.UserID, CreatedAt: time.Now()}
	if err := s.St.SplitWork(r.Context(), tok.UserID, workID, req.EditionSHA, aliases, nw); err != nil {
		switch err {
		case store.ErrNotFound:
			writeError(w, http.StatusNotFound, "edition not found")
		case store.ErrConflict:
			writeError(w, http.StatusConflict, "edition does not belong to work")
		default:
			writeError(w, http.StatusInternalServerError, "split failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"work_id": newIDStr})
}

// HandleMerge implements POST /v1/works/merge — the explicit
// confirmation endpoint for alias conflicts.
func (s *Server) HandleMerge(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var req struct {
		From string `json:"from_work_id"`
		Into string `json:"into_work_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.From == "" || req.Into == "" || req.From == req.Into {
		writeError(w, http.StatusBadRequest, "from_work_id and into_work_id required and must differ")
		return
	}
	if err := s.St.MergeWorks(r.Context(), tok.UserID, req.From, req.Into); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "work not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "merge failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"work_id": req.Into})
}
