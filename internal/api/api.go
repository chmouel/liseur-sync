// Package api implements the native /v1 REST API.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Server bundles dependencies for the native API.
type Server struct {
	St   store.Store
	Auth *auth.Service
	Cfg  config.Config
	// LoginLimiter rate-limits login and token-management endpoints.
	LoginLimiter *auth.RateLimiter
	// Kosync is the kosync adapter (nil disables it regardless of
	// config).
	Kosync interface {
		Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler)
	}
	// Koplugin is the statistics-plugin adapter (nil disables it).
	Koplugin interface {
		Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler)
	}
	// WebUI is the minimal admin UI (nil disables it).
	WebUI interface {
		Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// opJSON is the wire shape of an op (design §5.1).
type opJSON struct {
	OpID        string          `json:"op_id"`
	WorkID      string          `json:"work_id,omitempty"`
	EditionSHA  *string         `json:"edition_sha,omitempty"`
	ClientTS    string          `json:"client_ts"`
	Progression float64         `json:"progression"`
	Locator     json.RawMessage `json:"locator,omitempty"`
	ForeignPos  *string         `json:"foreign_pos,omitempty"`
	Origin      string          `json:"origin,omitempty"`
	Seq         int64           `json:"seq,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	ReceivedAt  string          `json:"received_at,omitempty"`
}

func opToJSON(o store.Op) opJSON {
	out := opJSON{
		OpID:        o.OpID,
		WorkID:      o.WorkID,
		EditionSHA:  o.EditionSHA,
		ClientTS:    o.ClientTS.UTC().Format("2006-01-02T15:04:05.999999999Z"),
		Progression: o.Progression,
		ForeignPos:  o.ForeignPos,
		Origin:      string(o.Origin),
		Seq:         o.Seq,
		DeviceID:    o.DeviceID,
		ReceivedAt:  o.ReceivedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
	}
	if len(o.LocatorJSON) > 0 {
		out.Locator = json.RawMessage(o.LocatorJSON)
	}
	return out
}
