package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() *Server {
	tools := []Tool{
		{Name: "echo", Title: "Echo", Description: "echo", Scope: "a",
			InputSchema: map[string]any{"type": "object"},
			Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				var in struct {
					Say string `json:"say"`
				}
				if err := DecodeArgs(raw, &in); err != nil {
					return nil, err
				}
				if in.Say == "fail" {
					return nil, Errorf("no")
				}
				if in.Say == "crash" {
					return nil, errors.New("database path /secret is gone")
				}
				return map[string]any{"said": in.Say}, nil
			}},
		{Name: "hidden", Scope: "b", InputSchema: map[string]any{"type": "object"},
			Call: func(context.Context, json.RawMessage) (any, error) { return map[string]any{}, nil }},
	}
	return New("test", "https://sync.example", func(context.Context) []string { return []string{"a"} }, tools)
}

func post(t *testing.T, s *Server, body string, hdr ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestTransportRefusals(t *testing.T) {
	s := testServer()
	if w, _ := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w, _ := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://sync.example"); w.Code != http.StatusOK {
		t.Fatalf("own origin: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET: %d %q", w.Code, w.Header().Get("Allow"))
	}
	if w, _ := post(t, s, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`); w.Code != http.StatusBadRequest {
		t.Fatalf("batch: %d", w.Code)
	}
	if w, out := post(t, s, `{nope`); w.Code != http.StatusBadRequest || out["error"] == nil {
		t.Fatalf("parse error: %d %v", w.Code, out)
	}
	if w, _ := post(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != http.StatusAccepted {
		t.Fatalf("notification: %d", w.Code)
	}
	if w, _ := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "MCP-Protocol-Version", "1999-01-01"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad version header: %d", w.Code)
	}
	if w, _ := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping","x":"`+strings.Repeat("a", bodyMax)+`"}`); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d", w.Code)
	}
}

func TestInitializeNegotiates(t *testing.T) {
	s := testServer()
	_, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if got := out["result"].(map[string]any)["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("known version answered with %v", got)
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	if got := out["result"].(map[string]any)["protocolVersion"]; got != protocolVersions[0] {
		t.Fatalf("unknown version answered with %v", got)
	}
}

func TestToolsFollowScopes(t *testing.T) {
	s := testServer()
	_, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools = %v", tools)
	}
	if ann := tools[0].(map[string]any)["annotations"].(map[string]any); ann["readOnlyHint"] != true {
		t.Fatalf("annotations = %v", ann)
	}

	_, out = post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"hidden"}}`)
	if out["error"] == nil {
		t.Fatal("a tool outside the grant's scopes ran")
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"say":"hi"}}}`)
	res := out["result"].(map[string]any)
	if res["structuredContent"].(map[string]any)["said"] != "hi" || res["isError"] != nil {
		t.Fatalf("call = %v", res)
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"say":"fail"}}}`)
	if out["result"].(map[string]any)["isError"] != true {
		t.Fatalf("tool error = %v", out)
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"bogus":1}}}`)
	if out["result"].(map[string]any)["isError"] != true {
		t.Fatalf("unknown argument = %v", out)
	}
	w, out := post(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":{"say":"crash"}}}`)
	if out["error"] == nil || strings.Contains(w.Body.String(), "/secret") {
		t.Fatalf("internal error leaked: %s", w.Body.String())
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`)
	if out["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Fatalf("unknown method = %v", out)
	}
}
