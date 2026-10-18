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

func TestFurthestResponsesPreserveOriginAliases(t *testing.T) {
	f := newFolderFixture(t)
	w := storetest.MkWork(t, f.st, f.user, "alias-work", "abc123")
	for i, alias := range []string{"partial-md5:first", "partial-md5:second"} {
		_, err := f.st.AppendOps(t.Context(), f.user.ID, "koreader", []store.Op{{
			OpID: fmt.Sprintf("alias-%d", i), WorkID: w.ID,
			ClientTS: time.Now(), Progression: 0.7 - float64(i)/10,
			Origin: store.OriginKosync, OriginAlias: &alias,
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/heads", "/v1/works/" + w.ID + "/positions?limit=1"} {
		code, out := get(t, f.ts.URL+path, f.token)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", path, code, out)
		}
		candidates := out["furthest"].([]any)
		if len(candidates) != 2 {
			t.Fatalf("lost an ownership group: %v", out)
		}
		for i, alias := range []string{"partial-md5:first", "partial-md5:second"} {
			op := candidates[i].(map[string]any)
			if op["origin_alias"] != alias || op["op_id"] != fmt.Sprintf("alias-%d", i) {
				t.Fatalf("lost source identity: %v", op)
			}
		}
	}
}

func TestForgetFurthest(t *testing.T) {
	f := newFolderFixture(t)
	w := storetest.MkWork(t, f.st, f.user, "forget-work", "abc123")
	forget := f.ts.URL + "/v1/works/" + w.ID + "/furthest/forget"
	code, out := get(t, f.ts.URL+"/v1/works/"+w.ID+"/positions", f.token)
	if code != http.StatusOK || out["furthest_floor"] != 0.0 {
		t.Fatalf("floor must always be present: %d %v", code, out)
	}
	for _, path := range []string{"/v1/heads", "/v1/changes?since=0"} {
		code, out = get(t, f.ts.URL+path, f.token)
		if floors, ok := out["furthest_floors"].(map[string]any); code != http.StatusOK || !ok || len(floors) != 0 {
			t.Fatalf("%s: floors must always be an object: %d %v", path, code, out)
		}
	}
	ops := []map[string]any{}
	for i, p := range []float64{0.98, 0.3, 0.31} {
		ops = append(ops, map[string]any{
			"op_id": fmt.Sprintf("op-%d", i), "work_id": w.ID,
			"client_ts": time.Now().UTC().Format(time.RFC3339Nano), "progression": p,
		})
	}
	if code, out = post(t, f.ts.URL+"/v1/ops", f.token, map[string]any{"ops": ops}); code != http.StatusOK {
		t.Fatalf("push: %d %v", code, out)
	}

	for _, body := range []any{
		map[string]any{},
		map[string]any{"upto_seq": nil},
		map[string]any{"upto_seq": -1},
		map[string]any{"upto_seq": "3"},
		map[string]any{"upto_seq": 1.5},
	} {
		if code, out = post(t, forget, f.token, body); code != http.StatusBadRequest || out["error"] == nil {
			t.Fatalf("%v: %d %v", body, code, out)
		}
	}
	// Repeating the request, or asking for less, answers the same floor.
	for _, upto := range []int64{1000, 3, 1} {
		code, out = post(t, forget, f.token, map[string]any{"upto_seq": upto})
		if code != http.StatusOK || out["furthest_floor"] != 3.0 || len(out) != 1 {
			t.Fatalf("forget upto %d: %d %v", upto, code, out)
		}
	}
	code, out = get(t, f.ts.URL+"/v1/works/"+w.ID+"/positions", f.token)
	if code != http.StatusOK || out["furthest_floor"] != 3.0 ||
		len(out["furthest"].([]any)) != 0 || len(out["ops"].([]any)) != 3 {
		t.Fatalf("positions after forgetting: %d %v", code, out)
	}
	code, out = get(t, f.ts.URL+"/v1/heads", f.token)
	if code != http.StatusOK || len(out["furthest"].([]any)) != 0 ||
		out["furthest_floors"].(map[string]any)[w.ID] != 3.0 {
		t.Fatalf("heads after forgetting: %d %v", code, out)
	}
	code, out = get(t, f.ts.URL+"/v1/changes?since=3", f.token)
	if code != http.StatusOK || len(out["ops"].([]any)) != 0 ||
		out["furthest_floors"].(map[string]any)[w.ID] != 3.0 {
		t.Fatalf("changes after forgetting: %d %v", code, out)
	}

	other := f.mintToken(t, f.other.ID, store.ScopeSync)
	for _, url := range []string{forget, f.ts.URL + "/v1/works/no-such-work/furthest/forget"} {
		code, out = post(t, url, other, map[string]any{"upto_seq": 1})
		if code != http.StatusNotFound || out["error"] != "work not found" {
			t.Fatalf("%s: %d %v", url, code, out)
		}
	}
	libraryOnly := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	if code, _ = post(t, forget, libraryOnly, map[string]any{"upto_seq": 1}); code != http.StatusForbidden {
		t.Fatalf("library-only scope: %d", code)
	}
}
