//go:build linux

package api

import (
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
)

// TestReaderTokenReadsAndSyncsButCannotManage is ADR-0007's acceptance
// criterion for the browser reader's credential, checked against the
// real routes rather than against the scope set in isolation.
//
// The reader is an ordinary API client: it may read the catalog and push
// positions, and that is all. The token is minted here by the same call
// the web UI uses, so a change to the reader's scopes shows up as a
// failure of this test rather than as a quietly wider credential.
func TestReaderTokenReadsAndSyncsButCannotManage(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "readable", []byte("a readable book"))

	reader, _, err := auth.NewService(f.st).MintReaderToken(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// What the reader is for: finding the book and fetching its bytes.
	if resp, raw := f.get(t, "/v1/books/"+bookID, reader); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader cannot open a book it is meant to read: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/download", reader); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader cannot download: %d", resp.StatusCode)
	}
	// Sync, which is the other half of being a reader.
	if resp, raw := f.get(t, "/v1/changes?since=0", reader); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader cannot pull changes: %d %s", resp.StatusCode, raw)
	}

	// What it must never be: a way to alter the library.
	if resp, _ := f.req(t, http.MethodDelete, "/v1/books/"+bookID, reader); resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader token deleted a book: want 403, got %d", resp.StatusCode)
	}
	if resp, _ := f.req(
		t, http.MethodPost, "/v1/libraries/"+f.library+"/upload", reader,
	); resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader token reached upload: want 403, got %d", resp.StatusCode)
	}
	// Nor a way to read what other people's reading looked like.
	if resp, _ := f.get(t, "/v1/insights/summary", reader); resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader token reached insights: want 403, got %d", resp.StatusCode)
	}
}
