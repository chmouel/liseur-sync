//go:build linux

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// postRaw sends a body the JSON encoder did not produce, which is the only
// way to test what the handler does with one.
func (f *folderFixture) postRaw(
	t *testing.T, path, token, body string,
) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func (f *folderFixture) resolveBook(
	t *testing.T, bookID, token string,
) (*http.Response, catalogResolveResponse) {
	t.Helper()
	resp, raw := f.req(t, http.MethodPost, "/v1/books/"+bookID+"/resolve", token)
	return resp, decodeResolve(t, resp, raw)
}

// postResolve is resolveBook with a request body, for confirmation.
func (f *folderFixture) postResolve(
	t *testing.T, bookID, token, body string,
) (*http.Response, catalogResolveResponse) {
	t.Helper()
	resp, raw := f.postRaw(t, "/v1/books/"+bookID+"/resolve", token, body)
	return resp, decodeResolve(t, resp, raw)
}

func decodeResolve(
	t *testing.T, resp *http.Response, raw []byte,
) catalogResolveResponse {
	t.Helper()
	var out catalogResolveResponse
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode resolve response: %v (%s)", err, raw)
		}
	}
	return out
}

// TestResolveJoinsADownloadedBookToTheReadersWork is the reason the route
// exists: a reader who got a book from the catalog must be able to sync a
// position for it, which needs a work ID.
func TestResolveJoinsADownloadedBookToTheReadersWork(t *testing.T) {
	f := newFolderFixture(t)
	bookID, sha := f.publish(t, "resolvable", []byte("a book to resolve"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	resp, out := f.resolveBook(t, bookID, tok)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first resolve: %d", resp.StatusCode)
	}
	if out.WorkID == "" || !out.Created || out.Confidence != "high" {
		t.Fatalf("first resolve: %+v", out)
	}
	if out.BookID != bookID {
		t.Fatalf("book_id = %q, want %q", out.BookID, bookID)
	}

	// The catalog supplies the evidence, so the digest of the stored file
	// must be among it. Without this the work is joined to nothing a
	// second device could match on.
	var sawSHA bool
	for _, id := range out.Identifiers {
		if id.Kind == "sha256" && id.Value == sha {
			sawSHA = true
		}
	}
	if !sawSHA {
		t.Fatalf("resolve did not use the file digest: %+v", out.Identifiers)
	}

	// Resolving again is how a reinstalled client finds its work again.
	// It must return the same one and must not create a second.
	again, out2 := f.resolveBook(t, bookID, tok)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("second resolve: %d", again.StatusCode)
	}
	if out2.WorkID != out.WorkID || out2.Created {
		t.Fatalf("second resolve made a new work: %+v", out2)
	}

	mapping, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID)
	if err != nil || mapping.WorkID != out.WorkID {
		t.Fatalf("mapping: %+v %v", mapping, err)
	}
}

// TestResolveGivesEachReaderTheirOwnWork is the privacy property: a
// shared catalog book must not become a shared work, or one reader's
// position would be another's. The fixture assigns the same folder to
// both readers before they resolve the book.
func TestResolveGivesEachReaderTheirOwnWork(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "shared", []byte("a shared book"))
	mine := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)
	theirs := f.mintToken(t, f.other.ID, store.ScopeLibraryRead, store.ScopeSync)

	respA, a := f.resolveBook(t, bookID, mine)
	respB, b := f.resolveBook(t, bookID, theirs)
	if respA.StatusCode != http.StatusCreated || respB.StatusCode != http.StatusCreated {
		t.Fatalf("resolves: %d %d", respA.StatusCode, respB.StatusCode)
	}
	if a.WorkID == "" || a.WorkID == b.WorkID {
		t.Fatalf("two readers share one work: %q %q", a.WorkID, b.WorkID)
	}
}

// TestResolveNeedsBothCapabilities pins the decision that this route spans
// two layers. A catalog-only credential must not touch the work graph, and
// a sync-only one must not read the catalog.
func TestResolveNeedsBothCapabilities(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "twoscope", []byte("needs both"))

	for name, token := range map[string]string{
		"read-only": f.mintToken(t, f.user.ID, store.ScopeLibraryRead),
		"sync-only": f.mintToken(t, f.user.ID, store.ScopeSync),
	} {
		t.Run(name, func(t *testing.T) {
			resp, _ := f.resolveBook(t, bookID, token)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("code = %d, want 403", resp.StatusCode)
			}
		})
	}
	resp, _ := f.resolveBook(t, bookID, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated code = %d, want 401", resp.StatusCode)
	}
	// Nothing above was allowed to leave a mapping behind.
	if _, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID); err != store.ErrNotFound {
		t.Fatalf("refused resolve still mapped the book: %v", err)
	}
}

// TestResolveRejectsAMalformedBodyWithout5xx: the body is optional, so both
// "no body" and "garbage" have to be handled, and neither is a server bug.
func TestResolveRejectsAMalformedBodyWithout5xx(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "malformed", []byte("malformed body"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	resp, raw := f.postRaw(t, "/v1/books/"+bookID+"/resolve", tok, "{not json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body: %d %s", resp.StatusCode, raw)
	}
	// A missing book is a 404 even for a caller who may read the catalog.
	resp, _ = f.resolveBook(t, "book-does-not-exist", tok)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown book: %d", resp.StatusCode)
	}
}

// TestResolveHonoursConfirmationForAFuzzyMatch: a title/author match is a
// guess. Acting on it unasked would silently merge two different books into
// one reading history, which is the mistake ADR-0003 exists to prevent.
func TestResolveHonoursConfirmationForAFuzzyMatch(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "fuzzy", "Ambiguous Book", []byte("fuzzy body"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	// A work the reader already has, reachable only by title/author.
	existing := store.Work{
		ID: "existing-work", UserID: f.user.ID,
		Title: "Ambiguous Book", CreatedAt: time.Now().UTC(),
	}
	if err := f.st.CreateWork(t.Context(), existing, nil,
		[]store.Identifier{{Kind: "ta", Value: "ambiguous book|"}}); err != nil {
		t.Fatal(err)
	}

	resp, out := f.resolveBook(t, bookID, tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unconfirmed resolve: %d", resp.StatusCode)
	}
	if out.WorkID != existing.ID || out.Confidence != "low" || out.Created {
		t.Fatalf("unconfirmed resolve: %+v", out)
	}
	// Low confidence must not commit the mapping.
	if _, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID); err != store.ErrNotFound {
		t.Fatalf("unconfirmed match created a mapping: %v", err)
	}

	resp, out = f.postResolve(t, bookID, tok, `{"confirmed":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed resolve: %d", resp.StatusCode)
	}
	if out.WorkID != existing.ID || out.Confidence != "high" {
		t.Fatalf("confirmed resolve: %+v", out)
	}
	mapping, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID)
	if err != nil || mapping.WorkID != existing.ID {
		t.Fatalf("confirmed match did not map: %+v %v", mapping, err)
	}
}

// TestResolveReportsIdentifiersSpanningTwoWorks: when the catalog's evidence
// points at two different works, guessing one would corrupt a reading
// history. The reader is told instead.
func TestResolveReportsIdentifiersSpanningTwoWorks(t *testing.T) {
	f := newFolderFixture(t)
	bookID, sha := f.publishAs(t, "split", "Split Book", []byte("split body"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	now := time.Now().UTC()
	// One work already claims the file's digest.
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "work-sha", UserID: f.user.ID, CreatedAt: now}, nil,
		[]store.Identifier{{Kind: "sha256", Value: sha}}); err != nil {
		t.Fatal(err)
	}
	// Another claims its stable catalog alias.
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "work-source", UserID: f.user.ID, CreatedAt: now}, nil,
		[]store.Identifier{
			{Kind: "source", Value: "liseur-sync:" + bookID},
		}); err != nil {
		t.Fatal(err)
	}

	resp, raw := f.req(t, http.MethodPost, "/v1/books/"+bookID+"/resolve", tok)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting resolve: %d %s", resp.StatusCode, raw)
	}
	var body struct {
		Error string   `json:"error"`
		Works []string `json:"works"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Works) != 2 {
		t.Fatalf("conflict should name both works: %+v", body)
	}
	// A conflict must leave the graph exactly as it was.
	if _, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID); err != store.ErrNotFound {
		t.Fatalf("conflict still mapped the book: %v", err)
	}
}
