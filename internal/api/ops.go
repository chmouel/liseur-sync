package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// opReqJSON is the inbound shape of POST /v1/ops. It carries only the
// fields a client sends, and progression is a pointer on purpose: a
// missing or null value is a rejected request, not a silent 0. opJSON
// keeps a plain float64 because it also builds responses (opToJSON),
// where a pointer would risk emitting "progression": null to every
// existing client — a worse regression than the bug this guards. 0 is a
// legitimate position (a reader at the very start of a book still
// syncs); only null, absent, non-finite or out-of-range is refused.
type opReqJSON struct {
	OpID        string          `json:"op_id"`
	WorkID      string          `json:"work_id"`
	EditionSHA  *string         `json:"edition_sha"`
	ClientTS    string          `json:"client_ts"`
	Progression *float64        `json:"progression"`
	Locator     json.RawMessage `json:"locator"`
	ForeignPos  *string         `json:"foreign_pos"`
}

// HandlePushOps implements POST /v1/ops — batch, idempotent on op_id.
// The token's server-side device_id stamps every op; a client-supplied
// device_id is ignored.
//
// Validation stops at the first bad item and refuses the whole batch:
// the append is atomic, so partial state is never an option, and the
// refusal names the item (code, item_index, op_id) so the client can
// set it aside and send the rest again. Whether the work exists is
// decided by the store inside the append transaction, not here — a
// check before the transaction could pass and the work be deleted
// before the insert, turning a recoverable refusal into a 500.
func (s *Server) HandlePushOps(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Ops []opReqJSON `json:"ops"`
	}
	if decodeBatch(w, r, s.Cfg.Ops.MaxBodyBytes, &body) {
		return
	}
	if len(body.Ops) == 0 {
		writeError(w, http.StatusBadRequest, "ops required")
		return
	}
	if len(body.Ops) > s.Cfg.Ops.MaxBatch {
		writeBatchTooLarge(w, s.Cfg.Ops.MaxBatch)
		return
	}

	ops := make([]store.Op, len(body.Ops))
	for i, in := range body.Ops {
		refuse := func(code, what string, limit int) {
			writeItemRefusal(w, code, "op", "op_id", in.OpID, i, what, limit)
		}
		if in.OpID == "" || len(in.OpID) > 64 {
			in.OpID = ""
			refuse(errCodeMissingField, "op_id required", 0)
			return
		}
		if in.WorkID == "" {
			refuse(errCodeMissingField, "work_id required", 0)
			return
		}
		if in.Progression == nil {
			refuse(errCodeMissingField, "progression required", 0)
			return
		}
		// Written as the negation of the in-range test so a NaN (both
		// comparisons false) is rejected rather than let through, which
		// the naive `p < 0 || p > 1` would do.
		if p := *in.Progression; !(p >= 0 && p <= 1) {
			refuse(errCodeProgression, "progression out of range [0,1]", 0)
			return
		}
		if len(in.Locator) > s.Cfg.Ops.MaxLocatorBytes {
			refuse(errCodeLocatorTooBig, "locator too large", s.Cfg.Ops.MaxLocatorBytes)
			return
		}
		ts, err := time.Parse(time.RFC3339Nano, in.ClientTS)
		if err != nil {
			refuse(errCodeBadTime, "bad client_ts", 0)
			return
		}
		if ts.After(time.Now().Add(24 * time.Hour)) {
			refuse(errCodeTimeInFuture, "client_ts in the future", 0)
			return
		}
		ops[i] = store.Op{
			OpID:        in.OpID,
			WorkID:      in.WorkID,
			EditionSHA:  in.EditionSHA,
			ClientTS:    ts,
			Progression: *in.Progression,
			LocatorJSON: in.Locator,
			ForeignPos:  in.ForeignPos,
			Origin:      store.OriginNative,
		}
	}

	results, err := s.St.AppendOps(r.Context(), tok.UserID, tok.DeviceID, ops)
	if err != nil {
		if writeStoreItemError(w, err, "op", "op_id") {
			return
		}
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
//
// A cursor that does not parse, or is negative, is refused rather than
// read as zero: zero means "replay everything", and a client that sent
// garbage should hear so, not silently get a full history.
func (s *Server) HandleChanges(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "since must be a non-negative integer")
			return
		}
		since = v
	}
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
			"message":        "since is below the compaction horizon; fetch /v1/heads and resume from its snapshot_seq",
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
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
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
