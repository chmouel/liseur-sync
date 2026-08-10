package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// sessionJSON is the wire shape of a reading session (design §6.1).
type sessionJSON struct {
	SessionID string  `json:"session_id"`
	WorkID    string  `json:"work_id"`
	EditionSHA *string `json:"edition_sha,omitempty"`
	StartedAt string  `json:"started_at"`
	EndedAt   string  `json:"ended_at"`
	StartProg float64 `json:"start_progression"`
	EndProg   float64 `json:"end_progression"`
	IdleMs    int64   `json:"idle_ms,omitempty"`
}

// HandlePushSessions implements POST /v1/sessions — batch, idempotent
// on session_id, append-only. The token's device_id stamps each row.
func (s *Server) HandlePushSessions(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Sessions []sessionJSON `json:"sessions"`
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
		if in.StartProg < 0 || in.StartProg > 1 || in.EndProg < 0 || in.EndProg > 1 {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": progression out of range [0,1]")
			return
		}
		durMs := ended.Sub(started).Milliseconds()
		if in.IdleMs < 0 || in.IdleMs > durMs {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": idle_ms out of range")
			return
		}
		if _, err := s.St.WorkByID(r.Context(), tok.UserID, in.WorkID); err != nil {
			writeError(w, http.StatusBadRequest, "session "+in.SessionID+": unknown work")
			return
		}
		sessions[i] = store.Session{
			SessionID:  in.SessionID,
			WorkID:     in.WorkID,
			EditionSHA: in.EditionSHA,
			DeviceID:   tok.DeviceID,
			StartedAt:  started,
			EndedAt:    ended,
			StartProg:  in.StartProg,
			EndProg:    in.EndProg,
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
