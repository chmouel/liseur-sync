//go:build linux

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"

	"github.com/chmouel/liseur-sync/internal/metadata/provider"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TestLookupIsRefusedOnAServerThatWasNotToldToTalkToAnybody.
//
// A self-hosted server contacts nobody unless its operator said so, and
// the refusal has to be legible: 501 says the feature is not turned on,
// which is a different problem from an empty result and is fixed
// somewhere else entirely.
func TestLookupIsRefusedOnAServerThatWasNotToldToTalkToAnybody(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "quiet", []byte("bytes"))

	resp, _ := f.send(t, http.MethodPost,
		"/v1/books/"+bookID+"/metadata/lookup", f.token, map[string]any{})
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("a server with no providers answered %d", resp.StatusCode)
	}
}

// stubProvider stands in for an external service without touching the
// network. The fetcher's own behaviour is tested in the provider
// package; what matters here is what the route does with an answer.
type stubProvider struct {
	name       string
	candidates []provider.Candidate
	asked      *provider.Query
}

func (s *stubProvider) Name() string    { return s.name }
func (s *stubProvider) Hosts() []string { return []string{"stub.example"} }

func (s *stubProvider) Lookup(
	_ context.Context, _ *provider.Fetcher, q provider.Query,
) ([]provider.Candidate, error) {
	s.asked = &q
	return s.candidates, nil
}

// enableStub installs a registry whose only provider is the stub. It
// reaches into the package's own internals, which is the point: nothing
// outside this module can supply a provider, so a caller cannot name a
// host.
func enableStub(t *testing.T, f *uploadFixture, candidates []provider.Candidate) *stubProvider {
	t.Helper()
	stub := &stubProvider{name: "stub", candidates: candidates}
	registry, err := provider.NewWithProviders([]provider.Provider{stub}, provider.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.Providers = registry
	return stub
}

// TestALookupAsksAboutTheBookAndNothingElse: the query is composed from
// a book the caller can already manage, so no part of a request reaches
// a third party.
func TestALookupAsksAboutTheBookAndNothingElse(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "moby", "Moby-Dick", []byte("bytes"))
	stub := enableStub(t, f, []provider.Candidate{{
		Provider: "stub", Title: "Moby-Dick", Publisher: "Harper",
		Score: 0.9, URL: "https://stub.example/1",
	}})

	resp, out := f.send(t, http.MethodPost,
		"/v1/books/"+bookID+"/metadata/lookup", f.token, map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup: %d %v", resp.StatusCode, out)
	}
	if stub.asked == nil {
		t.Fatal("no provider was asked anything")
	}
	if stub.asked.Title != "Moby-Dick" {
		t.Errorf("the query did not come from the book: %+v", stub.asked)
	}

	candidates, _ := out["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidates: %v", out["candidates"])
	}
	got, _ := candidates[0].(map[string]any)
	// Attribution is not decoration: a candidate nobody can trace back
	// to a service cannot be judged.
	if got["provider"] != "stub" || got["url"] != "https://stub.example/1" {
		t.Errorf("candidate lost its attribution: %v", got)
	}

	// Nothing was written. This is the ADR's rule, checked where it can
	// actually fail.
	after := f.metadata(t, bookID, f.token)
	if _, source := field(after, "title"); source == string(store.MetadataExternal) {
		t.Error("a lookup wrote to the catalog")
	}
	if value, source := field(after, "publisher"); value == "Harper" ||
		source == string(store.MetadataExternal) {
		t.Errorf("a lookup wrote what a service said: %v", after["publisher"])
	}
}

// field reads one metadata field's value and where it came from. The
// API answers with an object per field because provenance is half the
// point; the tests read it the same way.
func field(body map[string]any, name string) (value, source string) {
	f, _ := body[name].(map[string]any)
	value, _ = f["value"].(string)
	source, _ = f["source"].(string)
	return value, source
}

// TestOnlySomebodyWhoCouldApplyACandidateMayAskForOne. A lookup makes
// this server talk to a third party about a book; a read-only credential
// must not be able to cause that, and the answer is no use to it anyway.
func TestOnlySomebodyWhoCouldApplyACandidateMayAskForOne(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "scoped", []byte("bytes"))
	enableStub(t, f, []provider.Candidate{{Provider: "stub", Title: "x"}})

	readOnly := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, _ := f.send(t, http.MethodPost,
		"/v1/books/"+bookID+"/metadata/lookup", readOnly, map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a read-only token could send the server off-site: %d", resp.StatusCode)
	}

	// Another user's manage token is not access to this book either.
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryManage)
	resp, _ = f.send(t, http.MethodPost,
		"/v1/books/"+bookID+"/metadata/lookup", stranger, map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a stranger looked up somebody else's book: %d", resp.StatusCode)
	}
}

// TestAcceptingACandidateIsAnOrdinaryWriteThatALockRefuses.
//
// This is where "shown rather than applied" stops being prose. Accepting
// goes through the same precedence engine as every other source, so a
// field somebody locked keeps its value and the rest are recorded as
// external — which loses to a person and beats a filename.
func TestAcceptingACandidateIsAnOrdinaryWriteThatALockRefuses(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "locked", "The Wrong Title", []byte("bytes"))
	enableStub(t, f, nil)

	// Lock the title by editing it, the way a librarian would.
	before := f.metadata(t, bookID, f.token)
	revision := int64(before["revision"].(float64))
	resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", f.token,
		map[string]any{
			"revision": revision,
			"title":    map[string]any{"value": "The Right Title"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}
	revision = int64(out["revision"].(float64))

	resp, out = f.send(t, http.MethodPost, "/v1/books/"+bookID+"/metadata/apply", f.token,
		map[string]any{
			"revision":  revision,
			"provider":  "stub",
			"title":     "Something The Service Preferred",
			"publisher": "Harper",
			"tags":      []string{"Whaling"},
			"authors":   []string{"Herman Melville"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply: %d %v", resp.StatusCode, out)
	}

	// The locked field is untouched, and still says a person chose it.
	if value, source := field(out, "title"); value != "The Right Title" ||
		source != string(store.MetadataManual) {
		t.Errorf("an external candidate overwrote a locked title: %v", out["title"])
	}
	// The unlocked ones took the candidate, attributed to where it came
	// from rather than to the person who pressed the button.
	if value, source := field(out, "publisher"); value != "Harper" ||
		source != string(store.MetadataExternal) {
		t.Errorf("publisher: %v", out["publisher"])
	}

	// Applying the same candidate twice changes nothing, and says so
	// rather than bumping the revision on every press.
	revision = int64(out["revision"].(float64))
	resp, _ = f.send(t, http.MethodPost, "/v1/books/"+bookID+"/metadata/apply", f.token,
		map[string]any{
			"revision": revision, "provider": "stub",
			"title": "Something The Service Preferred", "publisher": "Harper",
			"tags": []string{"Whaling"}, "authors": []string{"Herman Melville"},
		})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a no-op apply was written anyway: %d", resp.StatusCode)
	}
}

// TestAcceptingAStaleCandidateIsRefused. Two people tidying one shelf is
// ordinary, and accepting against a book that has moved on is a silent
// overwrite of whatever the other person just did.
func TestAcceptingAStaleCandidateIsRefused(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "stale", "Old", []byte("bytes"))
	enableStub(t, f, nil)

	before := f.metadata(t, bookID, f.token)
	stale := int64(before["revision"].(float64))

	if resp, out := f.send(t, http.MethodPut, "/v1/books/"+bookID+"/metadata", f.token,
		map[string]any{"revision": stale, "publisher": map[string]any{"value": "Somebody Else"}},
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}

	resp, _ := f.send(t, http.MethodPost, "/v1/books/"+bookID+"/metadata/apply", f.token,
		map[string]any{"revision": stale, "provider": "stub", "subtitle": "or, The Whale"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a stale accept was applied: %d", resp.StatusCode)
	}

	// A missing revision is refused rather than treated as "whatever is
	// there now", which would be the same overwrite with fewer steps.
	resp, _ = f.send(t, http.MethodPost, "/v1/books/"+bookID+"/metadata/apply", f.token,
		map[string]any{"provider": "stub", "subtitle": "or, The Whale"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an accept with no revision was allowed: %d", resp.StatusCode)
	}
}

// TestLookupsAreRateLimitedForTheServicesSake. OpenLibrary is free and
// runs on donations; a self-hoster who hammers it gets everybody
// blocked. The limit is per user, and it is the server's manners rather
// than its security.
func TestLookupsAreRateLimitedForTheServicesSake(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "eager", []byte("bytes"))
	enableStub(t, f, nil)
	f.srv.LookupLimiter = auth.NewRateLimiter(2, time.Minute)

	path := "/v1/books/" + bookID + "/metadata/lookup"
	for i := range 2 {
		if resp, _ := f.send(t, http.MethodPost, path, f.token, map[string]any{}); resp.StatusCode != http.StatusOK {
			t.Fatalf("lookup %d: %d", i, resp.StatusCode)
		}
	}
	resp, _ := f.send(t, http.MethodPost, path, f.token, map[string]any{})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the third lookup in a row was allowed: %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("a throttled caller was not told when to come back")
	}
}
