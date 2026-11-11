package api

import (
	"net/http"
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
