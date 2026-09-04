package api

import (
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// sessionReqJSON is the inbound shape of POST /v1/sessions. Its
// progression fields are pointers on purpose: a missing or null value
// is a rejected request, not a silent 0. A session recorded as 0→0
// corrupts the statistics it feeds. 0 remains a legitimate value; only
// null, absent, non-finite or out-of-range is refused.
type sessionReqJSON struct {
	SessionID  string   `json:"session_id"`
	WorkID     string   `json:"work_id"`
	EditionSHA *string  `json:"edition_sha"`
	StartedAt  string   `json:"started_at"`
	EndedAt    string   `json:"ended_at"`
	StartProg  *float64 `json:"start_progression"`
	EndProg    *float64 `json:"end_progression"`
	IdleMs     int64    `json:"idle_ms"`
}

// HandlePushSessions implements POST /v1/sessions — batch, idempotent
// on session_id, append-only. The token's device_id stamps each row.
// Refusals follow the same shape as POST /v1/ops: the first bad item
// names itself (code, item_index, session_id) and the whole batch is
// refused; the work check and the idempotency check live in the store,
// inside the append transaction.
func (s *Server) HandlePushSessions(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Sessions []sessionReqJSON `json:"sessions"`
	}
	if decodeBatch(w, r, s.Cfg.Ops.MaxBodyBytes, &body) {
		return
	}
	if len(body.Sessions) == 0 {
		writeError(w, http.StatusBadRequest, "sessions required")
		return
	}
	if len(body.Sessions) > maxSessionBatch {
		writeBatchTooLarge(w, maxSessionBatch)
		return
	}

	sessions := make([]store.Session, len(body.Sessions))
	for i, in := range body.Sessions {
		refuse := func(code, what string) {
			writeItemRefusal(w, code, "session", "session_id", in.SessionID, i, what, 0)
		}
		if in.SessionID == "" || len(in.SessionID) > 64 {
			in.SessionID = ""
			refuse(errCodeMissingField, "session_id required")
			return
		}
		if in.WorkID == "" {
			refuse(errCodeMissingField, "work_id required")
			return
		}
		started, err := time.Parse(time.RFC3339Nano, in.StartedAt)
		if err != nil {
			refuse(errCodeBadTime, "bad started_at")
			return
		}
		ended, err := time.Parse(time.RFC3339Nano, in.EndedAt)
		if err != nil {
			refuse(errCodeBadTime, "bad ended_at")
			return
		}
		if ended.Before(started) {
			refuse(errCodeBadTime, "ended_at before started_at")
			return
		}
		if in.StartProg == nil || in.EndProg == nil {
			refuse(errCodeMissingField, "progression required")
			return
		}
		// Negation of the in-range test so a NaN (both comparisons
		// false) is rejected rather than let through.
		sp, ep := *in.StartProg, *in.EndProg
		if !(sp >= 0 && sp <= 1) || !(ep >= 0 && ep <= 1) {
			refuse(errCodeProgression, "progression out of range [0,1]")
			return
		}
		durMs := ended.Sub(started).Milliseconds()
		if in.IdleMs < 0 || in.IdleMs > durMs {
			refuse(errCodeIdleOutOfRange, "idle_ms out of range")
			return
		}
		sessions[i] = store.Session{
			SessionID:  in.SessionID,
			WorkID:     in.WorkID,
			EditionSHA: in.EditionSHA,
			DeviceID:   tok.DeviceID,
			StartedAt:  started,
			EndedAt:    ended,
			StartProg:  sp,
			EndProg:    ep,
			IdleMs:     in.IdleMs,
			Origin:     store.OriginNative,
		}
	}

	if err := s.St.AppendSessions(r.Context(), tok.UserID, sessions); err != nil {
		if writeStoreItemError(w, err, "session", "session_id") {
			return
		}
		writeError(w, http.StatusInternalServerError, "append failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": len(sessions)})
}

// maxSessionBatch is how many sessions one request may carry.
const maxSessionBatch = 1000
