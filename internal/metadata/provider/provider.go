package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Query is what is known about the book being looked up. A provider uses
// the identifier when it has one and falls back on the title and author.
//
// The query is composed by the server from a book already in the
// catalog, not accepted from a client, so a lookup can only ever ask
// about a book the caller can already read.
type Query struct {
	Title  string
	Author string
	// Identifiers are the book's own, in the catalog's vocabulary
	// ("isbn", "isbn13", …). An ISBN turns a guess into a lookup, which
	// is why identifiers are searched first.
	Identifiers []metadata.IdentifierKey
}

// isbn returns the first ISBN in the query, digits only.
func (q Query) isbn() string {
	for _, id := range q.Identifiers {
		if !strings.Contains(strings.ToLower(id.Scheme), "isbn") {
			continue
		}
		if cleaned := cleanISBN(id.Value); cleaned != "" {
			return cleaned
		}
	}
	// A publisher who put the ISBN in dc:identifier without a scheme is
	// common enough to be worth recognizing by shape.
	for _, id := range q.Identifiers {
		if cleaned := cleanISBN(id.Value); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanISBN(value string) string {
	var digits strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == 'x' || r == 'X':
			digits.WriteRune('X')
		}
	}
	out := digits.String()
	if len(out) != 10 && len(out) != 13 {
		return ""
	}
	return out
}

// Candidate is one book a provider believes the query described, in the
// same vocabulary the precedence engine consumes.
//
// It is a proposal and nothing more. Nothing here is written to the
// catalog until a person picks it (ADR-0004): a title from a stranger's
// database is a suggestion, and a librarian who did not ask for their
// shelf to be rewritten should not find that it has been.
type Candidate struct {
	// Provider names the service, for attribution in the UI. A candidate
	// that cannot say where it came from cannot be judged.
	Provider string
	// URL is the human-readable page for this record, so somebody can go
	// and look at what they are being offered.
	URL string
	// Score is how well this candidate matched the query, highest first.
	// It orders the list; it never decides anything.
	Score float64
	// ByIdentifier records that the service was asked about an ISBN
	// rather than a title, which is the difference between looking a
	// book up and guessing at it.
	ByIdentifier bool

	Title         string
	Subtitle      string
	Description   string
	Publisher     string
	PublishedDate string
	Languages     []string
	Tags          []string
	Contributors  []metadata.ContributorKey
	Identifiers   []metadata.IdentifierKey
	// CoverURL is left as a URL rather than fetched. Fetching it would
	// mean this server downloading an image from a third party on
	// somebody's behalf, which is a bigger decision than reading a
	// title, and one nothing yet needs.
	CoverURL string
}

// Proposal expresses a candidate in the form the precedence engine takes.
//
// The source is external, which ranks above filename parsing and below a
// manual edit: accepting a candidate is trusting a database more than a
// filename, and it must still lose to somebody who typed the value
// themselves and locked it.
//
// PartialSets is set because a provider only ever sees part of the
// picture. OpenLibrary knowing three tags is not a claim that the two
// the librarian added are wrong, and a complete-set merge would delete
// them.
func (c Candidate) Proposal() metadata.Proposal {
	proposal := metadata.Proposal{
		Source: store.MetadataExternal,
		// A service asked about an ISBN answered about this book; a
		// service asked about a title answered about a book with that
		// title, which is a different claim.
		Confidence:  confidence(c.ByIdentifier),
		PartialSets: true,
		Title: metadata.Candidate{
			Value: c.Title, Source: store.MetadataExternal,
		},
		Subtitle: metadata.Candidate{
			Value: c.Subtitle, Source: store.MetadataExternal,
		},
		Description: metadata.Candidate{
			Value: c.Description, Source: store.MetadataExternal,
		},
		Publisher: metadata.Candidate{
			Value: c.Publisher, Source: store.MetadataExternal,
		},
		PublishedDate: metadata.Candidate{
			Value: c.PublishedDate, Source: store.MetadataExternal,
		},
	}
	for _, language := range c.Languages {
		proposal.Languages = append(proposal.Languages,
			metadata.Assertion[string, string]{
				Key: metadata.NormalizeName(language), Value: language,
			})
	}
	for _, tag := range c.Tags {
		proposal.Tags = append(proposal.Tags,
			metadata.Assertion[string, string]{
				Key: metadata.NormalizeName(tag), Value: tag,
			})
	}
	for _, contributor := range c.Contributors {
		proposal.Contributors = append(proposal.Contributors,
			metadata.Assertion[metadata.ContributorKey, string]{
				Key: metadata.ContributorKey{
					Name: metadata.NormalizeName(contributor.Name),
					Role: contributor.Role,
				},
				Value: contributor.Name,
			})
	}
	// Identifiers are deliberately not proposed. They decide work
	// identity (ADR-0003), so accepting one from an external service
	// would move a reader's history between books as a side effect of
	// tidying a title.
	return proposal
}

func confidence(byIdentifier bool) metadata.Confidence {
	if byIdentifier {
		return metadata.ConfidenceHigh
	}
	return metadata.ConfidenceLow
}

// Provider is one external metadata service.
type Provider interface {
	// Name is the stable identifier used in configuration and reported
	// on every candidate.
	Name() string
	// Hosts are the hostnames this provider may contact. They are fixed
	// in code: an operator chooses whether a provider runs, never where
	// it connects.
	Hosts() []string
	// Lookup returns candidates, best first. A service that knows
	// nothing about the book returns none, which is not an error.
	//
	// The Fetcher is the only network this provider gets, which is what
	// makes the allowlist, the redirect check and the byte budget
	// properties of every provider rather than of each one separately.
	Lookup(ctx context.Context, f *Fetcher, q Query) ([]Candidate, error)
}

// Registry is the set of enabled providers with the Fetcher they share.
// A zero Registry is disabled and answers every lookup with
// ErrDisabled, which is what an operator who configured nothing gets.
type Registry struct {
	providers []Provider
	fetcher   *Fetcher
}

// ErrDisabled reports that no provider is configured. It is a distinct
// error so the UI can say "nobody turned this on" rather than "nothing
// was found", which are different problems with different fixes.
var ErrDisabled = errors.New("provider: external lookup is not enabled")

// ErrUnknownProvider names a provider in the configuration that this
// build does not have. It is refused at startup rather than ignored: an
// operator who typed "openlibary" and got silence would conclude the
// service was down.
var ErrUnknownProvider = errors.New("provider: unknown provider")

// Available lists the providers this build ships, in a stable order.
func Available() []Provider {
	return []Provider{OpenLibrary{}, GoogleBooks{}}
}

// New builds a registry from provider names. An empty list is not an
// error; it is a disabled registry.
func New(names []string, limits Limits) (*Registry, error) {
	if len(names) == 0 {
		return &Registry{}, nil
	}
	byName := map[string]Provider{}
	for _, p := range Available() {
		byName[p.Name()] = p
	}
	var chosen []Provider
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		p, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
		}
		seen[name] = true
		chosen = append(chosen, p)
	}
	if len(chosen) == 0 {
		return &Registry{}, nil
	}
	return NewWithProviders(chosen, limits)
}

// NewWithProviders builds a registry from provider values rather than
// names, so a caller that already has them — a test with a provider that
// answers without a network — does not go through the name table.
//
// The allowlist is still built from what each provider declares, never
// from configuration: adding a host means adding code, which is the
// property the whole check depends on.
func NewWithProviders(providers []Provider, limits Limits) (*Registry, error) {
	if len(providers) == 0 {
		return &Registry{}, nil
	}
	var hosts []string
	for _, p := range providers {
		hosts = append(hosts, p.Hosts()...)
	}
	return &Registry{providers: providers, fetcher: newFetcher(hosts, limits)}, nil
}

// Enabled reports whether any provider is configured.
func (r *Registry) Enabled() bool { return r != nil && len(r.providers) > 0 }

// Names lists the configured providers.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		names = append(names, p.Name())
	}
	return names
}

// Lookup asks every configured provider and returns their candidates
// together, best match first.
//
// One provider failing does not fail the lookup. Two services are
// configured precisely so that one being down still answers the
// question, and an error from one is reported alongside the other's
// results rather than instead of them.
func (r *Registry) Lookup(ctx context.Context, q Query) ([]Candidate, error) {
	if !r.Enabled() {
		return nil, ErrDisabled
	}
	var candidates []Candidate
	var failures []error
	for _, p := range r.providers {
		found, err := p.Lookup(ctx, r.fetcher, q)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		candidates = append(candidates, found...)
	}
	if len(candidates) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	return candidates, nil
}

// score rates a candidate against the query, so the list a person reads
// starts with the book they probably meant.
//
// It is ordering only. A low score is still shown, because a provider
// that spells an author differently is not a provider that found the
// wrong book, and hiding it would leave somebody with an empty list and
// no idea why.
func score(q Query, c Candidate, byIdentifier bool) float64 {
	if byIdentifier {
		return 1 // an ISBN match is not a guess
	}
	total := 0.0
	if q.Title != "" && c.Title != "" {
		total += 0.6 * similar(q.Title, c.Title)
	}
	if q.Author != "" && len(c.Contributors) > 0 {
		best := 0.0
		for _, contributor := range c.Contributors {
			if s := similar(q.Author, contributor.Name); s > best {
				best = s
			}
		}
		total += 0.4 * best
	} else if q.Author == "" && q.Title != "" && c.Title != "" {
		// Nothing to check the author against, so the title carries the
		// whole judgement rather than capping every candidate at 0.6.
		total = similar(q.Title, c.Title)
	}
	return total
}

// similar compares two names by the words in them, using the same
// splitter the catalog's own search uses. Sharing it is what keeps
// "Moby-Dick" and "Moby Dick" one book here as well as there — and
// accents folded, so a library catalogued with them ranks the same as
// one without.
func similar(a, b string) float64 {
	if metadata.NormalizeName(a) == "" || metadata.NormalizeName(b) == "" {
		return 0
	}
	words := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, word := range store.SearchTerms(s) {
			out[word] = true
		}
		return out
	}
	left, right := words(a), words(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for word := range left {
		if right[word] {
			shared++
		}
	}
	if shared == 0 {
		return 0
	}
	return 2 * float64(shared) / float64(len(left)+len(right))
}
