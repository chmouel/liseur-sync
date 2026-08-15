package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/buildinfo"
)

// The health probe names the running build. An operator whose container
// is serving an image older than they believe cannot always sign in to
// find out, and the published image carries no OCI labels to inspect
// from outside, so this route is the only answer available from a
// single unauthenticated request.
func TestHealthzReportsTheRunningBuild(t *testing.T) {
	ts, _ := testServer(t)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Status   string `json:"status"`
		Version  string `json:"version"`
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// The container smoke test in .github/workflows/build.yaml greps
	// the response for "ok"; keeping that assertion here means the
	// deployment check cannot be broken by a change made only in Go.
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	// buildinfo always resolves to something, falling back to "dev", so
	// an empty version means the stamp was dropped rather than that
	// this happens to be an unstamped build.
	if body.Version == "" {
		t.Error("version is empty; buildinfo always reports at least \"dev\"")
	}
	if want := buildinfo.Get().Short(); body.Version != want {
		t.Errorf("version = %q, want %q", body.Version, want)
	}
	if want := buildinfo.Get().ShortRevision(); body.Revision != want {
		t.Errorf("revision = %q, want %q", body.Revision, want)
	}
}

// Reporting the build must not have moved the route behind the scope
// check: a probe is useless if it needs a token.
func TestHealthzStaysUnauthenticated(t *testing.T) {
	ts, _ := testServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d without a token, want 200", resp.StatusCode)
	}
}
