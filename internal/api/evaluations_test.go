package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func TestEvaluationRoutes(t *testing.T) {
	ts, st := testServer(t)
	tok, work := annotationTestUser(t, ts.URL, st, "u1", "alice")
	other, _ := annotationTestUser(t, ts.URL, st, "u2", "bob")
	url := ts.URL + "/v1/works/" + work + "/evaluation"
	for _, token := range []string{"", other} {
		code, _ := get(t, url, token)
		want := http.StatusNotFound
		if token == "" {
			want = http.StatusUnauthorized
		}
		if code != want {
			t.Fatalf("read isolation: got %d, want %d", code, want)
		}
	}
	cases := []struct {
		field string
		value any
	}{
		{"rating", 1}, {"rating", 11}, {"rating", 4.5}, {"rating", "9"},
		{"review", strings.Repeat("é", 4097)}, {"review", "a\x00b"}, {"review", " "}, {"unknown", nil},
	}
	for _, item := range cases {
		code, out := post(t, url, tok, map[string]any{"write_id": "bad", "writer_id": "installation", "revision": 1, "field": item.field, "value": item.value})
		if code != http.StatusBadRequest {
			t.Fatalf("accepted invalid input: %+v: %d %v", item, code, out)
		}
	}
	for _, provenance := range []map[string]any{
		{},
		{"writer_id": "installation"},
		{"revision": 1},
		{"writer_id": " ", "revision": 1},
		{"writer_id": "bad\x00writer", "revision": 1},
		{"writer_id": strings.Repeat("x", 65), "revision": 1},
		{"writer_id": "installation", "revision": 0},
		{"writer_id": "installation", "revision": -1},
		{"writer_id": "installation", "revision": 1.5},
		{"writer_id": "installation", "revision": 9007199254740992},
	} {
		provenance["write_id"] = "invalid-provenance"
		provenance["field"] = "review"
		provenance["value"] = "Private review"
		if code, out := post(t, url, tok, provenance); code != http.StatusBadRequest {
			t.Fatalf("accepted invalid provenance: %+v: %d %v", provenance, code, out)
		}
	}
	for _, item := range []map[string]any{
		{"write_id": "rating", "writer_id": "installation", "revision": 1, "field": "rating", "value": 9},
		{"write_id": "review", "writer_id": "installation", "revision": 2, "field": "review", "value": "A private review"},
		{"write_id": "clear", "writer_id": "installation", "revision": 3, "field": "review", "value": nil},
		{"write_id": "review", "writer_id": "installation", "revision": 2, "field": "review", "value": "A private review"},
	} {
		code, out := post(t, url, tok, item)
		if code != http.StatusOK {
			t.Fatalf("write: %d %v", code, out)
		}
	}
	code, out := get(t, url, tok)
	if code != http.StatusOK {
		t.Fatalf("read: %d %v", code, out)
	}
	fields := out["fields"].([]any)
	if len(fields) != 2 || fields[0].(map[string]any)["value"] != float64(9) || fields[1].(map[string]any)["value"] != nil {
		t.Fatalf("fields: %v", out)
	}
	code, _ = post(t, url, tok, map[string]any{"write_id": "rating", "writer_id": "installation", "revision": 1, "field": "rating", "value": 8})
	if code != http.StatusConflict {
		t.Fatalf("id mismatch: %d", code)
	}
	code, _ = post(t, url, other, map[string]any{"write_id": "other", "writer_id": "installation", "revision": 1, "field": "rating", "value": 8})
	if code != http.StatusNotFound {
		t.Fatalf("cross-user write: %d", code)
	}
}

func TestEvaluationRetryAfterDeviceChange(t *testing.T) {
	ts, st := testServer(t)
	first, work := annotationTestUser(t, ts.URL, st, "u1", "alice")
	next, _, err := auth.NewService(st).MintToken(t.Context(), "u1", "replacement-device", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/v1/works/" + work + "/evaluation"
	original := map[string]any{"write_id": "first-review", "writer_id": "installation", "revision": 1, "field": "review", "value": "Original review"}
	if code, out := post(t, url, first, original); code != http.StatusOK {
		t.Fatalf("first write: %d %v", code, out)
	}
	if code, out := post(t, url, next, map[string]any{"write_id": "removal", "writer_id": "installation", "revision": 2, "field": "review", "value": nil}); code != http.StatusOK {
		t.Fatalf("removal: %d %v", code, out)
	}
	if code, out := post(t, url, next, original); code != http.StatusOK {
		t.Fatalf("retry from replacement device: %d %v", code, out)
	}
	code, out := get(t, url, next)
	if code != http.StatusOK {
		t.Fatalf("read: %d %v", code, out)
	}
	fields := out["fields"].([]any)
	if len(fields) != 1 || fields[0].(map[string]any)["value"] != nil || fields[0].(map[string]any)["write_id"] != "removal" {
		t.Fatalf("retry restored an older review: %v", out)
	}
	if code, _ := post(t, url, next, map[string]any{"write_id": "first-review", "writer_id": "installation", "revision": 1, "field": "review", "value": "Changed"}); code != http.StatusConflict {
		t.Fatalf("changed-content retry: %d", code)
	}
}

func TestEvaluationMergeRequiresRecoverableHeads(t *testing.T) {
	ts, st := testServer(t)
	token, into := annotationTestUser(t, ts.URL, st, "u1", "alice")
	from := storetest.MkWork(t, st, store.User{ID: "u1"}, "evaluation-source", "evaluation-source-sha")
	for _, write := range []struct {
		work, id, writer string
		revision         int64
	}{
		{from.ID, "a20", "writer-a", 20},
		{into, "b20", "writer-b", 20},
		{into, "a10", "writer-a", 10},
		{from.ID, "b10", "writer-b", 10},
	} {
		if code, out := post(t, ts.URL+"/v1/works/"+write.work+"/evaluation", token, map[string]any{
			"write_id": write.id, "writer_id": write.writer, "revision": write.revision, "field": "review", "value": "Review",
		}); code != http.StatusOK {
			t.Fatalf("write: %d %v", code, out)
		}
	}
	code, out := post(t, ts.URL+"/v1/works/merge", token, map[string]any{"from_work_id": from.ID, "into_work_id": into})
	if code != http.StatusConflict || out["error"] != "save a new rating or review before merging these works" {
		t.Fatalf("merge conflict: %d %v", code, out)
	}
}
