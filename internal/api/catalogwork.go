package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

type catalogResolveRequest struct {
	// Confirmed is the reader's word that a title/author match is the
	// same book, exactly as on /v1/works/resolve.
	Confirmed bool `json:"confirmed,omitempty"`
}

type catalogResolveResponse struct {
	BookID      string           `json:"book_id"`
	WorkID      string           `json:"work_id"`
	Confidence  string           `json:"confidence"`
	Created     bool             `json:"created"`
	Identifiers []identifierJSON `json:"identifiers"`
}

// resolveBookWork joins a catalog book to one user's work and is the
// whole of what resolving means, so the route a reader asks through and
// the upload that does it unasked cannot decide it differently.
//
// It returns the identifiers it resolved on as well, because the route
// hands them back and the caller has no other way to learn which digest
// the server matched.
func (s *Server) resolveBookWork(
	ctx context.Context, userID, bookID string, confirmed bool, at time.Time,
) (store.WorkResolution, []store.Identifier, error) {
	book, err := s.St.CatalogBookByID(ctx, userID, bookID)
	if err != nil {
		return store.WorkResolution{}, nil, err
	}
	bookIDs, author, err := workident.Evidence(ctx, s.St, userID, bookID)
	if err != nil {
		return store.WorkResolution{}, nil, err
	}
	workID, err := newID()
	if err != nil {
		return store.WorkResolution{}, nil, err
	}
	proposed, editions, ids := workident.Plan(userID, workID, book, bookIDs, author)
	proposed.CreatedAt = at
	result, err := s.St.ResolveCatalogBookWork(
		ctx, userID, bookID, proposed, editions, ids, confirmed, at,
	)
	return result, ids, err
}

// HandleResolveBookWork implements POST /v1/books/{id}/resolve: it joins a
// catalog book to the caller's own sync work so that positions and sessions
// can be reported for a book that was downloaded rather than side-loaded.
//
// The client sends no identifiers. It cannot: the catalog knows the file's
// digests and embedded ids, and a client that has only browsed the catalog
// has not seen the bytes. So the server collects them, which also means two
// devices resolve the same book from the same evidence instead of from
// whatever each happened to compute.
//
// The mapping is per user. Two readers of one shared book get two work IDs,
// which is what keeps reading history private (ADR-0003).
func (s *Server) HandleResolveBookWork(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req catalogResolveRequest
	// An empty body means "not confirmed", which is the safe default and
	// saves every client from sending {} to resolve a book.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).
			Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	bookID := r.PathValue("id")
	result, ids, err := s.resolveBookWork(r.Context(), tok.UserID, bookID, req.Confirmed, time.Now())
	if err != nil {
		// A book that was replaced, or whose folder was removed, between
		// the lookup and the write is still a 404 rather than a 500.
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "work resolution conflict")
			return
		}
		writeError(w, http.StatusInternalServerError, "resolve failed")
		return
	}
	if len(result.ConflictingWorkIDs) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "identifiers resolve to multiple works",
			"works": result.ConflictingWorkIDs,
		})
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, catalogResolveResponse{
		BookID: bookID, WorkID: result.WorkID,
		Confidence: result.Confidence, Created: result.Created,
		Identifiers: identifiersJSON(ids),
	})
}

// maxResolveBatch is how many books one batch resolve may name.
//
// Sized for the case it exists for: a phone connecting to an account
// and naming a whole catalog at once. Five hundred books is a large
// library and a small request — the body is a list of ids — while the
// work behind it is five hundred short transactions, which is what the
// bound is really about.
const maxResolveBatch = 500

type catalogResolveBatchRequest struct {
	BookIDs []string `json:"book_ids"`
	// Confirmed applies to the whole batch and should be sent only by a
	// caller that has no reader to ask — never as a way of agreeing to
	// title/author matches in bulk. Confirming a doubtful match is a
	// question about one book, and the single-book route is where the
	// answer belongs.
	Confirmed bool `json:"confirmed,omitempty"`
}

// catalogResolveItemJSON is one book's answer. Either it resolved, and
// carries the same fields the single-book route returns, or it did not,
// and carries the reason.
type catalogResolveItemJSON struct {
	BookID      string           `json:"book_id"`
	WorkID      string           `json:"work_id,omitempty"`
	Confidence  string           `json:"confidence,omitempty"`
	Created     bool             `json:"created,omitempty"`
	Identifiers []identifierJSON `json:"identifiers,omitempty"`
	Error       string           `json:"error,omitempty"`
	Works       []string         `json:"works,omitempty"`
}

// Per-item refusals. These are the batch spellings of the statuses the
// single-book route answers with: its 404, the 409 that names the works
// the identifiers landed between, and the 409 it answers when the work
// graph moved under the write — which is the one worth asking about
// again, since the next attempt sees the graph that displaced it.
const (
	resolveErrNotFound  = "not_found"
	resolveErrAmbiguous = "ambiguous"
	resolveErrConflict  = "conflict"
)

// HandleResolveBookWorkBatch implements POST /v1/books/resolve: the same
// join as POST /v1/books/{id}/resolve, for many books in one request.
//
// It exists for one moment in a device's life. A phone signing in to an
// account has no name on this server for any book it holds, and a book
// with no name can neither send a position nor receive one — so a fresh
// device with a few hundred catalog books needed a few hundred requests
// before it could sync at all, which is why clients ration them and why
// the shelf arrived in batches over several refreshes.
//
// A book is resolved in its own transaction, as it is on the single
// route, so one book the server cannot place settles nothing about the
// others: it takes its refusal in its own entry and the rest of the
// batch stands. That is also why the response is 200 with per-item
// results rather than a status code — there is no single status that
// could describe five hundred answers, and a batch-wide failure would
// throw away the ones that worked.
//
// What is *not* per item is a server that has stopped working. A
// database that has gone away would otherwise be reported as a
// permanent refusal of each book in turn, which a client cannot tell
// from "this book will never resolve" — so it fails the request and
// gets retried.
func (s *Server) HandleResolveBookWorkBatch(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req catalogResolveBatchRequest
	if decodeBatch(w, r, s.Cfg.Ops.MaxBodyBytes, &req) {
		return
	}
	if len(req.BookIDs) == 0 {
		writeError(w, http.StatusBadRequest, "book_ids required")
		return
	}
	if len(req.BookIDs) > maxResolveBatch {
		writeBatchTooLarge(w, maxResolveBatch)
		return
	}

	at := time.Now()
	results := make([]catalogResolveItemJSON, 0, len(req.BookIDs))
	for _, bookID := range req.BookIDs {
		item, ok := s.resolveBatchItem(r.Context(), tok.UserID, bookID, req.Confirmed, at)
		if !ok {
			writeError(w, http.StatusInternalServerError, "resolve failed")
			return
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// resolveBatchItem resolves one book of a batch. It reports false only
// for a failure that is the server's rather than the book's, which ends
// the whole request.
func (s *Server) resolveBatchItem(
	ctx context.Context, userID, bookID string, confirmed bool, at time.Time,
) (catalogResolveItemJSON, bool) {
	result, ids, err := s.resolveBookWork(ctx, userID, bookID, confirmed, at)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		return catalogResolveItemJSON{BookID: bookID, Error: resolveErrNotFound}, true
	case errors.Is(err, store.ErrConflict):
		return catalogResolveItemJSON{BookID: bookID, Error: resolveErrConflict}, true
	default:
		return catalogResolveItemJSON{}, false
	}
	if len(result.ConflictingWorkIDs) > 0 {
		return catalogResolveItemJSON{
			BookID: bookID, Error: resolveErrAmbiguous, Works: result.ConflictingWorkIDs,
		}, true
	}
	// A doubtful match is not a refusal here any more than it is on the
	// single route: the work it would land in comes back named, marked
	// `low`, and nothing is committed until a reader confirms it.
	return catalogResolveItemJSON{
		BookID: bookID, WorkID: result.WorkID,
		Confidence: result.Confidence, Created: result.Created,
		Identifiers: identifiersJSON(ids),
	}, true
}

func identifiersJSON(ids []store.Identifier) []identifierJSON {
	out := make([]identifierJSON, 0, len(ids))
	for _, id := range ids {
		out = append(out, identifierJSON{Kind: id.Kind, Value: id.Value})
	}
	return out
}
