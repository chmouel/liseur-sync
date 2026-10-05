package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestFurthestPositionIsSeparateFromLatest(t *testing.T) {
	f := newFolderFixture(t)
	w := storetest.MkWork(t, f.st, f.user, "furthest-work", "abc123")
	code, out := get(t, f.ts.URL+"/v1/works/"+w.ID+"/positions", f.token)
	if code != http.StatusOK || len(out["ops"].([]any)) != 0 || len(out["furthest"].([]any)) != 0 {
		t.Fatalf("empty positions must be arrays: %d %v", code, out)
	}
	ops := []map[string]any{{
		"op_id": "peak", "work_id": w.ID, "edition_sha": "abc123",
		"client_ts": time.Now().UTC().Format(time.RFC3339Nano), "progression": 0.7,
		"locator": map[string]any{"href": "chapter7.xhtml", "locations": map[string]any{"totalProgression": 0.7}},
	}}
	for i := range 205 {
		ops = append(ops, map[string]any{
			"op_id": fmt.Sprintf("lower-%d", i), "work_id": w.ID, "edition_sha": "abc123",
			"client_ts": time.Now().UTC().Format(time.RFC3339Nano), "progression": 0.31,
		})
	}
	code, out = post(t, f.ts.URL+"/v1/ops", f.token, map[string]any{"ops": ops})
	if code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}
	if _, err := f.st.Compact(t.Context(), f.user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/works/" + w.ID + "/positions?limit=1", "/v1/heads"} {
		code, out = get(t, f.ts.URL+path, f.token)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", path, code, out)
		}
		latest := out["ops"].([]any)
		furthest := out["furthest"].([]any)
		if len(latest) != 1 || latest[0].(map[string]any)["op_id"] != "lower-204" ||
			len(furthest) != 1 {
			t.Fatalf("latest/furthest: %s %v", path, out)
		}
		peak := furthest[0].(map[string]any)
		if peak["op_id"] != "peak" || peak["seq"] != 1.0 ||
			peak["progression"] != 0.7 || peak["edition_sha"] != "abc123" ||
			peak["locator"].(map[string]any)["href"] != "chapter7.xhtml" ||
			peak["device_id"] == nil || peak["received_at"] == nil {
			t.Fatalf("lost source payload: %v", peak)
		}
		if path == "/v1/heads" && out["snapshot_seq"] != 206.0 {
			t.Fatalf("incorrect snapshot sequence: %v", out)
		}
	}
	other := f.mintToken(t, f.other.ID, store.ScopeSync)
	code, out = get(t, f.ts.URL+"/v1/heads", other)
	if code != http.StatusOK || len(out["furthest"].([]any)) != 0 {
		t.Fatalf("another account saw furthest: %d %v", code, out)
	}
	code, out = get(t, f.ts.URL+"/v1/works/"+w.ID+"/positions", other)
	if code != http.StatusNotFound {
		t.Fatalf("another account read the work: %d %v", code, out)
	}
	libraryOnly := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	for _, path := range []string{"/v1/heads", "/v1/works/" + w.ID + "/positions"} {
		code, _ = get(t, f.ts.URL+path, libraryOnly)
		if code != http.StatusForbidden {
			t.Fatalf("%s with library-only scope: %d", path, code)
		}
	}
}
