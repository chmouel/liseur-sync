//go:build linux

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
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

// resolveBatch posts a batch resolve and decodes its per-item results.
func (f *folderFixture) resolveBatch(
	t *testing.T, token, body string,
) (*http.Response, []catalogResolveItemJSON) {
	t.Helper()
	resp, raw := f.postRaw(t, "/v1/books/resolve", token, body)
	var out struct {
		Results []catalogResolveItemJSON `json:"results"`
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode batch resolve: %v (%s)", err, raw)
		}
	}
	return resp, out.Results
}

func batchBody(t *testing.T, ids []string, confirmed bool) string {
	t.Helper()
	raw, err := json.Marshal(catalogResolveBatchRequest{BookIDs: ids, Confirmed: confirmed})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestResolveBatchNamesAWholeShelfAtOnce is the reason the route exists: a
// device that has just signed in has a name here for none of its books, and
// asking one request per book is what made a fresh library arrive in
// batches over several refreshes.
func TestResolveBatchNamesAWholeShelfAtOnce(t *testing.T) {
	f := newFolderFixture(t)
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	ids := make([]string, 0, 4)
	shas := make(map[string]string, 4)
	for _, name := range []string{"one", "two", "three", "four"} {
		id, sha := f.publish(t, "batch-"+name, []byte("a book called "+name))
		ids = append(ids, id)
		shas[id] = sha
	}

	resp, results := f.resolveBatch(t, tok, batchBody(t, ids, false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch resolve: %d", resp.StatusCode)
	}
	if len(results) != len(ids) {
		t.Fatalf("got %d results for %d books", len(results), len(ids))
	}

	works := make(map[string]string, len(ids))
	for i, item := range results {
		// Answers come back in the order they were asked for, so a
		// client can pair them up without matching on the id.
		if item.BookID != ids[i] {
			t.Fatalf("result %d is for %q, want %q", i, item.BookID, ids[i])
		}
		if item.Error != "" || item.WorkID == "" || !item.Created || item.Confidence != "high" {
			t.Fatalf("result %d: %+v", i, item)
		}
		if works[item.WorkID] != "" {
			t.Fatalf("two books share one work: %+v", results)
		}
		works[item.WorkID] = item.BookID

		// The evidence is the catalog's, exactly as on the single route:
		// a client that has only browsed has never seen the bytes.
		var sawSHA bool
		for _, id := range item.Identifiers {
			if id.Kind == "sha256" && id.Value == shas[item.BookID] {
				sawSHA = true
			}
		}
		if !sawSHA {
			t.Fatalf("result %d resolved without the file digest: %+v", i, item.Identifiers)
		}

		mapping, err := f.st.UserBookWork(t.Context(), f.user.ID, item.BookID)
		if err != nil || mapping.WorkID != item.WorkID {
			t.Fatalf("result %d not mapped: %+v %v", i, mapping, err)
		}
	}

	// Replaying is how a reinstalled client finds its works again, and
	// must name the same ones rather than making a second set.
	again, replay := f.resolveBatch(t, tok, batchBody(t, ids, false))
	if again.StatusCode != http.StatusOK {
		t.Fatalf("replayed batch: %d", again.StatusCode)
	}
	for i, item := range replay {
		if item.Created {
			t.Fatalf("replay created a work: %+v", item)
		}
		if works[item.WorkID] != ids[i] {
			t.Fatalf("replay renamed %q: %+v", ids[i], item)
		}
	}
}

// TestResolveBatchRefusesOneBookAtATime is the whole point of per-item
// errors: a device naming five hundred books must not lose the four
// hundred and ninety-eight that worked because two did not.
func TestResolveBatchRefusesOneBookAtATime(t *testing.T) {
	f := newFolderFixture(t)
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	good, _ := f.publish(t, "batch-good", []byte("a book that resolves"))
	split, sha := f.publishAs(t, "batch-split", "Split Book", []byte("split body"))

	// Two works already claim different identifiers of the same book, so
	// the server cannot tell which one it is.
	now := time.Now().UTC()
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "batch-work-sha", UserID: f.user.ID, CreatedAt: now}, nil,
		[]store.Identifier{{Kind: "sha256", Value: sha}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateWork(t.Context(),
		store.Work{ID: "batch-work-source", UserID: f.user.ID, CreatedAt: now}, nil,
		[]store.Identifier{{Kind: "source", Value: "liseur-sync:" + split}}); err != nil {
		t.Fatal(err)
	}

	ids := []string{good, "book-does-not-exist", split}
	resp, results := f.resolveBatch(t, tok, batchBody(t, ids, false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mixed batch: %d", resp.StatusCode)
	}
	if len(results) != 3 {
		t.Fatalf("mixed batch returned %d results", len(results))
	}
	if results[0].Error != "" || results[0].WorkID == "" {
		t.Fatalf("the good book did not resolve: %+v", results[0])
	}
	if results[1].Error != resolveErrNotFound || results[1].WorkID != "" {
		t.Fatalf("unknown book: %+v", results[1])
	}
	if results[2].Error != resolveErrAmbiguous || len(results[2].Works) != 2 {
		t.Fatalf("ambiguous book: %+v", results[2])
	}
	// Neither refusal may leave anything behind.
	for _, id := range []string{"book-does-not-exist", split} {
		if _, err := f.st.UserBookWork(t.Context(), f.user.ID, id); err != store.ErrNotFound {
			t.Fatalf("refused book %q was still mapped: %v", id, err)
		}
	}
}

// TestResolveBatchLeavesADoubtfulMatchToTheReader: a title/author match is
// a guess, and it is no more actionable in bulk than it is one at a time.
// The work comes back marked rather than refused, and nothing is committed.
func TestResolveBatchLeavesADoubtfulMatchToTheReader(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "batch-fuzzy", "Ambiguous Book", []byte("fuzzy body"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	existing := store.Work{
		ID: "batch-existing-work", UserID: f.user.ID,
		Title: "Ambiguous Book", CreatedAt: time.Now().UTC(),
	}
	if err := f.st.CreateWork(t.Context(), existing, nil,
		[]store.Identifier{{Kind: "ta", Value: "ambiguous book|"}}); err != nil {
		t.Fatal(err)
	}

	resp, results := f.resolveBatch(t, tok, batchBody(t, []string{bookID}, false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unconfirmed batch: %d", resp.StatusCode)
	}
	if results[0].Error != "" || results[0].WorkID != existing.ID ||
		results[0].Confidence != "low" || results[0].Created {
		t.Fatalf("unconfirmed batch: %+v", results[0])
	}
	if _, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID); err != store.ErrNotFound {
		t.Fatalf("a doubtful match was committed: %v", err)
	}
}

// TestResolveBatchNeedsBothCapabilities: the batch spans the same two
// layers as the single route, so it must demand the same pair. A gap here
// would be a way around the scope check that route exists to enforce.
func TestResolveBatchNeedsBothCapabilities(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "batch-twoscope", []byte("needs both"))
	body := batchBody(t, []string{bookID}, false)

	for name, token := range map[string]string{
		"read-only": f.mintToken(t, f.user.ID, store.ScopeLibraryRead),
		"sync-only": f.mintToken(t, f.user.ID, store.ScopeSync),
	} {
		t.Run(name, func(t *testing.T) {
			resp, _ := f.resolveBatch(t, token, body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("code = %d, want 403", resp.StatusCode)
			}
		})
	}
	resp, _ := f.resolveBatch(t, "", body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated code = %d, want 401", resp.StatusCode)
	}
	if _, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID); err != store.ErrNotFound {
		t.Fatalf("a refused batch still mapped the book: %v", err)
	}
}

// TestResolveBatchNamesItsOwnLimit: a client that asks for too much is told
// how much it may ask for, so it can cut the request down rather than
// guess. The app already halves a batch against exactly this shape.
func TestResolveBatchNamesItsOwnLimit(t *testing.T) {
	f := newFolderFixture(t)
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	ids := make([]string, maxResolveBatch+1)
	for i := range ids {
		ids[i] = "book-" + strconv.Itoa(i)
	}
	resp, raw := f.postRaw(t, "/v1/books/resolve", tok, batchBody(t, ids, false))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("over-limit batch: %d %s", resp.StatusCode, raw)
	}
	var body struct {
		Code  string `json:"code"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != errCodeBatchTooLarge || body.Limit != maxResolveBatch {
		t.Fatalf("over-limit batch: %+v", body)
	}

	// An empty or malformed batch is the caller's mistake, never a 5xx.
	for name, sent := range map[string]string{
		"empty":     `{"book_ids":[]}`,
		"missing":   `{}`,
		"malformed": `{not json`,
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := f.postRaw(t, "/v1/books/resolve", tok, sent)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (%s)", resp.StatusCode, raw)
			}
		})
	}
}
