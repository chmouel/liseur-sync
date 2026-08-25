package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// annotationJSON is the wire shape of an annotation (ADR-0028). A
// tombstone carries nothing but identity, rev, seq and when, which is
// why almost everything here is omitempty.
type annotationJSON struct {
	ID          string          `json:"id"`
	Rev         int64           `json:"rev"`
	Seq         int64           `json:"seq,omitempty"`
	WorkID      string          `json:"work_id,omitempty"`
	EditionSHA  *string         `json:"edition_sha,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Locator     json.RawMessage `json:"locator,omitempty"`
	Progression *float64        `json:"progression,omitempty"`
	Excerpt     string          `json:"excerpt,omitempty"`
	Color       string          `json:"color,omitempty"`
	Body        string          `json:"body,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	ClientTS    string          `json:"client_ts,omitempty"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
	Deleted     bool            `json:"deleted,omitempty"`
	DeletedAt   string          `json:"deleted_at,omitempty"`
}

const wireTimeFormat = "2006-01-02T15:04:05.999999999Z"

func annotationToJSON(a store.Annotation) annotationJSON {
	out := annotationJSON{
		ID:        a.ID,
		Rev:       a.Rev,
		Seq:       a.Seq,
		UpdatedAt: a.UpdatedAt.UTC().Format(wireTimeFormat),
	}
	if a.Deleted() {
		// A tombstone carries nothing but identity, rev, seq and when
		// — not even the work it hung on.
		out.Deleted = true
		out.DeletedAt = a.DeletedAt.UTC().Format(wireTimeFormat)
		return out
	}
	out.WorkID = a.WorkID
	out.EditionSHA = a.EditionSHA
	out.Kind = string(a.Kind)
	out.Progression = a.Progression
	out.Excerpt = a.Excerpt
	out.Color = a.Color
	out.Body = a.Body
	out.DeviceID = a.DeviceID
	out.ClientTS = a.ClientTS.UTC().Format(wireTimeFormat)
	if len(a.LocatorJSON) > 0 {
		out.Locator = json.RawMessage(a.LocatorJSON)
	}
	return out
}

// annotationWriteJSON is one item of a batched push.
type annotationWriteJSON struct {
	ID          string          `json:"id"`
	BaseRev     int64           `json:"base_rev"`
	WorkID      string          `json:"work_id"`
	EditionSHA  *string         `json:"edition_sha,omitempty"`
	Kind        string          `json:"kind"`
	Locator     json.RawMessage `json:"locator,omitempty"`
	Progression *float64        `json:"progression,omitempty"`
	Excerpt     string          `json:"excerpt,omitempty"`
	Color       string          `json:"color,omitempty"`
	Body        string          `json:"body,omitempty"`
	ClientTS    string          `json:"client_ts"`
}

// validateAnnotationWrite bounds one push item at the handler edge.
// Empty means valid; anything else is the 400 reason.
func (s *Server) validateAnnotationWrite(in annotationWriteJSON) (store.AnnotationWrite, string) {
	var out store.AnnotationWrite
	// A JSON null is an absent locator, not an anchor: RawMessage
	// holds "null" as four bytes, and four bytes must not satisfy a
	// highlight's locator requirement.
	if len(bytes.TrimSpace(in.Locator)) == 0 || bytes.Equal(bytes.TrimSpace(in.Locator), []byte("null")) {
		in.Locator = nil
	}
	if in.ID == "" || len(in.ID) > 64 {
		return out, "id required (at most 64 bytes)"
	}
	if in.BaseRev < 0 {
		return out, "base_rev must be >= 0"
	}
	if in.WorkID == "" || len(in.WorkID) > annotationMaxRefBytes {
		return out, "work_id required (at most 128 bytes)"
	}
	if in.EditionSHA != nil && len(*in.EditionSHA) > annotationMaxRefBytes {
		return out, "edition_sha too large"
	}
	kind := store.AnnotationKind(in.Kind)
	if !kind.Valid() {
		return out, "kind must be highlight, note or bookmark"
	}
	switch kind {
	case store.AnnotationNote:
		// A standalone note is a body with no anchor.
		if len(in.Locator) > 0 {
			return out, "a note carries no locator"
		}
		if in.Body == "" {
			return out, "a note requires a body"
		}
	default:
		// A highlight or bookmark anchors to the text.
		if len(in.Locator) == 0 {
			return out, string(kind) + " requires a locator"
		}
	}
	if kind == store.AnnotationBookmark && in.Body != "" {
		return out, "a bookmark carries no body"
	}
	if len(in.Locator) > s.Cfg.Ops.MaxLocatorBytes {
		return out, "locator too large"
	}
	if in.Progression != nil && (*in.Progression < 0 || *in.Progression > 1) {
		return out, "progression out of range [0,1]"
	}
	if len(in.Excerpt) > s.Cfg.Ops.AnnotationMaxExcerptBytes {
		return out, "excerpt too large"
	}
	if len(in.Body) > s.Cfg.Ops.AnnotationMaxBodyBytes {
		return out, "body too large"
	}
	if !store.ValidAnnotationColor(in.Color) {
		return out, "color must be one of the palette tokens"
	}
	if in.Color != "" && kind != store.AnnotationHighlight {
		return out, "color belongs to a highlight"
	}
	ts, err := time.Parse(time.RFC3339Nano, in.ClientTS)
	if err != nil || len(in.ClientTS) > annotationMaxClientTSBytes {
		return out, "bad client_ts"
	}
	if ts.After(time.Now().Add(24 * time.Hour)) {
		return out, "client_ts in the future"
	}
	return store.AnnotationWrite{
		ID:          in.ID,
		BaseRev:     in.BaseRev,
		WorkID:      in.WorkID,
		EditionSHA:  in.EditionSHA,
		Kind:        kind,
		LocatorJSON: in.Locator,
		Progression: in.Progression,
		Excerpt:     in.Excerpt,
		Color:       in.Color,
		Body:        in.Body,
		ClientTS:    ts,
	}, ""
}

// annotationMaxRefBytes bounds the identifiers a push may quote
// (work_id, edition_sha): a sha256 hex is 64 bytes, and no id this
// server mints is longer, so 128 is generous without letting an
// identifier column carry a payload.
const annotationMaxRefBytes = 128

// annotationMaxClientTSBytes bounds `client_ts`: a full RFC3339 stamp
// with nanoseconds and a zone offset is 35 bytes, but Go accepts
// arbitrarily long fractional seconds, so without a cap the one
// "small" field would be the unbounded one.
const annotationMaxClientTSBytes = 64

// annotationMaxRequestBytes sizes the push route's body cap from the
// documented per-item and per-batch bounds, so a batch every one of
// whose items obeys them always fits: the per-field caps are the
// contract, not a shared envelope that quietly undercuts them. The
// decoded string caps are multiplied by JSON's worst-case escaping
// expansion (`\u00XX`, six wire bytes per decoded byte); the locator
// cap is already measured in wire bytes and passes through as is.
func (s *Server) annotationMaxRequestBytes() int64 {
	const escape = 6
	perItem := escape*int64(s.Cfg.Ops.AnnotationMaxBodyBytes+
		s.Cfg.Ops.AnnotationMaxExcerptBytes+
		64+2*annotationMaxRefBytes+ // id, work_id, edition_sha
		annotationMaxClientTSBytes) +
		int64(s.Cfg.Ops.MaxLocatorBytes) +
		1024 // keys, revs, progression, color, punctuation
	return int64(s.Cfg.Ops.AnnotationMaxBatch) * perItem
}

// HandlePushAnnotations implements POST /v1/annotations — batched,
// compare-and-set on rev, never atomic: the response is 200 with one
// result per item, and one bad item fails alone, whether its problem
// is shape (an oversized body, a color off the palette) or reference
// (a stale rev, an unknown work, the per-work cap). The token's
// server-side device_id stamps every record.
func (s *Server) HandlePushAnnotations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Annotations []json.RawMessage `json:"annotations"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.annotationMaxRequestBytes())).Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Annotations) == 0 {
		writeError(w, http.StatusBadRequest, "annotations required")
		return
	}
	if len(body.Annotations) > s.Cfg.Ops.AnnotationMaxBatch {
		writeError(w, http.StatusBadRequest, "batch too large")
		return
	}

	type item struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Rev    int64           `json:"rev,omitempty"`
		Seq    int64           `json:"seq,omitempty"`
		Reason string          `json:"reason,omitempty"`
		Server *annotationJSON `json:"server,omitempty"`
	}
	out := struct {
		Results []item `json:"results"`
	}{Results: make([]item, len(body.Annotations))}

	// Shape errors are per-item results like every other failure —
	// the ADR's contract is that one bad item fails alone. That
	// includes an item the decoder itself chokes on (a string where a
	// number belongs, an overflowing rev), which is why each element
	// is unmarshalled on its own: the invalid ones are answered here
	// and only the rest reach the store, keeping their positions in
	// the response.
	items := make([]store.AnnotationWrite, 0, len(body.Annotations))
	itemPos := make([]int, 0, len(body.Annotations))
	for i, raw := range body.Annotations {
		var in annotationWriteJSON
		if err := json.Unmarshal(raw, &in); err != nil {
			// Best effort at naming the item it refuses.
			var probe struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &probe)
			out.Results[i] = item{ID: probe.ID, Status: "invalid", Reason: "malformed annotation"}
			continue
		}
		parsed, reason := s.validateAnnotationWrite(in)
		if reason != "" {
			out.Results[i] = item{ID: in.ID, Status: "invalid", Reason: reason}
			continue
		}
		items = append(items, parsed)
		itemPos = append(itemPos, i)
	}

	if len(items) > 0 {
		results, err := s.St.PushAnnotations(r.Context(), tok.UserID, tok.DeviceID, items, s.Cfg.Ops.AnnotationMaxPerWork)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "push failed")
			return
		}
		for j, res := range results {
			entry := item{ID: res.ID, Status: res.Status, Rev: res.Rev, Seq: res.Seq, Reason: res.Reason}
			if res.Server != nil {
				server := annotationToJSON(*res.Server)
				entry.Server = &server
			}
			out.Results[itemPos[j]] = entry
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleAnnotationChanges implements
// GET /v1/annotations/changes?since=<seq>&limit=<n> — the delta pull,
// same page contract as /v1/changes, tombstones included.
// /v1/changes itself remains positions-only.
func (s *Server) HandleAnnotationChanges(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	page, err := s.St.AnnotationChanges(r.Context(), tok.UserID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "changes failed")
		return
	}
	out := struct {
		Annotations []annotationJSON `json:"annotations"`
		HighWater   int64            `json:"high_water"`
		HasMore     bool             `json:"has_more"`
	}{HighWater: page.HighWater, HasMore: page.HasMore, Annotations: []annotationJSON{}}
	for _, a := range page.Annotations {
		out.Annotations = append(out.Annotations, annotationToJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleWorkAnnotations implements GET /v1/works/{id}/annotations —
// the live set for one work, ordered by progression then client_ts.
func (s *Server) HandleWorkAnnotations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	if _, err := s.St.WorkByID(r.Context(), tok.UserID, workID); err != nil {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	list, err := s.St.WorkAnnotations(r.Context(), tok.UserID, workID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "annotations failed")
		return
	}
	out := struct {
		Annotations []annotationJSON `json:"annotations"`
	}{Annotations: []annotationJSON{}}
	for _, a := range list {
		out.Annotations = append(out.Annotations, annotationToJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleDeleteAnnotation implements DELETE /v1/annotations/{id}?rev=N —
// writes the tombstone iff rev matches, 409 with the server copy
// otherwise; deleting a tombstone is already accepted.
func (s *Server) HandleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	id := r.PathValue("id")
	rev, err := strconv.ParseInt(r.URL.Query().Get("rev"), 10, 64)
	if err != nil || rev < 1 {
		writeError(w, http.StatusBadRequest, "rev query parameter required")
		return
	}
	res, err := s.St.DeleteAnnotation(r.Context(), tok.UserID, id, rev)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "annotation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if res.Status == "conflict" {
		server := annotationToJSON(*res.Server)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "rev conflict",
			"server": server,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"status": res.Status,
		"rev":    res.Rev,
		"seq":    res.Seq,
	})
}
