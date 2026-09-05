package api

import (
	"errors"
	"math"
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
	ActiveMs   *int64   `json:"active_ms"`
}

// MaxSessionActiveMs is the largest explicit active_ms the native
// sessions endpoint accepts. It is the largest JavaScript-safe integer,
// comfortably below int64's bound and therefore safe for downstream
// millisecond-to-second conversions.
const MaxSessionActiveMs int64 = 9007199254740991

const errCodeActiveOutOfRange = "active_out_of_range"

type sessionRequestError struct {
	code      string
	what      string
	sessionID string
}

func (e sessionRequestError) Error() string { return e.what }

func parseSessionRequest(in sessionReqJSON, deviceID string) (store.Session, error) {
	refuse := func(code, what string) (store.Session, error) {
		return store.Session{}, sessionRequestError{code: code, what: what, sessionID: in.SessionID}
	}
	if in.SessionID == "" || len(in.SessionID) > 64 {
		return store.Session{}, sessionRequestError{
			code: errCodeMissingField, what: "session_id required",
		}
	}
	if in.WorkID == "" {
		return refuse(errCodeMissingField, "work_id required")
	}
	started, err := time.Parse(time.RFC3339Nano, in.StartedAt)
	if err != nil {
		return refuse(errCodeBadTime, "bad started_at")
	}
	ended, err := time.Parse(time.RFC3339Nano, in.EndedAt)
	if err != nil {
		return refuse(errCodeBadTime, "bad ended_at")
	}
	if ended.Before(started) {
		return refuse(errCodeBadTime, "ended_at before started_at")
	}
	if in.StartProg == nil || in.EndProg == nil {
		return refuse(errCodeMissingField, "progression required")
	}
	sp, ep := *in.StartProg, *in.EndProg
	if math.IsNaN(sp) || math.IsInf(sp, 0) ||
		math.IsNaN(ep) || math.IsInf(ep, 0) ||
		!(sp >= 0 && sp <= 1) || !(ep >= 0 && ep <= 1) {
		return refuse(errCodeProgression, "progression out of range [0,1]")
	}
	durMs := ended.Sub(started).Milliseconds()
	if in.IdleMs < 0 || in.IdleMs > durMs {
		return refuse(errCodeIdleOutOfRange, "idle_ms out of range")
	}
	if in.ActiveMs != nil && (*in.ActiveMs < 0 || *in.ActiveMs > MaxSessionActiveMs) {
		return refuse(errCodeActiveOutOfRange, "active_ms out of range")
	}
	return store.Session{
		SessionID:  in.SessionID,
		WorkID:     in.WorkID,
		EditionSHA: in.EditionSHA,
		DeviceID:   deviceID,
		StartedAt:  started,
		EndedAt:    ended,
		StartProg:  sp,
		EndProg:    ep,
		IdleMs:     in.IdleMs,
		ActiveMs:   in.ActiveMs,
		Origin:     store.OriginNative,
	}, nil
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
		ses, err := parseSessionRequest(in, tok.DeviceID)
		if err != nil {
			var reqErr sessionRequestError
			if errors.As(err, &reqErr) {
				writeItemRefusal(w, reqErr.code, "session", "session_id",
					reqErr.sessionID, i, reqErr.what, 0)
				return
			}
			writeError(w, http.StatusInternalServerError, "append failed")
			return
		}
		sessions[i] = ses
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
