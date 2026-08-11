// Package koplugin implements the /adapter/koplugin/* endpoints
// accepting the KoInsight-shape upload from KOReader's statistics
// plugin (design §7.2).
//
// Auth: the stock plugin cannot set headers, so the adapter uses a
// capability URL — /adapter/koplugin/{capability}/api/... — where the
// capability is a dedicated adapter-only credential (koplugin_devices),
// stored hashed. device_id is derived from the credential row; a device
// id in the payload is ignored.
package koplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Server is the koplugin adapter.
type Server struct {
	St store.Store
}

// sessionNS is the fixed UUIDv5 namespace for derived session IDs.
var sessionNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0001")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// deviceFrom resolves the capability URL credential.
func (s *Server) deviceFrom(r *http.Request) (store.KopluginDevice, error) {
	cap := r.PathValue("capability")
	if cap == "" {
		return store.KopluginDevice{}, errAuth
	}
	d, err := s.St.KopluginDeviceByToken(r.Context(), auth.HashSecret(cap))
	if err != nil || d.RevokedAt != nil {
		return store.KopluginDevice{}, errAuth
	}
	// Per-user adapter gate.
	u, err := s.St.UserByID(r.Context(), d.UserID)
	if err != nil || !u.KopluginEnabled {
		return store.KopluginDevice{}, errAuth
	}
	return d, nil
}

var errAuth = errString("invalid capability")

type errString string

func (e errString) Error() string { return string(e) }

// sourceKey builds the length-prefixed legacy upsert key
// (device_id, book_md5, page, start_time).
func sourceKey(deviceID, bookMD5 string, page int64, startTime int64) string {
	return fmt.Sprintf("%d:%s|%d:%s|%d|%d",
		len(deviceID), deviceID, len(bookMD5), bookMD5, page, startTime)
}

// sessionIDFor derives the deterministic session UUID. For the first
// revision it is UUIDv5(ns, key); for a changed payload the caller
// passes the payload hash to disambiguate.
func sessionIDFor(key string, payloadHash string) string {
	name := key
	if payloadHash != "" {
		name = key + ":" + payloadHash
	}
	return uuid.NewSHA1(sessionNS, []byte(name)).String() // UUIDv5-shaped
}

// row is one KoInsight page-stat row.
type row struct {
	BookMD5    string `json:"book_md5"`
	Page       int64  `json:"page"`
	TotalPages int64  `json:"total_pages"`
	StartTime  int64  `json:"start_time"` // unix seconds
	Duration   int64  `json:"duration"`   // seconds
}

// uploadBody mirrors the KoInsight plugin payload.
type uploadBody struct {
	Version string `json:"version"`
	Rows    []row  `json:"rows"`
	// KoInsight 0.3.0 wraps rows as "sessions" in some versions; accept both.
	Sessions []row `json:"sessions"`
}

// HandleUpload implements POST /adapter/koplugin/{capability}/api/plugin/...
// (the exact trailing path is ignored; the stock plugin posts to a
// fixed route under the configured base).
func (s *Server) HandleUpload(w http.ResponseWriter, r *http.Request) {
	d, err := s.deviceFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body uploadBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeError422(w, "invalid JSON body")
		return
	}
	rows := body.Rows
	if len(rows) == 0 {
		rows = body.Sessions
	}
	if len(rows) == 0 {
		writeError422(w, "no rows")
		return
	}

	ctx := r.Context()
	inserted, superseded, dup := 0, 0, 0
	var rejects []string

	for i, rw := range rows {
		// Loud 422s for rows the legacy protocol would silently drop or
		// mangle (design §7.2).
		switch {
		case rw.Duration <= 0:
			rejects = append(rejects, fmt.Sprintf("row %d: duration <= 0", i))
			continue
		case rw.TotalPages <= 0:
			rejects = append(rejects, fmt.Sprintf("row %d: total_pages <= 0", i))
			continue
		case rw.Page < 1 || rw.Page > rw.TotalPages:
			rejects = append(rejects, fmt.Sprintf("row %d: page %d outside [1,%d]", i, rw.Page, rw.TotalPages))
			continue
		case rw.StartTime <= 0:
			rejects = append(rejects, fmt.Sprintf("row %d: start_time <= 0", i))
			continue
		case rw.BookMD5 == "":
			rejects = append(rejects, fmt.Sprintf("row %d: book_md5 required", i))
			continue
		}

		md5v := strings.ToLower(rw.BookMD5)
		workID, _, err := s.St.CreatePendingWork(ctx, d.UserID, md5v)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}

		key := sourceKey(d.DeviceID, md5v, rw.Page, rw.StartTime)
		// Deterministic session id over key + full payload, so a changed
		// payload yields a new session (supersession) and an identical
		// re-upload is a duplicate.
		payloadHash := sha256hex(fmt.Sprintf("%d|%d|%d|%d", rw.StartTime, rw.Duration, rw.Page, rw.TotalPages))
		ses := store.Session{
			SessionID:   sessionIDFor(key, payloadHash),
			WorkID:      workID,
			DeviceID:    d.DeviceID,
			StartedAt:   time.Unix(rw.StartTime, 0),
			EndedAt:     time.Unix(rw.StartTime+rw.Duration, 0),
			StartProg:   float64(rw.Page-1) / float64(rw.TotalPages),
			EndProg:     float64(rw.Page) / float64(rw.TotalPages),
			Origin:      store.OriginKoplugin,
			OriginAlias: strPtr("partial-md5:" + md5v),
			SourceKey:   strPtr(key),
		}
		status, err := s.St.UpsertKopluginSession(ctx, d.UserID, ses)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		switch status {
		case "inserted":
			inserted++
		case "superseded":
			superseded++
		default:
			dup++
		}
	}

	resp := map[string]any{
		"inserted":   inserted,
		"superseded": superseded,
		"duplicate":  dup,
	}
	if len(rejects) > 0 {
		resp["rejected"] = rejects
		writeJSON(w, http.StatusUnprocessableEntity, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleSupersedeUpload handles a changed payload for an existing key:
// the caller layer recomputes the session with a payload-hashed ID.
// (Folded into UpsertKopluginSession; this comment documents the seam.)

func writeError422(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"rejected": []string{msg}})
}

func strPtr(s string) *string { return &s }

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Mount registers the adapter routes. The capability secret appears in
// the path, so anything that logs a path must run it through
// api.RedactPath first.
func (s *Server) Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler) {
	mux.Handle("POST /adapter/koplugin/{capability}/api/plugin/upload",
		secure(http.HandlerFunc(s.HandleUpload)))
}
