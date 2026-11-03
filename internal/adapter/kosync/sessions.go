package kosync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type sessionEvent struct {
	EventID       string `json:"event_id"`
	Document      string `json:"document"`
	Start         string `json:"start_time"`
	End           string `json:"end_time"`
	Emitted       string `json:"event_ts"`
	Duration      string `json:"duration_seconds"`
	StartProgress string `json:"start_progress_bp"`
	EndProgress   string `json:"end_progress_bp"`
	Scale         string `json:"progress_scale"`
}

func (e *sessionEvent) session(slot string, now time.Time) (store.Session, string, error) {
	invalid := errors.New("invalid reader.session event")
	if e.Scale != "10000" {
		return store.Session{}, "", invalid
	}
	if len(e.EventID) == 0 || len(e.EventID) > 128 {
		return store.Session{}, "", invalid
	}
	for _, c := range e.EventID {
		if c < 33 || c > 126 {
			return store.Session{}, "", invalid
		}
	}
	e.Document = strings.ToLower(e.Document)
	digest, err := hex.DecodeString(e.Document)
	if err != nil || len(digest) != 16 {
		return store.Session{}, "", invalid
	}
	fields := []*string{&e.Start, &e.End, &e.Emitted, &e.Duration, &e.StartProgress, &e.EndProgress, &e.Scale}
	values := make([]int64, len(fields))
	for i, field := range fields {
		for _, c := range *field {
			if c < '0' || c > '9' {
				return store.Session{}, "", invalid
			}
		}
		v, err := strconv.ParseInt(*field, 10, 64)
		if err != nil || v < 0 {
			return store.Session{}, "", invalid
		}
		values[i] = v
		*field = strconv.FormatInt(v, 10)
	}
	start, end, emitted, duration, sp, ep, scale := values[0], values[1], values[2], values[3], values[4], values[5], values[6]
	if start <= 0 || emitted > now.Add(5*time.Minute).Unix() || emitted < start || duration < 10 || duration > emitted-start || end != start+duration || scale != 10000 || sp > 10000 || ep > 10000 || ep < sp {
		return store.Session{}, "", invalid
	}
	raw, _ := json.Marshal(e)
	payload := sha256.Sum256(raw)
	id := sha256.Sum256([]byte(slot + "\x00" + e.EventID))
	active := duration * 1000
	return store.Session{
		SessionID: hex.EncodeToString(id[:]),
		StartedAt: time.Unix(start, 0).UTC(), EndedAt: time.Unix(emitted, 0).UTC(),
		StartProg: float64(sp) / 10000, EndProg: float64(ep) / 10000, ActiveMs: &active,
	}, hex.EncodeToString(payload[:]), nil
}

func (s *Server) HandleSession(w http.ResponseWriter, r *http.Request) {
	d, err := s.deviceFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var event sessionEvent
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	session, hash, err := event.session(d.DeviceSlot, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	outcome, err := s.St.AppendKosyncSession(r.Context(), d.UserID, d.DeviceSlot, event.Document, event.EventID, hash, session)
	switch {
	case errors.Is(err, store.ErrIDMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "event id already has a different payload"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "enable CrossPoint statistics for this slot first"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store session"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": outcome})
	}
}
