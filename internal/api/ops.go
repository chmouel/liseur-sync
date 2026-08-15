package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandlePushOps implements POST /v1/ops — batch, idempotent on op_id.
// The token's server-side device_id stamps every op; a client-supplied
// device_id is ignored.
func (s *Server) HandlePushOps(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Ops []opJSON `json:"ops"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Ops) == 0 {
		writeError(w, http.StatusBadRequest, "ops required")
		return
	}
	if len(body.Ops) > s.Cfg.Ops.MaxBatch {
		writeError(w, http.StatusBadRequest, "batch too large")
		return
	}

	// Validate all items up front; any invalid item fails the batch with
	// per-item reasons (native clients are expected to be well-formed;
	// failing loudly beats partial state).
	ops := make([]store.Op, len(body.Ops))
	for i, in := range body.Ops {
		if in.OpID == "" || len(in.OpID) > 64 {
			writeError(w, http.StatusBadRequest, "op "+strconv.Itoa(i)+": op_id required")
			return
		}
		if in.WorkID == "" {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": work_id required")
			return
		}
		if in.Progression < 0 || in.Progression > 1 {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": progression out of range [0,1]")
			return
		}
		if len(in.Locator) > s.Cfg.Ops.MaxLocatorBytes {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": locator too large")
			return
		}
		ts, err := time.Parse(time.RFC3339Nano, in.ClientTS)
		if err != nil {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": bad client_ts")
			return
		}
		if ts.After(time.Now().Add(24 * time.Hour)) {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": client_ts in the future")
			return
		}
		// Verify the work belongs to the user.
		if _, err := s.St.WorkByID(r.Context(), tok.UserID, in.WorkID); err != nil {
			writeError(w, http.StatusBadRequest, "op "+in.OpID+": unknown work")
			return
		}
		ops[i] = store.Op{
			OpID:        in.OpID,
			WorkID:      in.WorkID,
			EditionSHA:  in.EditionSHA,
			ClientTS:    ts,
			Progression: in.Progression,
			LocatorJSON: in.Locator,
			ForeignPos:  in.ForeignPos,
			Origin:      store.OriginNative,
		}
	}

	results, err := s.St.AppendOps(r.Context(), tok.UserID, tok.DeviceID, ops)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "append failed")
		return
	}
	type item struct {
		OpID   string `json:"op_id"`
		Status string `json:"status"`
		Seq    int64  `json:"seq,omitempty"`
		Reason string `json:"reason,omitempty"`
	}
	out := struct {
		Results []item `json:"results"`
	}{Results: make([]item, len(results))}
	for i, r := range results {
		out.Results[i] = item{OpID: r.OpID, Status: r.Status, Seq: r.Seq, Reason: r.Reason}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleChanges implements GET /v1/changes?since=<seq>&limit=<n>.
func (s *Server) HandleChanges(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	page, err := s.St.Changes(r.Context(), tok.UserID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "changes failed")
		return
	}
	if page.ResyncNeeded {
		writeJSON(w, http.StatusGone, map[string]any{
			"error":          "resync_required",
			"high_water":     page.HighWater,
			"heads_endpoint": "/v1/heads",
			"message":        "since is below the compaction horizon; fetch /v1/heads and resume from high_water",
		})
		return
	}
	out := struct {
		Ops       []opJSON `json:"ops"`
		HighWater int64    `json:"high_water"`
		HasMore   bool     `json:"has_more"`
	}{HighWater: page.HighWater, HasMore: page.HasMore}
	for _, o := range page.Ops {
		out.Ops = append(out.Ops, opToJSON(o))
	}
	if out.Ops == nil {
		out.Ops = []opJSON{}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandlePositions implements GET /v1/works/{id}/positions?limit=50.
func (s *Server) HandlePositions(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	if _, err := s.St.WorkByID(r.Context(), tok.UserID, workID); err != nil {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ops, err := s.St.Positions(r.Context(), tok.UserID, workID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "positions failed")
		return
	}
	out := struct {
		Ops []opJSON `json:"ops"`
	}{}
	for _, o := range ops {
		out.Ops = append(out.Ops, opToJSON(o))
	}
	if out.Ops == nil {
		out.Ops = []opJSON{}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleHeads implements GET /v1/heads — newest op per (work, device)
// plus an atomic snapshot seq. The resync recovery protocol.
func (s *Server) HandleHeads(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	h, err := s.St.HeadsFor(r.Context(), tok.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "heads failed")
		return
	}
	out := struct {
		Ops         []opJSON `json:"ops"`
		SnapshotSeq int64    `json:"snapshot_seq"`
	}{SnapshotSeq: h.SnapshotSeq}
	for _, o := range h.Ops {
		out.Ops = append(out.Ops, opToJSON(o))
	}
	if out.Ops == nil {
		out.Ops = []opJSON{}
	}
	writeJSON(w, http.StatusOK, out)
}
