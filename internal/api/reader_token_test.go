//go:build linux

package api

import (
	"encoding/json"
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

// TestReaderPositionRoundTripsAsALocator is ADR-0007's last acceptance
// criterion, exercised over the wire: the browser reader pushes a
// Readium Locator, and what comes back is the same locator plus the
// progression every other client can act on.
//
// The server stores the locator opaquely, which is exactly why this is
// worth testing — nothing in the store would notice if the reader began
// writing a shape no other client understands.
func TestReaderPositionRoundTripsAsALocator(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "syncable", []byte("a book to read"))

	reader, _, err := auth.NewService(f.st).MintReaderToken(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp, resolved := f.resolveBook(t, bookID, reader)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("reader could not resolve the book it downloaded: %d", resp.StatusCode)
	}

	// Exactly what internal/webui/static/reader.js emits: chapter 2 of
	// 4, halfway down.
	const locator = `{"href":"OEBPS/ch2.xhtml","type":"application/xhtml+xml",` +
		`"locations":{"progression":0.5,"totalProgression":0.375,"position":2},` +
		`"title":"Moby-Dick"}`
	push := `{"ops":[{"op_id":"reader-op-1","work_id":"` + resolved.WorkID + `",` +
		`"client_ts":"2026-08-13T18:00:00Z","progression":0.375,"locator":` + locator + `}]}`
	if resp, raw := f.postRaw(t, "/v1/ops", reader, push); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader could not push a position: %d %s", resp.StatusCode, raw)
	}

	resp, raw := f.get(t, "/v1/works/"+resolved.WorkID+"/positions?limit=1", reader)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("positions: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Ops []struct {
			Progression float64         `json:"progression"`
			Locator     json.RawMessage `json:"locator"`
			DeviceID    string          `json:"device_id"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Ops) != 1 {
		t.Fatalf("want the position just written, got %d", len(out.Ops))
	}
	got := out.Ops[0]
	if got.Progression != 0.375 {
		t.Errorf("progression: want 0.375, got %v", got.Progression)
	}

	// The locator must survive verbatim: the reader reopens at the exact
	// place from href and the within-chapter fraction, and every other
	// client falls back on totalProgression.
	var back struct {
		HRef      string `json:"href"`
		Locations struct {
			Progression      float64 `json:"progression"`
			TotalProgression float64 `json:"totalProgression"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(got.Locator, &back); err != nil {
		t.Fatalf("stored locator is not the JSON that was sent: %v", err)
	}
	if back.HRef != "OEBPS/ch2.xhtml" {
		t.Errorf("locator href: want OEBPS/ch2.xhtml, got %q", back.HRef)
	}
	if back.Locations.Progression != 0.5 || back.Locations.TotalProgression != 0.375 {
		t.Errorf("locator locations did not survive: %+v", back.Locations)
	}

	// The device is the browser, and re-minting must not change it, or
	// the same person reading in two tabs becomes two heads.
	if _, again, err := auth.NewService(f.st).MintReaderToken(t.Context(), f.user.ID); err != nil {
		t.Fatal(err)
	} else if again.DeviceID != got.DeviceID {
		t.Errorf("device id changed between mints: op said %q, new token says %q",
			got.DeviceID, again.DeviceID)
	}
}
