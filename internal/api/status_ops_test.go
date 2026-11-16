//go:build linux

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestPushStatusOps: POST /v1/ops carries the explicit reading status
// beside the position, refuses a malformed act with the same item
// refusal shape, and replays on the whole payload (ADR-0066).
func TestPushStatusOps(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publishAs(t, "z", "Zebra", []byte("zebra bytes"))
	tok := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)
	resp, out := f.resolveBook(t, bookID, tok)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resolve: %d", resp.StatusCode)
	}

	push := func(body string) (*http.Response, []byte) {
		t.Helper()
		return f.postRaw(t, "/v1/ops", tok, body)
	}
	act := `{"ops":[{"op_id":"act-1","work_id":"` + out.WorkID + `",
		"edition_sha":"` + digest + `","client_ts":"2024-01-01T00:00:00Z",
		"progression":0.4,"kind":"status","reading_status":"finished"}]}`
	raw, body := push(act)
	if raw.StatusCode != http.StatusOK {
		t.Fatalf("status act refused: %d %s", raw.StatusCode, body)
	}
	again, body := push(act)
	if again.StatusCode != http.StatusOK || !containsStatus(body, "duplicate") {
		t.Fatalf("replay: %d %s", again.StatusCode, body)
	}
	changed := `{"ops":[{"op_id":"act-1","work_id":"` + out.WorkID + `",
		"edition_sha":"` + digest + `","client_ts":"2024-01-01T00:00:00Z",
		"progression":0.4,"kind":"status","reading_status":"unread"}]}`
	if raw, body := push(changed); raw.StatusCode != http.StatusOK || !containsStatus(body, "conflict") {
		t.Fatalf("changed reading_status must conflict: %d %s", raw.StatusCode, body)
	}

	for name, c := range map[string]struct{ body, code string }{
		"a status act without a status": {
			`{"ops":[{"op_id":"bad-1","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"status"}]}`,
			"missing_field",
		},
		"an unknown kind": {
			`{"ops":[{"op_id":"bad-2","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"moved"}]}`,
			"bad_value",
		},
		"an unknown reading status": {
			`{"ops":[{"op_id":"bad-3","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"status",
			  "reading_status":"abandoned"}]}`,
			"bad_value",
		},
		"coverage on a status act": {
			`{"ops":[{"op_id":"bad-4","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"status",
			  "reading_status":"finished","coverage":"partial"}]}`,
			"bad_value",
		},
		"an unknown coverage": {
			`{"ops":[{"op_id":"bad-5","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"coverage":"half"}]}`,
			"bad_value",
		},
		"a reading status on a position op": {
			`{"ops":[{"op_id":"bad-6","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,
			  "reading_status":"finished"}]}`,
			"bad_value",
		},
		"an explicit position kind": {
			`{"ops":[{"op_id":"bad-7","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"position"}]}`,
			"bad_value",
		},
		"an empty kind": {
			`{"ops":[{"op_id":"bad-8","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":""}]}`,
			"bad_value",
		},
		"an empty reading status": {
			`{"ops":[{"op_id":"bad-9","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"kind":"status",
			  "reading_status":""}]}`,
			"bad_value",
		},
		"an empty coverage": {
			`{"ops":[{"op_id":"bad-10","work_id":"` + out.WorkID + `",
			  "client_ts":"2024-01-01T00:00:00Z","progression":0.4,"coverage":""}]}`,
			"bad_value",
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, body := push(c.body)
			if raw.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", raw.StatusCode, body)
			}
			var refusal struct {
				Code      string `json:"code"`
				ItemIndex *int   `json:"item_index"`
			}
			if err := json.Unmarshal(body, &refusal); err != nil {
				t.Fatalf("refusal is not JSON: %v (%s)", err, body)
			}
			if refusal.Code != c.code || refusal.ItemIndex == nil {
				t.Fatalf("refusal = %+v, want code %q and an item index", refusal, c.code)
			}
		})
	}

	// A position with coverage is accepted, and the changes feed hands
	// back both kinds with the fields they were written with.
	pos := `{"ops":[{"op_id":"pos-1","work_id":"` + out.WorkID + `",
		"edition_sha":"` + digest + `","client_ts":"2024-01-01T00:00:00Z",
		"progression":0.5,"coverage":"partial"}]}`
	if raw, body := push(pos); raw.StatusCode != http.StatusOK {
		t.Fatalf("position with coverage refused: %d %s", raw.StatusCode, body)
	}
	_, changes := f.get(t, "/v1/changes?since=0", tok)
	var page struct {
		Ops []map[string]any `json:"ops"`
	}
	if err := json.Unmarshal(changes, &page); err != nil {
		t.Fatalf("changes: %v (%s)", err, changes)
	}
	kinds := map[string]map[string]any{}
	for _, op := range page.Ops {
		id, _ := op["op_id"].(string)
		kinds[id] = op
	}
	if act := kinds["act-1"]; act["kind"] != "status" || act["reading_status"] != "finished" {
		t.Fatalf("status act on the wire = %+v", act)
	}
	if act := kinds["act-1"]; act["coverage"] != nil {
		t.Fatalf("a status act rendered coverage: %+v", act)
	}
	position := kinds["pos-1"]
	if position["coverage"] != "partial" {
		t.Fatalf("position coverage on the wire = %+v", position)
	}
	if _, ok := position["kind"]; ok {
		t.Fatalf("a position op rendered a kind: %+v", position)
	}
}

func containsStatus(body []byte, want string) bool {
	var out struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false
	}
	for _, r := range out.Results {
		if r.Status == want {
			return true
		}
	}
	return false
}
