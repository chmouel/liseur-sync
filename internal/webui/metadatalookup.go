package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/chmouel/liseur-sync/internal/metadata/provider"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Lookup asks external metadata services about a book, and accepts one
// of their answers. It is an interface for the same reason uploads and
// downloads are: the rules about who may ask, how often, and what is
// asked live in one place, and the UI is another caller of them rather
// than a second implementation.
//
// A nil Lookup is the default posture. The page then does not offer to
// contact anybody, which is what an operator who configured nothing
// should see — an absent button rather than one that fails.
type Lookup interface {
	LookupBookMetadata(ctx context.Context, userID, bookID string) ([]provider.Candidate, error)
	ApplyMetadataCandidate(
		ctx context.Context, userID, bookID string,
		candidate provider.Candidate, expectedRevision int64,
	) (store.BookMetadata, error)
}

// CandidateView is one suggestion as the page shows it.
//
// Everything a person needs to judge it is on the card: which service
// said it, whether the service was answering about this book's ISBN or
// about a book with a similar title, and a link to the record so they
// can go and look. A suggestion that cannot be checked is one somebody
// accepts on faith.
type CandidateView struct {
	Provider     string
	URL          string
	ByIdentifier bool
	Title        string
	Subtitle     string
	Description  string
	Publisher    string
	Published    string
	Authors      string
	Tags         string
	Languages    string
	// Payload is the candidate as JSON, carried in the form so that
	// accepting it applies what was on screen. Looking again could
	// return something else, and a person would then accept what they
	// read and get what they did not.
	Payload string
}

// LookupView is the panel under the edit form.
type LookupView struct {
	// Offered is false when no provider is configured. The panel is then
	// absent rather than disabled: an operator who turned nothing on has
	// no reason to see a feature they did not ask for.
	Offered bool
	Ran     bool
	// Revision is text because it is going into a form. Formatting it
	// here keeps the template free of anything but display.
	Revision   string
	Candidates []CandidateView
	Problem    string
}

// handleBookMetadataLookup asks the configured services about a book and
// renders what they said.
//
// It is a POST with a CSRF token even though it writes nothing to the
// catalog, because it does have an effect: it makes this server send a
// request to a third party about one of the operator's books. A GET
// would let any page on the internet cause that by linking to it.
func (s *Server) handleBookMetadataLookup(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	bookID := r.PathValue("id")
	if !s.checkCSRF(r, a) {
		s.bookResult(w, r, bookID, "", "that form had expired; try again")
		return
	}
	if s.Lookup == nil {
		s.bookResult(w, r, bookID, "",
			"this server is not set up to ask anybody about books")
		return
	}
	candidates, err := s.Lookup.LookupBookMetadata(r.Context(), u.ID, bookID)
	if err != nil {
		s.bookResult(w, r, bookID, "", lookupProblem(err))
		return
	}
	if len(candidates) == 0 {
		s.bookResult(w, r, bookID, "nobody had anything to say about this book", "")
		return
	}
	// The candidates are rendered straight away rather than redirected
	// to, because there is nowhere to redirect: they are not stored, and
	// storing them would be a small copy of somebody else's database
	// kept for a page nobody may revisit.
	s.renderBookWithCandidates(w, r, a, u, bookID, candidates)
}

// renderBookWithCandidates draws the book page with the suggestions on
// it. This is the one page in the UI that is answered directly rather
// than redirected to, because the suggestions exist only for as long as
// they are on screen.
func (s *Server) renderBookWithCandidates(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	bookID string, candidates []provider.Candidate,
) {
	v, ok := s.bookView(r, u, bookID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.Lookup.Ran = true
	v.Lookup.Candidates = candidateViews(candidates)
	bookPage(relPrefix(r.URL.Path), userCtx{User: u}, csrfFor(a), v).
		Render(r.Context(), w)
}

// lookupProblem says what went wrong in words that point at the fix.
func lookupProblem(err error) string {
	switch {
	case errors.Is(err, provider.ErrDisabled):
		return "this server is not set up to ask anybody about books"
	case strings.Contains(err.Error(), "too many"):
		return "that is a lot of lookups; give the service a minute"
	default:
		return "no metadata service could be reached"
	}
}

// handleApplyBookMetadataCandidate accepts one suggestion.
//
// The candidate comes back from the form rather than being looked up
// again, so what is written is what was on the screen. It costs nothing
// to trust: these values are written with external provenance, and every
// one of them is something this same person could have typed into the
// edit form beside it.
func (s *Server) handleApplyBookMetadataCandidate(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	bookID := r.PathValue("id")
	if !s.checkCSRF(r, a) {
		s.bookResult(w, r, bookID, "", "that form had expired; try again")
		return
	}
	if s.Lookup == nil {
		s.bookResult(w, r, bookID, "", "this server is not set up to ask anybody about books")
		return
	}
	var candidate provider.Candidate
	if err := json.Unmarshal([]byte(r.FormValue("candidate")), &candidate); err != nil {
		s.bookResult(w, r, bookID, "", "that suggestion could not be read")
		return
	}
	revision, err := parseRevision(r.FormValue("revision"))
	if err != nil {
		s.bookResult(w, r, bookID, "", "that form had expired; try again")
		return
	}
	if _, err := s.Lookup.ApplyMetadataCandidate(
		r.Context(), u.ID, bookID, candidate, revision,
	); err != nil {
		if errors.Is(err, store.ErrStaleRevision) {
			s.bookResult(w, r, bookID, "",
				"somebody else changed this book while you were looking; "+
					"reload to see their version")
			return
		}
		s.bookResult(w, r, bookID, "",
			"nothing was taken from that suggestion: every field it offers is "+
				"either locked, blank, or already what the book says")
		return
	}
	s.bookResult(w, r, bookID, "saved what that suggestion offered", "")
}

func parseRevision(raw string) (int64, error) {
	var revision int64
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &revision); err != nil {
		return 0, err
	}
	if revision < 1 {
		return 0, errors.New("webui: revision is required")
	}
	return revision, nil
}

// candidateViews renders candidates for the page, carrying each one back
// as JSON so accepting applies exactly what was shown.
func candidateViews(candidates []provider.Candidate) []CandidateView {
	out := make([]CandidateView, 0, len(candidates))
	for _, c := range candidates {
		view := CandidateView{
			Provider:     c.Provider,
			URL:          c.URL,
			ByIdentifier: c.ByIdentifier,
			Title:        c.Title,
			Subtitle:     c.Subtitle,
			Description:  c.Description,
			Publisher:    c.Publisher,
			Published:    c.PublishedDate,
			Tags:         strings.Join(c.Tags, ", "),
			Languages:    strings.Join(c.Languages, ", "),
		}
		names := make([]string, 0, len(c.Contributors))
		for _, contributor := range c.Contributors {
			names = append(names, contributor.Name)
		}
		view.Authors = strings.Join(names, ", ")
		if payload, err := json.Marshal(c); err == nil {
			view.Payload = string(payload)
		}
		out = append(out, view)
	}
	return out
}
