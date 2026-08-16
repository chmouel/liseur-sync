package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Series claims (ADR-0018). These are the only routes that write to the
// catalog from a token rather than from a reconcile pass, and they write
// to a layer beside it rather than into it: `book_series` still says
// what the folder said, and a claim says what a person said.

// maxSeriesClaimItems bounds one claim. A book in more than this many
// series is a client bug, and the bound is what keeps a claim from
// becoming an unbounded write.
const maxSeriesClaimItems = 64

// maxSeriesReorderItems bounds one renumbering. A series longer than
// this is renumbered a page at a time.
const maxSeriesReorderItems = 1000

// maxSeriesNameBytes matches what a reconcile pass would accept for a
// series name, so a claimed series and a scanned one are the same kind
// of thing.
const maxSeriesNameBytes = 512

type seriesClaimItemBody struct {
	SeriesID string   `json:"series_id"`
	Name     string   `json:"name"`
	Position *float64 `json:"position"`
}

type seriesClaimBody struct {
	Scope  string                `json:"scope"`
	Series []seriesClaimItemBody `json:"series"`
}

type seriesReorderBody struct {
	Scope string `json:"scope"`
	Order []struct {
		BookID   string   `json:"book_id"`
		Position *float64 `json:"position"`
	} `json:"order"`
}

// claimScope reads the layer a request names and checks the caller may
// write it. The shared layer speaks for everyone, so it takes admin;
// the personal layer speaks only for the caller, so library-manage —
// already required by the route — is enough.
//
// An absent scope means personal. That default is deliberate: the
// dangerous layer is the one that has to be asked for by name.
func claimScope(
	w http.ResponseWriter, r *http.Request, raw string,
) (store.SeriesSource, bool) {
	if raw == "" {
		raw = string(store.SeriesSourcePersonal)
	}
	scope := store.SeriesSource(raw)
	if !scope.Writable() {
		writeError(w, http.StatusBadRequest,
			`scope must be "personal" or "shared"`)
		return "", false
	}
	if scope == store.SeriesSourceShared {
		tok, _ := auth.TokenFrom(r)
		if !tok.Scopes.Contains(store.ScopeAdmin) {
			writeError(w, http.StatusForbidden,
				"the shared series layer is an admin capability")
			return "", false
		}
	}
	return scope, true
}

// writeSeriesClaimError maps the store's refusals onto precise 4xx.
// Malformed input never becomes a 5xx.
func writeSeriesClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "book not found")
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "series claim failed")
	}
}

// HandleSetBookSeries implements PUT /v1/books/{id}/series. The body
// states the whole of one layer's opinion about one book, including the
// empty list, which claims the book is in no series at all.
func (s *Server) HandleSetBookSeries(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body seriesClaimBody
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes),
	).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	scope, ok := claimScope(w, r, body.Scope)
	if !ok {
		return
	}
	if len(body.Series) > maxSeriesClaimItems {
		writeError(w, http.StatusBadRequest, "too many series in one claim")
		return
	}
	// An absent list and an empty one mean the same thing here, and both
	// mean "in no series". There is no way to say "I have no opinion"
	// with this route; that is what DELETE is for.
	items := make([]store.SeriesClaimItem, 0, len(body.Series))
	for _, it := range body.Series {
		if (it.SeriesID == "") == (it.Name == "") {
			writeError(w, http.StatusBadRequest,
				"each series needs exactly one of series_id or name")
			return
		}
		if len(it.Name) > maxSeriesNameBytes {
			writeError(w, http.StatusBadRequest, "series name is too long")
			return
		}
		if it.Position != nil && !validSeriesPosition(*it.Position) {
			writeError(w, http.StatusBadRequest,
				"series position must be a finite number")
			return
		}
		items = append(items, store.SeriesClaimItem{
			SeriesID: it.SeriesID, Name: it.Name, Position: it.Position,
		})
	}
	bookID := r.PathValue("id")
	if err := s.St.SetBookSeriesOverride(
		r.Context(), tok.UserID, bookID, scope, items, time.Now(),
	); err != nil {
		writeSeriesClaimError(w, err)
		return
	}
	s.writeSeriesLayers(w, r, tok.UserID, bookID)
}

// HandleClearBookSeries implements DELETE /v1/books/{id}/series. It
// removes one layer's claim so the book falls back to the layer
// beneath, and answers with what it fell back to.
func (s *Server) HandleClearBookSeries(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	scope, ok := claimScope(w, r, r.URL.Query().Get("scope"))
	if !ok {
		return
	}
	bookID := r.PathValue("id")
	if err := s.St.ClearBookSeriesOverride(
		r.Context(), tok.UserID, bookID, scope,
	); err != nil {
		writeSeriesClaimError(w, err)
		return
	}
	s.writeSeriesLayers(w, r, tok.UserID, bookID)
}

// HandleReorderSeries implements
// PUT /v1/entities/series/{entity}/order, the bulk renumbering a
// drag-reorder needs. It is one transaction: a partly
// renumbered series is worse than an unrenumbered one.
func (s *Server) HandleReorderSeries(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	if kind != store.EntitySeries {
		writeError(w, http.StatusNotFound, "only a series can be reordered")
		return
	}
	var body seriesReorderBody
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes),
	).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	scope, ok := claimScope(w, r, body.Scope)
	if !ok {
		return
	}
	if len(body.Order) == 0 {
		writeError(w, http.StatusBadRequest, "order is empty")
		return
	}
	if len(body.Order) > maxSeriesReorderItems {
		writeError(w, http.StatusBadRequest, "too many books in one reorder")
		return
	}
	order := make([]store.SeriesPlacement, 0, len(body.Order))
	seen := make(map[string]bool, len(body.Order))
	for _, p := range body.Order {
		if p.BookID == "" {
			writeError(w, http.StatusBadRequest, "order needs a book_id")
			return
		}
		if seen[p.BookID] {
			writeError(w, http.StatusBadRequest,
				"the same book appears twice in one order")
			return
		}
		seen[p.BookID] = true
		if p.Position != nil && !validSeriesPosition(*p.Position) {
			writeError(w, http.StatusBadRequest,
				"series position must be a finite number")
			return
		}
		order = append(order, store.SeriesPlacement{
			BookID: p.BookID, Position: p.Position,
		})
	}
	if err := s.St.ReorderSeries(
		r.Context(), tok.UserID, r.PathValue("entity"),
		scope, order, time.Now(),
	); err != nil {
		writeSeriesClaimError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleBookSeriesLayers implements GET /v1/books/{id}/series, which an
// editor needs: it is the only read that shows what the folder said
// underneath a claim, and therefore the only one from which a client can
// tell whether a reset would change anything.
func (s *Server) HandleBookSeriesLayers(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.writeSeriesLayers(w, r, tok.UserID, r.PathValue("id"))
}

func (s *Server) writeSeriesLayers(
	w http.ResponseWriter, r *http.Request, userID, bookID string,
) {
	layers, err := s.St.BookSeriesLayers(r.Context(), userID, bookID)
	if err != nil {
		writeSeriesClaimError(w, err)
		return
	}
	body := map[string]any{
		"book_id": bookID,
		"source":  string(layers.Source),
		"series":  seriesListJSON(layers.Effective),
		"folder":  seriesListJSON(layers.Folder),
	}
	// A nil layer is "nobody claimed", an empty one is the claim "in no
	// series". JSON null and [] carry that difference, so a client can
	// tell a reset it can offer from one it cannot.
	if layers.Shared != nil {
		body["shared"] = seriesListJSON(layers.Shared)
	} else {
		body["shared"] = nil
	}
	if layers.Personal != nil {
		body["personal"] = seriesListJSON(layers.Personal)
	} else {
		body["personal"] = nil
	}
	writeJSON(w, http.StatusOK, body)
}

// seriesListJSON renders memberships in the same shape the book payload
// uses, so a client parses one representation of a series membership.
func seriesListJSON(list []store.BookSeries) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, s := range list {
		out = append(out, bookSeriesJSON(s))
	}
	return out
}

// validSeriesPosition refuses what a float64 can carry but a series
// cannot. NaN and the infinities would sort against the unplaced
// sentinel in ways nobody intends, so they are rejected at the edge
// rather than stored and puzzled over later.
func validSeriesPosition(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0)
}
