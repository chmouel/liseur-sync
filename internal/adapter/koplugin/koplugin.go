// Package koplugin implements the /adapter/koplugin/* endpoints
// accepting the KoInsight-shape upload from the KoInsight KOReader
// plugin (design §7.2). That plugin posts its statistics to
// api/plugin/import and registers itself at api/plugin/device; every
// released version does, so those are the routes mounted here.
//
// Auth: the stock plugin cannot set headers, so the adapter uses a
// capability URL — /adapter/koplugin/{capability}/api/... — where the
// capability is a dedicated adapter-only credential (koplugin_devices),
// stored hashed. device_id is derived from the credential row; a device
// id in the payload is ignored.
package koplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	capability := r.PathValue("capability")
	if capability == "" {
		return store.KopluginDevice{}, errAuth
	}
	d, err := s.St.KopluginDeviceByToken(r.Context(), auth.HashSecret(capability))
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

// Limits on one page-stat row. The row is stored through a session and
// an alias, and both backends have to be able to read it back: a NUL
// cannot be stored in PostgreSQL text, and a time past year 9999 cannot
// be parsed back from SQLite's RFC3339 text. A row that fails these is
// refused like any other bad row, not handed to the store.
// maxRejectLines caps the refused rows a response lists.
const maxRejectLines = 100

const (
	maxUnixSeconds = 253402300799 // 9999-12-31T23:59:59Z
	maxRowDuration = 24 * 60 * 60 // one page view; KOReader caps this far lower
	maxTotalPages  = 10_000_000
	maxMD5Len      = 64
)

// validAlias reports whether a book id is safe to use as a
// partial-md5 alias: KOReader's ids are hex digests.
func validAlias(s string) bool {
	if s == "" || len(s) > maxMD5Len {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// row is one KoInsight page-stat row.
type row struct {
	BookMD5    string `json:"book_md5"`
	Page       int64  `json:"page"`
	TotalPages int64  `json:"total_pages"`
	StartTime  int64  `json:"start_time"` // unix seconds
	Duration   int64  `json:"duration"`   // seconds
}

// uploadBody mirrors the KoInsight plugin payload: page-stat rows under
// "stats", plus book metadata and annotations that this adapter does not
// store. The row lists are raw because the plugin's JSON encoder writes
// an empty Lua table as {} rather than [], and because they are decoded
// one row at a time under a count limit (see decodeRows).
//
// "rows" and "sessions" are older names for the same list and are still
// read, so a client that sends either keeps working.
type uploadBody struct {
	Version     string          `json:"version"`
	Stats       json.RawMessage `json:"stats"`
	Rows        json.RawMessage `json:"rows"`
	Sessions    json.RawMessage `json:"sessions"`
	Books       json.RawMessage `json:"books"`
	Annotations json.RawMessage `json:"annotations"`
}

// maxRows bounds the rows of one request. The byte limit alone does not
// bound memory: a body of `null` entries is a few bytes per row but
// decodes to a whole zero-valued row each, so the row count is checked
// while decoding, before the list is built.
const maxRows = 500_000

var (
	errBadStats = errString("stats must be a list of rows")
	errTooMany  = errString("too many rows in one request")
)

// decodeRows decodes one row list. null, [] and an empty object are all
// "no rows": the plugin writes an empty Lua table as {}, in whatever
// spacing it likes, so the object form is recognised by structure rather
// than by its bytes. A non-empty object is an error. Rows are decoded one
// at a time and the count is checked as they come.
func decodeRows(raw json.RawMessage) ([]row, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0, string(raw) == "null":
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, errBadStats
	}
	if tok == json.Delim('{') {
		// An empty object is an empty Lua table. Anything with a
		// property is refused at the first one, without reading the rest:
		// building the object first would let a body of millions of short
		// properties allocate far more than it costs to send.
		if dec.More() {
			return nil, errBadStats
		}
		return nil, nil
	}
	if tok != json.Delim('[') {
		return nil, errBadStats
	}
	var rows []row
	for dec.More() {
		if len(rows) >= maxRows {
			return nil, errTooMany
		}
		var rw row
		if err := dec.Decode(&rw); err != nil {
			return nil, errBadStats
		}
		rows = append(rows, rw)
	}
	if _, err := dec.Token(); err != nil { // the closing ]
		return nil, errBadStats
	}
	return rows, nil
}

// statRows returns the first non-empty of stats, rows and sessions.
func (b uploadBody) statRows() ([]row, error) {
	for _, raw := range []json.RawMessage{b.Stats, b.Rows, b.Sessions} {
		rows, err := decodeRows(raw)
		if err != nil || len(rows) > 0 {
			return rows, err
		}
	}
	return nil, nil
}

// carriesKoInsightEnvelope reports whether the request has any of the
// fields the KoInsight plugin always sends. A request with stats empty
// but books or annotations present is the plugin syncing annotations,
// which this adapter does not store: that is accepted, not an error.
func (b uploadBody) carriesKoInsightEnvelope() bool {
	return len(b.Stats) > 0 || len(b.Books) > 0 || len(b.Annotations) > 0
}

// HandleUpload implements POST /adapter/koplugin/{capability}/api/plugin/import,
// the route the KoInsight plugin posts its statistics to.
func (s *Server) HandleUpload(w http.ResponseWriter, r *http.Request) {
	d, err := s.deviceFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	raw, ok := readBody(w, r, maxUploadBytes)
	if !ok {
		return
	}
	var body uploadBody
	if err := json.Unmarshal(raw, &body); err != nil { // rejects trailing input too
		writeError422(w, "invalid JSON body")
		return
	}
	rows, err := body.statRows()
	if errors.Is(err, errTooMany) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeError422(w, "invalid stats")
		return
	}
	if len(rows) == 0 {
		if body.carriesKoInsightEnvelope() {
			// The plugin also posts books and annotations on their own.
			// Nothing here is a page-stat row, and nothing is dropped
			// silently: the response says what was ignored.
			writeJSON(w, http.StatusOK, map[string]any{
				"inserted": 0, "superseded": 0, "duplicate": 0,
				"ignored": []string{"books", "annotations"},
			})
			return
		}
		writeError422(w, "no rows")
		return
	}

	ctx := r.Context()
	inserted, superseded, dup, refused := 0, 0, 0, 0
	var rejects []string
	// reject records a refused row. The list is capped so a request of
	// nothing but bad rows cannot build a response as large as itself;
	// the count stays exact.
	reject := func(format string, args ...any) {
		refused++
		if len(rejects) < maxRejectLines {
			rejects = append(rejects, fmt.Sprintf(format, args...))
		}
	}

	for i, rw := range rows {
		// Loud 422s for rows the legacy protocol would silently drop or
		// mangle (design §7.2).
		switch {
		case rw.Duration <= 0:
			reject("row %d: duration <= 0", i)
			continue
		case rw.Duration > maxRowDuration:
			reject("row %d: duration > %d", i, maxRowDuration)
			continue
		case rw.TotalPages <= 0:
			reject("row %d: total_pages <= 0", i)
			continue
		case rw.TotalPages > maxTotalPages:
			reject("row %d: total_pages > %d", i, maxTotalPages)
			continue
		case rw.Page < 1 || rw.Page > rw.TotalPages:
			reject("row %d: page %d outside [1,%d]", i, rw.Page, rw.TotalPages)
			continue
		case rw.StartTime <= 0:
			reject("row %d: start_time <= 0", i)
			continue
		case rw.StartTime > maxUnixSeconds-rw.Duration: // no overflow: Duration is bounded above
			reject("row %d: start_time out of range", i)
			continue
		case !validAlias(rw.BookMD5):
			reject("row %d: book_md5 must be 1-%d hex characters", i, maxMD5Len)
			continue
		}

		md5v := strings.ToLower(rw.BookMD5)
		key := sourceKey(d.DeviceID, md5v, rw.Page, rw.StartTime)
		// Deterministic session id over key + full payload, so a changed
		// payload yields a new session (supersession) and an identical
		// re-upload is a duplicate.
		payloadHash := sha256hex(fmt.Sprintf("%d|%d|%d|%d", rw.StartTime, rw.Duration, rw.Page, rw.TotalPages))
		reportedPages := 1.0
		ses := store.Session{
			SessionID:     sessionIDFor(key, payloadHash),
			DeviceID:      d.DeviceID,
			StartedAt:     time.Unix(rw.StartTime, 0),
			EndedAt:       time.Unix(rw.StartTime+rw.Duration, 0),
			StartProg:     float64(rw.Page-1) / float64(rw.TotalPages),
			EndProg:       float64(rw.Page) / float64(rw.TotalPages),
			ReportedPages: &reportedPages,
			Origin:        store.OriginKoplugin,
			OriginAlias:   strPtr("partial-md5:" + md5v),
			SourceKey:     strPtr(key),
		}
		status, err := s.St.UpsertKopluginSessionByAlias(ctx, d.UserID, md5v, ses)
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
	if refused > 0 {
		resp["rejected"] = rejects
		resp["rejected_total"] = refused
		// The plugin re-sends its whole statistics table on every sync, so
		// a row the adapter refuses is refused again each time. Answering
		// 422 whenever any row is bad would make that one row fail every
		// future sync even though everything else was stored. 422 is
		// therefore for a request in which nothing was accepted; a partly
		// accepted one is 200 and names what was refused, both here and
		// in the log.
		slog.Warn("koplugin upload refused rows",
			"device", d.DeviceID, "refused", refused,
			"accepted", inserted+superseded+dup)
		if inserted+superseded+dup == 0 {
			writeJSON(w, http.StatusUnprocessableEntity, resp)
			return
		}
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

// maxUploadBytes bounds an upload. The plugin re-sends its whole
// statistics table, with book metadata and annotations, on every sync, so
// the request only ever grows. A page row is about 100 bytes; this leaves
// room for several hundred thousand, which is years of daily reading.
const maxUploadBytes = 32 << 20

// readBody reads a whole request body up to limit bytes. A body over the
// limit is a 413 and anything else unreadable a 400, and both say so:
// a truncated read must never be handled as if it were the request.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request body"})
		}
		return nil, false
	}
	return raw, true
}

// Mount registers the adapter routes. The capability secret appears in
// the path, so anything that logs a path must run it through
// api.RedactPath first.
func (s *Server) Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler) {
	mux.Handle("POST /adapter/koplugin/{capability}/api/plugin/import",
		secure(http.HandlerFunc(s.HandleUpload)))
	mux.Handle("POST /adapter/koplugin/{capability}/api/plugin/device",
		secure(http.HandlerFunc(s.HandleDevice)))
}

// HandleDevice answers the plugin's device registration. The device is
// already known: its identity comes from the capability credential, and
// a device id in the body is ignored like it is everywhere else here.
// Nothing is stored; the call only has to confirm the credential works.
func (s *Server) HandleDevice(w http.ResponseWriter, r *http.Request) {
	if _, err := s.deviceFrom(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if _, ok := readBody(w, r, 64<<10); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registered": true})
}
