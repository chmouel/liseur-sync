package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/catalog"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/metadata/provider"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandleBookMetadataLookup implements
// POST /v1/books/{id}/metadata/lookup — ask the configured external
// services about this book and report what they said.
//
// Nothing is written. The response is candidates with the service that
// offered each one named, and applying one is an ordinary metadata edit
// through PUT /v1/books/{id}/metadata, gated on the revision the caller
// read like every other edit. That split is the ADR's rule about
// external data made structural: there is no code path from a lookup to
// the catalog, so there is nothing to get wrong.
//
// It needs library-manage rather than library-read, for two reasons that
// point the same way: the result is only useful to somebody who could
// apply it, and a lookup makes this server talk to a third party about a
// book, which is not a thing a read-only credential should be able to
// cause.
func (s *Server) HandleBookMetadataLookup(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	candidates, err := s.LookupBookMetadata(r.Context(), tok.UserID, r.PathValue("id"))
	switch {
	case errors.Is(err, provider.ErrDisabled):
		// A precise refusal, because "off" and "found nothing" are
		// different problems and only one of them is fixed by editing
		// the configuration.
		writeError(w, http.StatusNotImplemented,
			"external metadata lookup is not enabled on this server")
		return
	case errors.Is(err, ErrLookupRateLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many lookups; try again shortly")
		return
	case errors.Is(err, errCatalogLookupFailed):
		// Every service failing is the external world's problem, not the
		// caller's, and 502 says so: nothing about the request was
		// wrong and retrying later is the right move.
		writeError(w, http.StatusBadGateway, "no metadata service could be reached")
		return
	case err != nil:
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, lookupResponse(s.Providers.Names(), candidates))
}

// Errors LookupBookMetadata reports that are about the lookup rather
// than about the book.
var (
	// ErrLookupRateLimited reports that this user has asked too often.
	ErrLookupRateLimited = errors.New("api: too many metadata lookups")
	// errCatalogLookupFailed reports that no provider could be reached.
	errCatalogLookupFailed = errors.New("api: no metadata service could be reached")
)

// LookupBookMetadata asks the configured services about one book the
// caller may manage, and returns what they said.
//
// It is exported so the web UI can offer the same thing without going
// through HTTP, the way uploads and downloads already do: one
// implementation of who may ask, how often, and what is asked.
func (s *Server) LookupBookMetadata(
	ctx context.Context, userID, bookID string,
) ([]provider.Candidate, error) {
	if !s.Providers.Enabled() {
		return nil, provider.ErrDisabled
	}
	if s.LookupLimiter != nil && !s.LookupLimiter.Allow(userID) {
		return nil, ErrLookupRateLimited
	}

	// The query is built from a book the caller can already manage, not
	// from anything in the request. A lookup can therefore only ever ask
	// about a book they can already read, and nothing a client sends
	// reaches a provider except through the catalog.
	meta, err := s.St.CatalogBookMetadata(ctx, userID, bookID, store.LibraryRoleManage)
	if err != nil {
		return nil, err
	}

	query := provider.Query{Title: meta.Book.Title}
	for _, contributor := range meta.Contributors {
		if contributor.Role == "author" {
			query.Author = contributor.Name
			break
		}
	}
	for _, identifier := range meta.Identifiers {
		query.Identifiers = append(query.Identifiers, metadata.IdentifierKey{
			Scheme: identifier.Scheme, Value: identifier.Value,
		})
	}

	candidates, err := s.Providers.Lookup(ctx, query)
	if err != nil {
		if errors.Is(err, provider.ErrDisabled) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errCatalogLookupFailed, err)
	}
	return candidates, nil
}

type lookupCandidateJSON struct {
	Provider      string             `json:"provider"`
	URL           string             `json:"url,omitempty"`
	Score         float64            `json:"score"`
	ByIdentifier  bool               `json:"by_identifier"`
	Title         string             `json:"title,omitempty"`
	Subtitle      string             `json:"subtitle,omitempty"`
	Description   string             `json:"description,omitempty"`
	Publisher     string             `json:"publisher,omitempty"`
	PublishedDate string             `json:"published_date,omitempty"`
	Languages     []string           `json:"languages,omitempty"`
	Tags          []string           `json:"tags,omitempty"`
	Authors       []string           `json:"authors,omitempty"`
	CoverURL      string             `json:"cover_url,omitempty"`
	Identifiers   []identifierJSONed `json:"identifiers,omitempty"`
}

type identifierJSONed struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

func lookupResponse(names []string, candidates []provider.Candidate) map[string]any {
	out := make([]lookupCandidateJSON, 0, len(candidates))
	for _, c := range candidates {
		item := lookupCandidateJSON{
			Provider:      c.Provider,
			URL:           c.URL,
			Score:         c.Score,
			ByIdentifier:  c.ByIdentifier,
			Title:         c.Title,
			Subtitle:      c.Subtitle,
			Description:   c.Description,
			Publisher:     c.Publisher,
			PublishedDate: c.PublishedDate,
			Languages:     c.Languages,
			Tags:          c.Tags,
			CoverURL:      c.CoverURL,
		}
		for _, contributor := range c.Contributors {
			item.Authors = append(item.Authors, contributor.Name)
		}
		// Identifiers are reported so a person can check they are being
		// offered the right edition, and are not applicable through the
		// edit route: they decide work identity (ADR-0003).
		for _, identifier := range c.Identifiers {
			item.Identifiers = append(item.Identifiers, identifierJSONed{
				Scheme: identifier.Scheme, Value: identifier.Value,
			})
		}
		out = append(out, item)
	}
	return map[string]any{"providers": names, "candidates": out}
}

// applyCandidateRequest is what a client sends back to accept one
// candidate.
//
// The candidate travels through the client rather than being looked up
// again server-side, because a second lookup can return something else:
// a person would then accept what they read and get what they did not.
// Trusting the round-trip costs nothing, because these values are
// written with external provenance, which ranks below a manual edit and
// below a lock — every one of them is something the caller could already
// have typed into the edit form, since applying needs the same
// library-manage capability that form does.
type applyCandidateRequest struct {
	Revision int64 `json:"revision"`
	// Provider is recorded for the person reading the page later, so a
	// value can be traced back to the service that supplied it.
	Provider      string   `json:"provider"`
	ByIdentifier  bool     `json:"by_identifier"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle"`
	Description   string   `json:"description"`
	Publisher     string   `json:"publisher"`
	PublishedDate string   `json:"published_date"`
	Languages     []string `json:"languages"`
	Tags          []string `json:"tags"`
	Authors       []string `json:"authors"`
}

// candidate rebuilds the provider candidate a request describes.
func (req applyCandidateRequest) candidate() provider.Candidate {
	c := provider.Candidate{
		Provider:      req.Provider,
		ByIdentifier:  req.ByIdentifier,
		Title:         req.Title,
		Subtitle:      req.Subtitle,
		Description:   req.Description,
		Publisher:     req.Publisher,
		PublishedDate: req.PublishedDate,
		Languages:     req.Languages,
		Tags:          req.Tags,
	}
	for _, name := range req.Authors {
		c.Contributors = append(c.Contributors,
			metadata.ContributorKey{Name: name, Role: "author"})
	}
	return c
}

// HandleApplyBookMetadataCandidate implements
// POST /v1/books/{id}/metadata/apply — accept one candidate.
//
// Accepting is a separate act from looking up, and it goes through the
// same precedence engine every other source does. That is what makes
// "shown rather than applied" true in the code and not only in the
// prose: a lookup has no path to the catalog, and an apply is an
// ordinary write that a lock still refuses.
func (s *Server) HandleApplyBookMetadataCandidate(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req applyCandidateRequest
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, maxMetadataBodyBytes),
	).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	// The revision is required, for the same reason it is on an edit:
	// two people tidying one shelf is ordinary, and accepting a
	// candidate against a book that has moved on is a silent overwrite.
	if req.Revision < 1 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}
	updated, err := s.ApplyMetadataCandidate(
		r.Context(), tok.UserID, r.PathValue("id"), req.candidate(), req.Revision)
	switch {
	case errors.Is(err, errNothingToApply):
		writeError(w, http.StatusConflict,
			"that candidate would not change anything, or every field it "+
				"offers is locked")
		return
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, http.StatusConflict,
			"somebody else changed this book while you were looking")
		return
	case err != nil:
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, bookMetadataJSON(updated))
}

// errNothingToApply reports a candidate that changed nothing — every
// field it offered was blank, already right, or locked.
var errNothingToApply = errors.New("api: the candidate changed nothing")

// ApplyMetadataCandidate merges one candidate into a book. Exported for
// the web UI, which offers the same operation through a form.
func (s *Server) ApplyMetadataCandidate(
	ctx context.Context, userID, bookID string,
	candidate provider.Candidate, expectedRevision int64,
) (store.BookMetadata, error) {
	current, err := s.St.CatalogBookMetadata(ctx, userID, bookID, store.LibraryRoleManage)
	if err != nil {
		return store.BookMetadata{}, err
	}
	// The revision the caller read is checked before the merge as well
	// as by the store, so a stale accept is refused rather than being
	// merged against a book that has since moved on.
	if expectedRevision != 0 && expectedRevision != current.Book.Revision {
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	next, changed := catalog.Resolve(current, candidate.Proposal())
	if !changed {
		return store.BookMetadata{}, errNothingToApply
	}
	request := store.ApplyBookMetadataRequest{
		Metadata:         next,
		ExpectedRevision: current.Book.Revision,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := store.ValidateApplyBookMetadata(request); err != nil {
		return store.BookMetadata{}, err
	}
	return s.St.ApplyCatalogBookMetadata(ctx, userID, request)
}
