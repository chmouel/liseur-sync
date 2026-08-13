package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
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
	meta, err := s.St.CatalogBookMetadata(r.Context(), tok.UserID, bookID, store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	files, err := s.St.ListBookFiles(r.Context(), tok.UserID, bookID, store.LibraryRoleRead)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "book lookup failed")
		return
	}

	ids := catalogBookIdentifiers(meta, files)
	workID, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	proposed := store.Work{
		ID: workID, UserID: tok.UserID,
		Title: meta.Book.Title, Author: firstAuthor(meta), CreatedAt: time.Now(),
	}
	var editions []store.Edition
	for _, id := range ids {
		if id.Kind == "sha256" {
			editions = append(editions, store.Edition{
				UserID: tok.UserID, SHA256: id.Value, WorkID: workID,
			})
		}
	}

	result, err := s.St.ResolveCatalogBookWork(r.Context(), tok.UserID, bookID,
		proposed, editions, ids, req.Confirmed, time.Now())
	if err != nil {
		// The metadata read above already refused a book this caller
		// cannot see, so reaching this means the book was trashed or
		// purged in between. A race is still a 404, not a 500.
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

// catalogBookIdentifiers is the evidence the catalog holds about one book,
// strongest first. Only available files contribute: a file whose blob is
// missing cannot vouch for a digest, and registering an alias from it would
// attach the reader's work graph to bytes nobody can produce.
//
// The stable "source:liseur-sync:<book_id>" alias is not added here. The
// store appends it inside the resolution transaction, so it is present even
// for a book with no files at all.
func catalogBookIdentifiers(meta store.BookMetadata, files []store.BookFile) []store.Identifier {
	var ids []store.Identifier
	// Duplicates are not filtered here: orderIdentifiers, which every
	// return path goes through, already collapses them. Empty values are,
	// because nothing downstream does and "sha256:" would alias every
	// book whose digest the catalog happens not to know.
	add := func(kind, value string) {
		if value == "" {
			return
		}
		ids = append(ids, store.Identifier{Kind: kind, Value: value})
	}
	for _, f := range files {
		if f.Availability != store.BookFileAvailable {
			continue
		}
		add("sha256", f.BlobSHA256)
		if f.PartialMD5 != nil {
			add("partial-md5", *f.PartialMD5)
		}
		if f.DCIdentifier != nil {
			add("dc", *f.DCIdentifier)
		}
	}
	// Catalogued identifiers are richer than the one the ingest recorded
	// on the file: a librarian may have corrected the ISBN.
	for _, id := range meta.Identifiers {
		add("dc", id.Value)
	}
	if fingerprint := titleAuthorFingerprint(meta); fingerprint != "" {
		add("ta", fingerprint)
	}
	return orderIdentifiers(ids)
}

// titleAuthorFingerprint is the fuzzy fallback alias. It must fold exactly
// the way a client's does or the two never meet, so it reuses the same
// normalization the catalog uses to match contributor names.
func titleAuthorFingerprint(meta store.BookMetadata) string {
	title := metadata.NormalizeName(meta.Book.Title)
	if title == "" {
		return ""
	}
	return title + "|" + metadata.NormalizeName(firstAuthor(meta))
}

// firstAuthor is the book's primary author: contributors come back ordered,
// so the first author role is the one a reader would name.
func firstAuthor(meta store.BookMetadata) string {
	for _, c := range meta.Contributors {
		if c.Role == "author" {
			return c.Name
		}
	}
	return ""
}

func identifiersJSON(ids []store.Identifier) []identifierJSON {
	out := make([]identifierJSON, 0, len(ids))
	for _, id := range ids {
		out = append(out, identifierJSON{Kind: id.Kind, Value: id.Value})
	}
	return out
}
