// Package api implements the native /v1 REST API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/live"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Server bundles dependencies for the native API.
type Server struct {
	St   store.Store
	Auth *auth.Service
	Cfg  config.Config
	// LoginLimiter rate-limits login and token-management endpoints.
	LoginLimiter *auth.RateLimiter
	// OPDSLimiter rate-limits the OPDS surface: feed, browse, search,
	// covers and downloads. Kept separate from LoginLimiter (ADR-0006
	// originally shared it) because one screen of a folder feed is a
	// request per visible cover plus the feed itself, which routinely
	// outnumbers a budget sized for password attempts before the reader
	// gets anywhere near tapping download.
	OPDSLimiter *auth.RateLimiter
	// Files opens the bytes behind a catalog book. Nil disables the
	// download and cover routes, which then report 503 rather than
	// panicking.
	Files BookFiles
	// Covers caches rendered covers. Nil is not an error: covers are then
	// rendered on every request, which is slow but correct.
	Covers CoverCache
	// Ingest writes an uploaded publication into a folder and asks for
	// a pass (ADR-0023). Nil disables the upload route, which then
	// reports 503: a server with no watcher has nothing to reconcile
	// with, so accepting the bytes would be a promise it cannot keep.
	Ingest BookIngest
	// Removal deletes a book's file from its folder (ADR-0025). Nil
	// disables the delete route for the same reason Ingest's nil
	// disables the upload one: without a watcher there is nothing to
	// reconcile the folder back to.
	Removal BookRemover
	// uploading serialises an upload from the digest check through the
	// write it guards. Two copies of one book arriving together would
	// otherwise both find nothing in the catalog and both be written,
	// which is the single thing the digest is there to prevent. The
	// body is already spooled before this is taken, uploads are rare,
	// and a book is small: one lock is cheaper than the machinery to
	// hold one per digest.
	uploading sync.Mutex
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
	// Live fans committed changes out to connected clients (ADR-0034).
	// Nil disables GET /v1/events, which then reports 503: a client
	// treats that as "no live events here" and keeps its own schedule.
	Live *live.Hub
	// Events bounds a live stream. The zero value takes
	// DefaultEventsPolicy.
	Events EventsPolicy
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

// errCodeUnknownWork is the one refusal a client has a move for: the
// batch named a work the server no longer holds (orphan cleanup deleted
// it), so the client re-resolves the book and retries under the fresh
// work id.
const errCodeUnknownWork = "unknown_work"

// Item-level refusal codes shared by POST /v1/ops and POST /v1/sessions.
// A batch is appended atomically, so a refusal must name the item that
// caused it well enough for a client to set that one aside and send the
// rest again without parsing prose.
const (
	errCodeMissingField   = "missing_field"
	errCodeBadTime        = "bad_time"
	errCodeTimeInFuture   = "time_in_future"
	errCodeProgression    = "progression_out_of_range"
	errCodeLocatorTooBig  = "locator_too_large"
	errCodeIdleOutOfRange = "idle_out_of_range"
	errCodeIDReused       = "id_reused"
	errCodeBatchTooLarge  = "batch_too_large"
)

// itemRefusal is the body of an item-level 4xx. item_index is always
// set; the id field ("op_id" or "session_id") only when the item had
// one — a missing_field about the id itself cannot name it.
type itemRefusal struct {
	status  int
	code    string
	msg     string
	idField string
	id      string
	index   int
	workID  string
	limit   int
}

func (e itemRefusal) write(w http.ResponseWriter) {
	body := map[string]any{
		"error":      e.msg,
		"code":       e.code,
		"item_index": e.index,
	}
	if e.id != "" {
		body[e.idField] = e.id
	}
	if e.workID != "" {
		body["work_id"] = e.workID
	}
	if e.limit > 0 {
		body["limit"] = e.limit
	}
	writeJSON(w, e.status, body)
}

// writeItemRefusal answers a validation failure on one item of a batch.
func writeItemRefusal(w http.ResponseWriter, code, kind, idField, id string, index int, what string, limit int) {
	label := kind + " " + id
	if id == "" {
		label = kind + " " + strconv.Itoa(index)
	}
	itemRefusal{
		status: http.StatusBadRequest, code: code, msg: label + ": " + what,
		idField: idField, id: id, index: index, limit: limit,
	}.write(w)
}

// writeStoreItemError maps a *store.ItemError from an append into the
// same shape: unknown_work for a work the user lacks, id_reused (409)
// for an idempotency key replayed with a different payload. Anything
// else is a genuine failure. Reports whether it answered.
func writeStoreItemError(w http.ResponseWriter, err error, kind, idField string) bool {
	var item *store.ItemError
	if !errors.As(err, &item) {
		return false
	}
	label := kind + " " + item.ID
	switch {
	case errors.Is(err, store.ErrNotFound):
		itemRefusal{
			status: http.StatusBadRequest, code: errCodeUnknownWork, msg: label + ": unknown work",
			idField: idField, id: item.ID, index: item.Index, workID: item.WorkID,
		}.write(w)
	case errors.Is(err, store.ErrIDMismatch):
		itemRefusal{
			status: http.StatusConflict, code: errCodeIDReused,
			msg:     label + ": " + idField + " reused with a different payload",
			idField: idField, id: item.ID, index: item.Index,
		}.write(w)
	default:
		return false
	}
	return true
}

// writeBatchTooLarge is the batch-level refusal; limit says how many
// items a request may carry.
func writeBatchTooLarge(w http.ResponseWriter, limit int) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": "batch too large", "code": errCodeBatchTooLarge, "limit": limit,
	})
}

// decodeBatch reads a JSON batch body under the configured byte bound.
// A body over the bound is 413, like the annotations route; anything
// else that fails to decode is 400. Reports whether it answered.
func decodeBatch(w http.ResponseWriter, r *http.Request, maxBytes int64, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(into); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return true
	}
	return false
}

// LogServerErrors wraps a handler and logs every 5xx response, so a
// failing endpoint shows up in the server log instead of only on the
// client.
func LogServerErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status >= 500 {
			slog.Error("request failed", "method", r.Method, "path", RedactPath(r.URL.Path), "status", sw.status)
		}
	})
}

// kopluginPrefix is the koplugin adapter mount point. The path segment
// that follows it is the capability secret.
const kopluginPrefix = "/adapter/koplugin/"

// RedactPath strips credentials that legacy adapters carry in the URL
// path, so they never reach a log file. The koplugin adapter
// authenticates with a capability secret embedded in the path (stock
// KOReader's statistics plugin can only be pointed at a URL, it cannot
// send a header), and that secret is a device credential stored hashed
// like any other. Every path that gets logged must go through here.
func RedactPath(p string) string {
	rest, ok := strings.CutPrefix(p, kopluginPrefix)
	if !ok {
		return p
	}
	tail := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		tail = rest[i:]
	}
	return kopluginPrefix + "[redacted]" + tail
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the real writer through
// this one. Without it a streaming handler cannot flush, and
// GET /v1/events would hold every frame until the connection closed
// while a direct handler test passed happily.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
