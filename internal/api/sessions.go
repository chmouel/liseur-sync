package api

import (
	"encoding/json"
	"net/http"
	"strconv"
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
func (s *Server) HandlePushSessions(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Sessions []sessionReqJSON `json:"sessions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Sessions) == 0 {
		writeError(w, http.StatusBadRequest, "sessions required")
		return
	}
	if len(body.Sessions) > 1000 {
		writeError(w, http.StatusBadRequest, "batch too large")
		return
	}

	sessions := make([]store.Session, len(body.Sessions))
	for i, in := range body.Sessions {
		if in.SessionID == "" || len(in.SessionID) > 64 {
			writeError(w, http.StatusBadRequest, "session "+strconv.Itoa(i)+": session_id required")
			return
		}
		if in.WorkID == "" {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": work_id required")
			return
		}
		started, err := time.Parse(time.RFC3339Nano, in.StartedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": bad started_at")
			return
		}
		ended, err := time.Parse(time.RFC3339Nano, in.EndedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": bad ended_at")
			return
		}
		if ended.Before(started) {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": ended_at before started_at")
			return
		}
		if in.StartProg == nil || in.EndProg == nil {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": progression required")
			return
		}
		// Negation of the in-range test so a NaN (both comparisons
		// false) is rejected rather than let through.
		sp, ep := *in.StartProg, *in.EndProg
		if !(sp >= 0 && sp <= 1) || !(ep >= 0 && ep <= 1) {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": progression out of range [0,1]")
			return
		}
		durMs := ended.Sub(started).Milliseconds()
		if in.IdleMs < 0 || in.IdleMs > durMs {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": idle_ms out of range")
			return
		}
		if _, err := s.St.WorkByID(r.Context(), tok.UserID, in.WorkID); err != nil {
			writeUnknownWork(w, "session "+in.SessionID+": unknown work", "session_id", in.SessionID, in.WorkID)
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
		if err == store.ErrIDMismatch {
			writeError(w, http.StatusConflict, "session_id reused with a different payload")
			return
		}
		writeError(w, http.StatusInternalServerError, "append failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": len(sessions)})
}
