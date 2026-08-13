package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// serve stands up a test server that answers with body and records what
// was asked, so a test can check the query rather than only the parse.
func serve(t *testing.T, body string) (*Fetcher, *url.URL, *url.Values) {
	t.Helper()
	var asked url.Values
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	host := mustHost(t, ts.URL)
	f := testFetcher(t, host, Limits{})
	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return f, &url.URL{Scheme: "https", Host: parsed.Host}, &asked
}

// TestAnISBNIsLookedUpAndATitleIsGuessedAt is the distinction that
// decides how much a candidate should be believed: a service asked about
// an ISBN answered about this book, and a service asked about a title
// answered about a book with that title. The confidence a candidate
// carries has to reflect which question was asked.
func TestAnISBNIsLookedUpAndATitleIsGuessedAt(t *testing.T) {
	const body = `{"docs":[{"key":"/works/OL1W","title":"Moby-Dick",
	  "author_name":["Herman Melville"],"first_publish_year":1851,
	  "publisher":["Harper"],"language":["eng"],"subject":["Whaling","Sea stories"],
	  "cover_i":42}]}`

	f, base, asked := serve(t, body)
	query := Query{
		Title:  "Moby Dick",
		Author: "Melville",
		Identifiers: []metadata.IdentifierKey{
			{Scheme: "uuid", Value: "not-an-isbn"},
			{Scheme: "ISBN-13", Value: "978-0-14-243724-7"},
		},
	}
	got := lookupAt(t, OpenLibrary{}, f, base, "/search.json", query)

	if asked.Get("isbn") != "9780142437247" {
		t.Errorf("the ISBN was not used, or not cleaned: %q", asked.Get("isbn"))
	}
	if asked.Get("title") != "" {
		t.Error("a book with an ISBN was searched by title as well")
	}
	if len(got) != 1 {
		t.Fatalf("want one candidate, got %d", len(got))
	}
	c := got[0]
	if !c.ByIdentifier || c.Score != 1 {
		t.Errorf("an ISBN match must not be scored as a guess: %+v", c)
	}
	if c.Proposal().Confidence != metadata.ConfidenceHigh {
		t.Errorf("confidence: %v", c.Proposal().Confidence)
	}
	if c.Title != "Moby-Dick" || c.Publisher != "Harper" || c.PublishedDate != "1851" {
		t.Errorf("candidate: %+v", c)
	}
	if len(c.Contributors) != 1 || c.Contributors[0].Role != "author" {
		t.Errorf("contributors: %+v", c.Contributors)
	}
	if c.Provider != "openlibrary" || !strings.Contains(c.URL, "/works/OL1W") {
		t.Errorf("a candidate must say where it came from: %+v", c)
	}

	// The same book without an identifier is a title search, and what
	// comes back is a guess however well it matches.
	f, base, asked = serve(t, body)
	got = lookupAt(t, OpenLibrary{}, f, base, "/search.json",
		Query{Title: "Moby Dick", Author: "Herman Melville"})
	if asked.Get("title") != "Moby Dick" || asked.Get("author") != "Herman Melville" {
		t.Errorf("title search: %v", *asked)
	}
	if got[0].ByIdentifier {
		t.Error("a title search reported itself as an identifier match")
	}
	if got[0].Proposal().Confidence != metadata.ConfidenceLow {
		t.Errorf("a guess must be graded as one: %v", got[0].Proposal().Confidence)
	}
	if got[0].Score <= 0.5 {
		t.Errorf("a good title and author match scored %v", got[0].Score)
	}
}

// TestNothingIsAskedWhenThereIsNothingToAskAbout: a book with no title
// and no identifier is not a query, and sending one would be this server
// telling a third party about a book it cannot even name.
func TestNothingIsAskedWhenThereIsNothingToAskAbout(t *testing.T) {
	for _, p := range Available() {
		f, base, asked := serve(t, `{}`)
		got := lookupAt(t, p, f, base, "", Query{})
		if len(got) != 0 {
			t.Errorf("%s: returned candidates for an empty query", p.Name())
		}
		if len(*asked) != 0 {
			t.Errorf("%s: contacted the service for an empty query", p.Name())
		}
	}
}

// TestGoogleBooksCandidatesCarryWhatTheyFound.
func TestGoogleBooksCandidatesCarryWhatTheyFound(t *testing.T) {
	f, base, asked := serve(t, `{"items":[{"volumeInfo":{
	  "title":"Moby-Dick","subtitle":"or, The Whale","authors":["Herman Melville"],
	  "publisher":"Harper","publishedDate":"1851-11-14","description":"A whale.",
	  "categories":["Fiction"],"language":"en",
	  "infoLink":"https://books.google.com/x",
	  "industryIdentifiers":[{"type":"ISBN_13","identifier":"9780142437247"}],
	  "imageLinks":{"thumbnail":"http://books.google.com/cover.jpg"}}}]}`)

	got := lookupAt(t, GoogleBooks{}, f, base, "/books/v1/volumes",
		Query{Title: "Moby Dick", Author: "Melville"})
	if len(got) != 1 {
		t.Fatalf("want one candidate, got %d", len(got))
	}
	c := got[0]
	if !strings.Contains(asked.Get("q"), `intitle:"Moby Dick"`) ||
		!strings.Contains(asked.Get("q"), `inauthor:"Melville"`) {
		t.Errorf("query: %q", asked.Get("q"))
	}
	if c.Subtitle != "or, The Whale" || c.Description != "A whale." {
		t.Errorf("candidate: %+v", c)
	}
	// A cover the page cannot load is a broken image, not a cover.
	if !strings.HasPrefix(c.CoverURL, "https://") {
		t.Errorf("cover URL was left insecure: %q", c.CoverURL)
	}

	// A quote in a title must not be able to reach the query language.
	f, base, asked = serve(t, `{"items":[]}`)
	lookupAt(t, GoogleBooks{}, f, base, "/books/v1/volumes",
		Query{Title: `Moby" OR inauthor:"x`})
	if strings.Count(asked.Get("q"), `"`) != 2 {
		t.Errorf("a quote survived into the query: %q", asked.Get("q"))
	}
}

// TestACandidateIsAProposalNotAWrite is the ADR's rule about what an
// external service is allowed to do to a library, expressed where it is
// enforced: in the shape of what a candidate turns into.
func TestACandidateIsAProposalNotAWrite(t *testing.T) {
	c := Candidate{
		Provider:     "openlibrary",
		Title:        "Moby-Dick",
		Tags:         []string{"Whaling"},
		Languages:    []string{"eng"},
		Contributors: []metadata.ContributorKey{{Name: "Herman Melville", Role: "author"}},
		Identifiers:  []metadata.IdentifierKey{{Scheme: "isbn_13", Value: "9780142437247"}},
	}
	p := c.Proposal()

	if p.Source != store.MetadataExternal {
		t.Errorf("source: %v", p.Source)
	}
	// External ranks above a filename and below a person, so accepting a
	// candidate never overrules somebody who typed the value themselves.
	if metadata.Rank(store.MetadataFilename) >= metadata.Rank(p.Source) ||
		metadata.Rank(p.Source) >= metadata.Rank(store.MetadataManual) {
		t.Error("external must sit between filename parsing and a manual edit")
	}

	// A provider sees part of the picture. Merging its tags as a
	// complete set would delete the ones the librarian added.
	if !p.PartialSets {
		t.Error("a provider's sets must be merged as partial")
	}

	// Identifiers decide work identity (ADR-0003): accepting one would
	// move a reader's history between books as a side effect of tidying
	// a title.
	if len(p.Identifiers) != 0 {
		t.Errorf("a provider must not propose identifiers: %+v", p.Identifiers)
	}

	if len(p.Tags) != 1 || p.Tags[0].Value != "Whaling" {
		t.Errorf("tags: %+v", p.Tags)
	}
	if len(p.Contributors) != 1 || p.Contributors[0].Value != "Herman Melville" {
		t.Errorf("contributors: %+v", p.Contributors)
	}
}

// TestOneProviderBeingDownStillAnswers. Two services are configured so
// that one being unreachable still answers the question; reporting the
// failure instead of the other's results would make the second provider
// pointless.
func TestOneProviderBeingDownStillAnswers(t *testing.T) {
	f, base, _ := serve(t, `{"docs":[{"key":"/works/OL1W","title":"Moby-Dick"}]}`)
	f.rewrite = func(target *url.URL) *url.URL {
		out := *target
		out.Host = base.Host
		return &out
	}
	f.allowed["openlibrary.org"] = true
	registry := &Registry{fetcher: f, providers: []Provider{OpenLibrary{}, broken{}}}
	got, err := registry.Lookup(t.Context(), Query{Title: "Moby-Dick"})
	if err != nil {
		t.Fatalf("one provider failing failed the lookup: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want the working provider's candidate, got %d", len(got))
	}

	// Every provider failing is a failure, and it names them, because
	// "no results" and "nothing worked" need different fixes.
	registry = &Registry{fetcher: f, providers: []Provider{broken{}}}
	if _, err := registry.Lookup(t.Context(), Query{Title: "x"}); err == nil ||
		!strings.Contains(err.Error(), "broken") {
		t.Errorf("a total failure was reported as an empty result: %v", err)
	}
}

// TestTheImageShipsTrustRoots is the smoke test ADR-0004 asked for by
// name. Every provider is reached over TLS verified against the system
// trust store, so an image built without CA certificates fails every
// lookup — and fails at the only moment anybody would notice, which is
// in production.
//
// It checks the trust store, not the network: there is no outbound
// connection here, so it is safe to run in CI and on a laptop on a
// train.
func TestTheImageShipsTrustRoots(t *testing.T) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		t.Fatalf("no system trust store, so every provider lookup will fail: %v", err)
	}
	if len(pool.Subjects()) == 0 { //nolint:staticcheck // the count is the point
		t.Fatal("the system trust store is empty; TLS to any provider will fail")
	}

	// The Fetcher must not have been handed a configuration that skips
	// verification. This is cheap to assert and expensive to discover
	// later.
	f := newFetcher([]string{"openlibrary.org"}, Limits{})
	tlsConfig := f.client.Transport.(*http.Transport).TLSClientConfig
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		t.Fatal("provider TLS verification is disabled")
	}
}

// --- helpers -------------------------------------------------------

// lookupAt runs a real provider against a local server, by letting the
// Fetcher redirect the URL the provider composed. The provider still
// builds its own URL and the allowlist still passes on the real
// hostname, which is what makes the test about the provider rather than
// about a base URL nothing in production supplies.
func lookupAt(
	t *testing.T, p Provider, f *Fetcher, base *url.URL, wantPath string, q Query,
) []Candidate {
	t.Helper()
	f.rewrite = func(target *url.URL) *url.URL {
		if wantPath != "" && target.Path != wantPath {
			t.Errorf("%s asked for %q, want %q", p.Name(), target.Path, wantPath)
		}
		if !f.allowed[strings.ToLower(target.Hostname())] {
			t.Errorf("%s composed a URL outside its own allowlist: %s", p.Name(), target)
		}
		out := *target
		out.Host = base.Host
		return &out
	}
	// The provider's real host has to be on the list, or permit refuses
	// before the rewrite ever runs.
	for _, host := range p.Hosts() {
		f.allowed[strings.ToLower(host)] = true
	}
	got, err := p.Lookup(t.Context(), f, q)
	if err != nil {
		t.Fatalf("%s: %v", p.Name(), err)
	}
	return got
}

// broken stands in for a provider whose service is down.
type broken struct{}

func (broken) Name() string    { return "broken" }
func (broken) Hosts() []string { return []string{"broken.example"} }
func (broken) Lookup(context.Context, *Fetcher, Query) ([]Candidate, error) {
	return nil, errors.New("unreachable")
}
