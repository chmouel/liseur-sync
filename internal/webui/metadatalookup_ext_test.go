package webui_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/metadata/provider"
	"github.com/chmouel/liseur-sync/internal/store"
)

// stubLookup answers as an external service would, without one. What is
// being tested here is the page, not the network: whether a librarian is
// shown enough to judge a suggestion, and whether accepting one applies
// what they read.
type stubLookup struct {
	candidates []provider.Candidate
	err        error
	applied    *provider.Candidate
	revision   int64
	applyErr   error
}

func (s *stubLookup) LookupBookMetadata(
	context.Context, string, string,
) ([]provider.Candidate, error) {
	return s.candidates, s.err
}

func (s *stubLookup) ApplyMetadataCandidate(
	_ context.Context, _, _ string, c provider.Candidate, revision int64,
) (store.BookMetadata, error) {
	s.applied = &c
	s.revision = revision
	return store.BookMetadata{}, s.applyErr
}

// TestABookPageOffersToAskElsewhereOnlyWhenSomebodySaidItMay.
//
// A server that was told to contact nobody shows no button at all,
// rather than one that fails when pressed. The difference matters
// because the absent button is the honest description of a server whose
// operator configured nothing.
func TestABookPageOffersToAskElsewhereOnlyWhenSomebodySaidItMay(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "asking")

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	if strings.Contains(html, "Ask elsewhere") {
		t.Error("a server with no providers offered to contact one")
	}

	f.ui.Lookup = &stubLookup{}
	_, html = f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(html, "Ask elsewhere") {
		t.Error("a configured server did not offer the lookup")
	}
}

// TestLookupShowsWhoSaidWhatBeforeAnythingIsAccepted. A suggestion
// nobody can attribute or check is one somebody accepts on faith, so the
// service, the reason it matched, and a link to its record are all on
// the card.
func TestLookupShowsWhoSaidWhatBeforeAnythingIsAccepted(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "shown")
	f.ui.Lookup = &stubLookup{candidates: []provider.Candidate{{
		Provider: "openlibrary", URL: "https://openlibrary.org/works/OL1W",
		Title: "Moby-Dick", Subtitle: "or, The Whale", Publisher: "Harper",
		Tags: []string{"Whaling"},
		Contributors: []metadata.ContributorKey{
			{Name: "Herman Melville", Role: "author"},
		},
	}}}

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	resp, page := postPage(t, f, "/ui/books/"+bookID+"/metadata/lookup", f.cookie,
		url.Values{"csrf": {csrfFrom(t, html)}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup: %d", resp.StatusCode)
	}
	for _, want := range []string{
		"openlibrary", "openlibrary.org/works/OL1W", "Moby-Dick",
		"Herman Melville", "Harper", "Whaling", "matched on a similar title",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the candidate did not show %q:\n%s", want, page)
		}
	}
	// The catalog is untouched until somebody presses Use this.
	md, err := f.st.CatalogBookMetadata(
		t.Context(), "u1", bookID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if md.Book.Publisher == "Harper" {
		t.Error("looking a book up changed it")
	}
}

// TestAcceptingSendsBackTheAnswerThatWasOnScreen. A second lookup could
// return something else, and a person would then accept what they read
// and get what they did not.
func TestAcceptingSendsBackTheAnswerThatWasOnScreen(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "accepted")
	stub := &stubLookup{candidates: []provider.Candidate{{
		Provider: "openlibrary", Title: "Moby-Dick", Publisher: "Harper",
	}}}
	f.ui.Lookup = stub

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	csrf := csrfFrom(t, html)
	_, page := postPage(t, f, "/ui/books/"+bookID+"/metadata/lookup",
		f.cookie, url.Values{"csrf": {csrf}})

	candidate := formValue(t, page, "candidate")
	revision := formValue(t, page, "revision")
	resp := f.postForm(t, "/ui/books/"+bookID+"/metadata/apply", f.cookie,
		url.Values{"csrf": {csrf}, "candidate": {candidate}, "revision": {revision}})
	if resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("apply: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if stub.applied == nil || stub.applied.Publisher != "Harper" {
		t.Errorf("what was accepted is not what was shown: %+v", stub.applied)
	}
	// The revision travels with it, so accepting against a book somebody
	// else has since edited is refused rather than silently overwriting.
	if stub.revision == 0 {
		t.Error("accepting did not carry the revision it was looking at")
	}
}

// TestNeitherLookingNorAcceptingWorksWithoutTheFormsToken. Looking a
// book up is a mutation of the outside world even though it writes
// nothing here: it makes this server send a request to a third party
// about somebody's book, which no other site may cause.
func TestNeitherLookingNorAcceptingWorksWithoutTheFormsToken(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "forged")
	stub := &stubLookup{candidates: []provider.Candidate{{
		Provider: "openlibrary", Title: "Moby-Dick",
	}}}
	f.ui.Lookup = stub

	for _, path := range []string{"lookup", "apply"} {
		resp := f.postForm(t, "/ui/books/"+bookID+"/metadata/"+path, f.cookie,
			url.Values{"csrf": {"not-the-token"}, "candidate": {`{"title":"x"}`}})
		if resp.StatusCode != http.StatusSeeOther ||
			!strings.Contains(resp.Header.Get("Location"), "problem=") {
			t.Errorf("%s without a token: %d %s",
				path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if stub.applied != nil {
		t.Error("a forged form applied a candidate")
	}
}

// TestAServiceThatCannotBeReachedSaysSoInsteadOfFailing. The librarian
// is on a page about their own book; somebody else's outage should read
// as a sentence, not as an error page.
func TestAServiceThatCannotBeReachedSaysSoInsteadOfFailing(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "offline")
	f.ui.Lookup = &stubLookup{err: errors.New("dial tcp: no route to host")}

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	resp := f.postForm(t, "/ui/books/"+bookID+"/metadata/lookup", f.cookie,
		url.Values{"csrf": {csrfFrom(t, html)}})
	location := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther ||
		!strings.Contains(location, "problem=") {
		t.Fatalf("unreachable service: %d %s", resp.StatusCode, location)
	}

	// Nobody having heard of the book is a different sentence: there is
	// nothing wrong, and there is nothing to accept.
	f.ui.Lookup = &stubLookup{}
	_, html = f.get(t, "/ui/books/"+bookID, f.cookie)
	resp = f.postForm(t, "/ui/books/"+bookID+"/metadata/lookup", f.cookie,
		url.Values{"csrf": {csrfFrom(t, html)}})
	if !strings.Contains(resp.Header.Get("Location"), "notice=") {
		t.Errorf("an empty result read as a failure: %s", resp.Header.Get("Location"))
	}
}

// postPage posts a form and keeps the page that comes back. The
// fixture's postForm closes the body, because almost every mutation here
// answers with a redirect; the lookup is the one that answers with a
// page.
func postPage(
	t *testing.T, f *booksFixture, path string, cookie *http.Cookie, form url.Values,
) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// formValue pulls one hidden input's value out of the rendered page, so
// the test posts back what the browser would.
func formValue(t *testing.T, html, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("no %s field in the page:\n%s", name, html)
	}
	rest := html[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated %s field", name)
	}
	return unescapeAttribute(rest[:end])
}

// unescapeAttribute reverses templ's attribute escaping, which is what
// the browser does before it sends the value back.
func unescapeAttribute(s string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&quot;", `"`,
		"&#39;", "'", "&#x27;", "'",
	).Replace(s)
}
