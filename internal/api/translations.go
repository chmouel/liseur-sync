package api

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// translationJSON is the wire shape of one cached sentence translation
// (ADR-0052): the client's opaque content hash and the translated text.
// updated_at and device_id are the server's stamps; the client sends
// neither, and the sentence itself never travels — the key is a one-way
// hash of it, which is the whole privacy bargain of the feature.
type translationJSON struct {
	Key         string `json:"key"`
	Translation string `json:"translation"`
	DeviceID    string `json:"device_id,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// errCodeBadKey, errCodeTooLarge and errCodeBadValue are the
// translation push's own item codes: the shape a cached translation
// can get wrong is exactly three — a key that is not the 64 hex chars
// the client's cache is keyed by, a value over the per-value bound, and
// a value holding a NUL, which PostgreSQL refuses outright and SQLite
// cannot measure for the budget (its LENGTH stops at one). missing_field
// is shared with ops.
const (
	errCodeBadKey   = "bad_key"
	errCodeTooLarge = "too_large"
	errCodeBadValue = "bad_value"
)

// validTranslationKey reports whether k is 64 hex characters: the
// SHA-256 spelling every client of this cache derives its keys with.
// Bound at the edge rather than in the store, in the style of the
// annotation write validation, so a bad shape is a 400 that names the
// item and not a constraint violation that aborts a batch.
func validTranslationKey(k string) bool {
	if len(k) != 64 {
		return false
	}
	_, err := hex.DecodeString(k)
	return err == nil
}

// translationMaxRequestBytes sizes the push route's body cap from the
// documented per-item bound, so a batch every item of which obeys it
// always fits: the per-value cap is the contract, not a shared envelope
// that quietly undercuts it. The decoded string cap is multiplied by
// JSON's worst-case escaping expansion (`\u00XX`, six wire bytes per
// decoded byte); the key is hex and needs none.
func (s *Server) translationMaxRequestBytes() int64 {
	const escape = 6
	perItem := escape*int64(s.Cfg.Ops.TranslationMaxValueBytes) +
		64 + // key
		1024 // keys, punctuation
	return int64(s.Cfg.Ops.MaxBatch) * perItem
}

// HandlePushWorkTranslations implements
// POST /v1/works/{id}/translations — a batched, atomic, idempotent
// upsert of cached sentence translations. Last write wins; an identical
// push changes nothing. The token's server-side device_id stamps every
// row. Validation stops at the first bad item and refuses the whole
// batch, like an ops batch: the refusal names the item (code,
// item_index, key) so the client can set it aside and send the rest
// again.
func (s *Server) HandlePushWorkTranslations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Translations []struct {
			Key         string `json:"key"`
			Translation string `json:"translation"`
		} `json:"translations"`
	}
	if decodeBatch(w, r, s.translationMaxRequestBytes(), &body) {
		return
	}
	if len(body.Translations) == 0 {
		writeError(w, http.StatusBadRequest, "translations required")
		return
	}
	if len(body.Translations) > s.Cfg.Ops.MaxBatch {
		writeBatchTooLarge(w, s.Cfg.Ops.MaxBatch)
		return
	}

	items := make([]store.WorkTranslationWrite, len(body.Translations))
	for i, in := range body.Translations {
		switch {
		case in.Key == "":
			writeItemRefusal(w, errCodeMissingField, "translation", "key", in.Key, i, "key required", 0)
			return
		case !validTranslationKey(in.Key):
			writeItemRefusal(w, errCodeBadKey, "translation", "key", in.Key, i,
				"key must be 64 hex characters", 0)
			return
		case in.Translation == "":
			writeItemRefusal(w, errCodeMissingField, "translation", "key", in.Key, i,
				"translation required", 0)
			return
		case len(in.Translation) > s.Cfg.Ops.TranslationMaxValueBytes:
			writeItemRefusal(w, errCodeTooLarge, "translation", "key", in.Key, i,
				"translation too large", s.Cfg.Ops.TranslationMaxValueBytes)
			return
		case strings.ContainsRune(in.Translation, 0):
			writeItemRefusal(w, errCodeBadValue, "translation", "key", in.Key, i,
				"translation must not contain NUL", 0)
			return
		}
		items[i] = store.WorkTranslationWrite{Key: in.Key, Translation: in.Translation}
	}

	if err := s.St.PushWorkTranslations(
		r.Context(), tok.UserID, r.PathValue("id"), tok.DeviceID,
		items, s.Cfg.Ops.TranslationBudgetBytes,
	); err != nil {
		if writeStoreItemError(w, err, "translation", "key") {
			return
		}
		writeError(w, http.StatusInternalServerError, "push failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "applied"})
}

// HandlePullWorkTranslations implements
// GET /v1/works/{id}/translations?limit=&after= — one page of the
// work's translation cache in (updated_at, key) order, strictly after
// the opaque cursor. The first pull is the whole work; later pulls are
// the delta. A cursor that does not parse is refused rather than read
// as "start over": the client drops its cursor and pulls from the
// beginning, which costs requests but never skips a row.
func (s *Server) HandlePullWorkTranslations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	workID := r.PathValue("id")
	after := r.URL.Query().Get("after")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// The default is chosen first and clamped after: a server configured
	// below the default must not hand out more than it promised.
	if limit <= 0 {
		limit = 500
	}
	if limit > s.Cfg.Ops.TranslationMaxPull {
		limit = s.Cfg.Ops.TranslationMaxPull
	}

	page, err := s.St.PullWorkTranslations(r.Context(), tok.UserID, workID, after, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeError(w, http.StatusBadRequest, "after must be a translation cursor")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pull failed")
		return
	}
	out := struct {
		Translations []translationJSON `json:"translations"`
		NextCursor   string            `json:"next_cursor,omitempty"`
		HasMore      bool              `json:"has_more"`
	}{Translations: []translationJSON{}, HasMore: page.HasMore}
	for _, t := range page.Translations {
		out.Translations = append(out.Translations, translationJSON{
			Key:         t.Key,
			Translation: t.Translation,
			DeviceID:    t.DeviceID,
			UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	out.NextCursor = page.NextCursor
	writeJSON(w, http.StatusOK, out)
}

// HandleDeleteWorkTranslations implements
// DELETE /v1/works/{id}/translations — drop one work's cached
// translations from the account (ADR-0053). Idempotent: a clear of an
// empty cache answers the same as the first. A work the user does not
// have is the pull's 404 with its JSON body, which a client takes as
// "nothing left to clear" and tells apart from an older server's bare
// 404 for a route it never had.
func (s *Server) HandleDeleteWorkTranslations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	n, err := s.St.DeleteWorkTranslations(r.Context(), tok.UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "work not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "clear failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// HandleDeleteAllWorkTranslations implements DELETE /v1/translations —
// drop the account's whole translation cache, every work at once
// (ADR-0053). Idempotent.
func (s *Server) HandleDeleteAllWorkTranslations(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	n, err := s.St.DeleteAllWorkTranslations(r.Context(), tok.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "clear failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
