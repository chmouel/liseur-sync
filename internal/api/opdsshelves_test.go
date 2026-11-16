package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestOPDSShelvesFollowTheReadersOwnPosition(t *testing.T) {
	f := newFolderFixture(t)
	zebra, digest := f.publishAs(t, "z", "Zebra", []byte("zebra bytes"))
	f.publishAs(t, "a", "apple", []byte("apple bytes"))
	f.publishAs(t, "m", "Mango", []byte("mango bytes"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)
	resp, out := f.resolveBook(t, zebra, tok)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resolve: %d", resp.StatusCode)
	}
	f.pushPosition(t, tok, out.WorkID, digest)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	titles := func(shelf string) []string {
		t.Helper()
		resp, raw := f.opds(t, "/opds/v1.2/shelves/"+shelf, "token", read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", shelf, resp.StatusCode, raw)
		}
		var got []string
		for _, e := range parseFeed(t, raw).Entries {
			got = append(got, e.Title)
		}
		return got
	}
	for shelf, want := range map[string]string{
		"reading":      "Zebra",
		"last-read":    "Zebra",
		"unread":       "Mango apple",
		"alphabetical": "apple Mango Zebra",
	} {
		if got := strings.Join(titles(shelf), " "); got != want {
			t.Errorf("%s = %q, want %q", shelf, got, want)
		}
	}

	if resp, _ := f.opds(t, "/opds/v1.2/shelves/nope", "token", read); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown shelf = %d, want 404", resp.StatusCode)
	}
	if resp, _ := f.opds(t, "/opds/v1.2/shelves/unread?offset=-1", "token", read); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad offset = %d, want 400", resp.StatusCode)
	}
	// Another reader has opened nothing.
	other := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	if resp, raw := f.opds(t, "/opds/v1.2/shelves/reading", "token", other); resp.StatusCode != http.StatusOK ||
		len(parseFeed(t, raw).Entries) != 0 {
		t.Errorf("another reader's reading shelf leaked: %d %s", resp.StatusCode, raw)
	}
}

// TestOPDSShelvesReadTheExplicitChoice: a shelf is a view of the
// resolution rule, not of the position. A book marked read at 0.4 is on
// the shelf of books read, one marked unread at 0.99 is on the unread
// shelf and off "currently reading", and a book whose choice was
// withdrawn is back where its position says (ADR-0066).
func TestOPDSShelvesReadTheExplicitChoice(t *testing.T) {
	f := newFolderFixture(t)
	read, digest := f.publishAs(t, "r", "Read", []byte("read bytes"))
	back, backDigest := f.publishAs(t, "u", "Backonthepile", []byte("pile bytes"))
	withdrawn, withdrawnDigest := f.publishAs(t, "w", "Withdrawn", []byte("withdrawn bytes"))
	wantBook, wantDigest := f.publishAs(t, "want", "Wanted", []byte("wanted bytes"))
	pausedBook, pausedDigest := f.publishAs(t, "paused", "Paused", []byte("paused bytes"))
	dnfBook, dnfDigest := f.publishAs(t, "dnf", "Abandoned", []byte("abandoned bytes"))
	readingBook, readingDigest := f.publishAs(t, "reading", "Explicit reading", []byte("reading bytes"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	push := func(opID, workID, sha string, progression float64, status string) {
		t.Helper()
		body := `{"ops":[{"op_id":"` + opID + `","work_id":"` + workID +
			`","edition_sha":"` + sha + `","client_ts":"2024-01-01T00:00:00Z","progression":` +
			strconv.FormatFloat(progression, 'f', -1, 64)
		if status != "" {
			body += `,"kind":"status","reading_status":"` + status + `"`
		}
		resp, raw := f.postRaw(t, "/v1/ops", tok, body+`}]}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d %s", opID, status, resp.StatusCode, raw)
		}
	}
	resolve := func(bookID string) string {
		t.Helper()
		resp, out := f.resolveBook(t, bookID, tok)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("resolve: %d", resp.StatusCode)
		}
		return out.WorkID
	}

	readWork := resolve(read)
	push("read-pos", readWork, digest, 0.4, "")
	push("read-act", readWork, digest, 0.4, "finished")

	backWork := resolve(back)
	push("back-pos", backWork, backDigest, 0.99, "")
	push("back-act", backWork, backDigest, 0.99, "unread")

	withdrawnWork := resolve(withdrawn)
	push("with-pos", withdrawnWork, withdrawnDigest, 0.2, "")
	push("with-act", withdrawnWork, withdrawnDigest, 0.2, "finished")
	push("with-auto", withdrawnWork, withdrawnDigest, 0.2, "automatic")
	push("want-act", resolve(wantBook), wantDigest, 0, "want_to_read")
	pausedWork := resolve(pausedBook)
	push("paused-act", pausedWork, pausedDigest, 0, "paused")
	push("paused-pos", pausedWork, pausedDigest, 0.99, "")
	push("dnf-act", resolve(dnfBook), dnfDigest, 0, "did_not_finish")
	push("reading-act", resolve(readingBook), readingDigest, 0, "reading")

	readTok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	titles := func(shelf string) []string {
		t.Helper()
		resp, raw := f.opds(t, "/opds/v1.2/shelves/"+shelf, "token", readTok)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", shelf, resp.StatusCode, raw)
		}
		var got []string
		for _, e := range parseFeed(t, raw).Entries {
			got = append(got, e.Title)
		}
		sort.Strings(got)
		return got
	}
	for shelf, want := range map[string]string{
		"reading":        "Explicit reading Withdrawn",
		"unread":         "Backonthepile Wanted",
		"last-read":      "Explicit reading Read Withdrawn",
		"want-to-read":   "Wanted",
		"paused":         "Paused",
		"did-not-finish": "Abandoned",
	} {
		if got := strings.Join(titles(shelf), " "); got != want {
			t.Errorf("%s = %q, want %q", shelf, got, want)
		}
	}
	// A paused book has been started but is not being read, and the
	// reader is in the middle of it: it is on neither the unread nor the
	// currently-reading shelf, and only its own shelf names it.
	if got := strings.Join(titles("reading"), " "); strings.Contains(got, "Paused") {
		t.Errorf("paused is on the reading shelf: %q", got)
	}
	if got := strings.Join(titles("unread"), " "); strings.Contains(got, "Abandoned") {
		t.Errorf("an unfinished book is on the unread shelf: %q", got)
	}
}
