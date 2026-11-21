package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/booktext"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/readium/go-toolkit/pkg/manifest"
	readiumurl "github.com/readium/go-toolkit/pkg/util/url"
)

const (
	mcpPublicURL = "https://sync.example"
	mcpRedirect  = "https://app.example/cb"
	mcpVerifier  = "a-very-long-pkce-verifier-that-is-at-least-43-characters-long"
	mcpClientID  = "lsc_test"
)

// enableMCP turns the assistant endpoint on for the fixture and serves
// it from a fresh listener, since routes are fixed when they are built.
func (f *folderFixture) enableMCP(t *testing.T, tune func(*config.MCPConfig)) {
	t.Helper()
	f.srv.Cfg.MCP.Enabled = true
	f.srv.Cfg.MCP.PublicURL = mcpPublicURL
	if tune != nil {
		tune(&f.srv.Cfg.MCP)
	}
	f.srv.OAuth = oauth.New(f.st, f.srv.Cfg)
	f.srv.OAuthLimiter = auth.NewRateLimiter(1000, time.Minute)
	f.srv.MCPLimiter = auth.NewRateLimiter(1000, time.Minute)
	if err := f.st.CreateOAuthClient(t.Context(), store.OAuthClient{
		ID: mcpClientID, Kind: store.OAuthClientRegistered, Name: "Test assistant",
		RedirectURIs: []string{mcpRedirect}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(f.srv.Routes())
	t.Cleanup(ts.Close)
	f.ts = ts
}

// assistantToken runs the authorization code flow for userID: consent
// through the OAuth server, then the real token endpoint.
func (f *folderFixture) assistantToken(t *testing.T, userID string, scopes ...string) string {
	t.Helper()
	if len(scopes) == 0 {
		scopes = oauth.AllScopes()
	}
	sum := sha256.Sum256([]byte(mcpVerifier))
	req, err := f.srv.OAuth.ParseAuthorize(t.Context(), url.Values{
		"response_type": {"code"}, "client_id": {mcpClientID}, "redirect_uri": {mcpRedirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"resource": {mcpPublicURL + "/mcp"}, "state": {"s"}, "scope": {strings.Join(scopes, " ")},
	})
	if err != nil {
		t.Fatal(err)
	}
	to, err := f.srv.OAuth.Approve(t.Context(), req, userID, scopes)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {mcpClientID},
		"redirect_uri": {mcpRedirect}, "code_verifier": {mcpVerifier}, "resource": {mcpPublicURL + "/mcp"},
	}
	resp, err := http.PostForm(f.ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	access, _ := body["access_token"].(string)
	if resp.StatusCode != http.StatusOK || access == "" {
		t.Fatalf("token exchange: %d %v", resp.StatusCode, body)
	}
	return access
}

// rpc posts one JSON-RPC request to /mcp and returns the status and raw body.
func (f *folderFixture) rpc(t *testing.T, token, method string, params any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/mcp", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// call runs a tool and returns its structured result, or the error text
// when the tool refused.
func (f *folderFixture) call(t *testing.T, token, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	status, body := f.rpc(t, token, "tools/call", map[string]any{"name": tool, "arguments": args})
	if status != http.StatusOK {
		t.Fatalf("%s: %d %s", tool, status, body)
	}
	var out struct {
		Result struct {
			IsError bool             `json:"isError"`
			Content []map[string]any `json:"content"`
			Out     map[string]any   `json:"structuredContent"`
		} `json:"result"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		return nil, out.Error["message"].(string)
	}
	if out.Result.IsError {
		text, _ := out.Result.Content[0]["text"].(string)
		return nil, text
	}
	return out.Result.Out, ""
}

func (f *folderFixture) mustCall(t *testing.T, token, tool string, args map[string]any) map[string]any {
	t.Helper()
	out, errText := f.call(t, token, tool, args)
	if errText != "" {
		t.Fatalf("%s: %s", tool, errText)
	}
	return out
}

// chapterIndex finds the chapter titled title.
func (f *folderFixture) chapterIndex(t *testing.T, token, bookID, title string) int {
	t.Helper()
	out := f.mustCall(t, token, "list_chapters", map[string]any{"book_id": bookID})
	for _, c := range out["chapters"].([]any) {
		c := c.(map[string]any)
		if c["title"] == title {
			return int(c["chapter"].(float64))
		}
	}
	t.Fatalf("no chapter %q in %v", title, out)
	return -1
}

func TestMCPIsAbsentUnlessEnabled(t *testing.T) {
	f := newFolderFixture(t)
	for _, path := range []string{"/mcp", "/oauth/token"} {
		if resp, _ := f.postRaw(t, path, "", "{}"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp"} {
		if resp, _ := f.get(t, path, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestMCPRefusesEverythingButAnAssistantToken(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	for name, token := range map[string]string{
		"none":      "",
		"api token": f.token,
		"garbage":   "lsa_nope",
	} {
		status, body := f.rpc(t, token, "tools/list", nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	resp, _ := f.req(t, http.MethodPost, "/mcp", "")
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `resource_metadata="`+mcpPublicURL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Errorf("challenge = %q", got)
	}
	access := f.assistantToken(t, f.user.ID)
	if resp, _ := f.get(t, "/v1/heads", access); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an assistant token opened the native API: %d", resp.StatusCode)
	}
}

func TestMCPToolsFollowGrantedScopes(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)

	status, body := f.rpc(t, access, "tools/list", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"read_chapter"`) ||
		strings.Contains(string(body), `"search_library"`) || strings.Contains(string(body), `"get_reading_status"`) {
		t.Fatalf("tools/list: %d %s", status, body)
	}
	if _, errText := f.call(t, access, "search_library", map[string]any{"query": "moby"}); errText == "" {
		t.Error("a tool outside the grant ran")
	}
	_, errText := f.call(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current"})
	if !strings.Contains(errText, oauth.ScopeReading) {
		t.Errorf("the reader's position without the reading scope: %q", errText)
	}
}

func TestMCPWorkStatsRespectReadingScope(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUB(t))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, body := f.postRaw(t, "/v1/ops", f.token, `{"ops":[
		{"op_id":"position","work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		 "client_ts":"2024-01-01T12:00:00Z","progression":0.4,"coverage":"partial"},
		{"op_id":"status","work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		 "client_ts":"2024-01-01T12:01:00Z","progression":0.4,"kind":"status","reading_status":"paused"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	start := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{{
		UserID: f.user.ID, SessionID: "session", WorkID: resolved.WorkID, DeviceID: "device",
		StartedAt: start, EndedAt: start.Add(time.Hour), StartProg: 0.1, EndProg: 0.4, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		scopes         []string
		readingAllowed bool
	}{
		{"insights only", []string{oauth.ScopeInsights}, false},
		{"insights and reading", []string{oauth.ScopeInsights, oauth.ScopeReading}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := f.assistantToken(t, f.user.ID, tc.scopes...)
			list := f.mustCall(t, access, "get_work_stats", map[string]any{"range": "all"})
			works := list["works"].([]any)
			if len(works) != 1 {
				t.Fatalf("works: %v", works)
			}
			one := f.mustCall(t, access, "get_work_stats", map[string]any{"work_id": resolved.WorkID, "range": "all"})
			for _, work := range []map[string]any{works[0].(map[string]any), one} {
				if work["sessions"] != float64(1) || work["total_active_minutes"] != float64(60) {
					t.Fatalf("missing statistics: %v", work)
				}
				for _, key := range []string{"current_progression", "status", "eta_seconds", "covers_narrative"} {
					if _, ok := work[key]; ok != tc.readingAllowed {
						t.Fatalf("%s present=%v, reading allowed=%v: %v", key, ok, tc.readingAllowed, work)
					}
				}
				if tc.readingAllowed && (work["current_progression"] != 0.4 || work["status"] != "paused" || work["eta_seconds"] == nil || work["covers_narrative"] != false) {
					t.Fatalf("wrong reading fields: %v", work)
				}
			}
		})
	}
}

func TestMCPStatsSummaryDateOrder(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	access := f.assistantToken(t, f.user.ID, oauth.ScopeInsights)
	if _, msg := f.call(t, access, "get_stats_summary", map[string]any{"from": "2024-02-01", "to": "2024-01-31"}); msg == "" {
		t.Fatal("reversed dates accepted")
	}
	f.mustCall(t, access, "get_stats_summary", map[string]any{"from": "2024-02-01", "to": "2024-02-01"})
}

func TestMCPReadingStatusLastReadMatchesListing(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, body := f.postRaw(t, "/v1/ops", f.token, `{"ops":[
		{"op_id":"offline-position","work_id":"`+resolved.WorkID+`","client_ts":"2020-01-01T00:00:00Z","progression":0.4},
		{"op_id":"status-update","work_id":"`+resolved.WorkID+`","client_ts":"2020-01-02T00:00:00Z","progression":0.4,"kind":"status","reading_status":"paused"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeReading)
	list := f.mustCall(t, access, "get_reading_status", map[string]any{})["works"].([]any)
	one := f.mustCall(t, access, "get_reading_status", map[string]any{"work_id": resolved.WorkID})
	if len(list) != 1 || list[0].(map[string]any)["last_read_at"] == nil || one["last_read_at"] != list[0].(map[string]any)["last_read_at"] {
		t.Fatalf("list %v, single %v", list, one)
	}
}

func TestMCPCalendarReportsSnapshotRevision(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	_, resolved := f.resolveBook(t, bookID, f.token)
	access := f.assistantToken(t, f.user.ID, oauth.ScopeInsights)
	first := f.mustCall(t, access, "get_reading_calendar", map[string]any{"from": "2024-01-01", "to": "2024-01-31"})
	second := f.mustCall(t, access, "get_reading_calendar", map[string]any{"from": "2024-02-01", "to": "2024-02-29"})
	revision, ok := first["stats_revision"].(string)
	if _, err := strconv.ParseInt(revision, 10, 64); !ok || err != nil || revision != second["stats_revision"] || first["timezone"] != second["timezone"] {
		t.Fatalf("calendar snapshots: %v, %v", first, second)
	}
	start := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{{
		UserID: f.user.ID, SessionID: "calendar-session", WorkID: resolved.WorkID, DeviceID: "device",
		StartedAt: start, EndedAt: start.Add(time.Hour), StartProg: 0.1, EndProg: 0.4, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	changed := f.mustCall(t, access, "get_reading_calendar", map[string]any{"from": "2024-01-01", "to": "2024-01-31"})
	if changed["stats_revision"] == revision || len(changed["days"].([]any)) != 1 {
		t.Fatalf("changed calendar: %v", changed)
	}
}

func TestMCPVocabularyRespectsLimitAcrossFilteredPages(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	now := time.Now().UTC()
	words := []store.VocabularyWordWrite{
		{Language: "fr", Normalized: "bonjour", Word: "bonjour", AddedAt: now, OccurrencesJSON: []byte("[]")},
		{Language: "en", Normalized: "one", Word: "one", AddedAt: now, OccurrencesJSON: []byte("[]")},
		{Language: "en", Normalized: "two", Word: "two", AddedAt: now, OccurrencesJSON: []byte("[]")},
		{Language: "en", Normalized: "three", Word: "three", AddedAt: now, OccurrencesJSON: []byte("[]")},
	}
	results, err := f.st.PushVocabularyWords(t.Context(), f.user.ID, "device", words, 50000)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Status != "applied" {
			t.Fatalf("vocabulary write: %+v", result)
		}
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeVocabulary)
	first := f.mustCall(t, access, "list_vocabulary", map[string]any{"language": "en", "limit": 2})
	got := first["words"].([]any)
	cursor, ok := first["next_cursor"].(string)
	if len(got) != 2 || !ok || got[0].(map[string]any)["word"] != "one" || got[1].(map[string]any)["word"] != "two" {
		t.Fatalf("first page: %v", first)
	}
	second := f.mustCall(t, access, "list_vocabulary", map[string]any{"language": "en", "limit": 2, "cursor": cursor})
	got = second["words"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["word"] != "three" || second["next_cursor"] != nil {
		t.Fatalf("second page: %v", second)
	}
}

func TestMCPVocabularyBoundsReviewScan(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	now := time.Now().UTC()
	_, err := f.st.PushVocabularyWords(t.Context(), f.user.ID, "device", []store.VocabularyWordWrite{{
		Language: "en", Normalized: "word", Word: "word", AddedAt: now, OccurrencesJSON: []byte("[]"),
	}}, 50000)
	if err != nil {
		t.Fatal(err)
	}
	var reviews []store.VocabularyReviewWrite
	for i := range 40 {
		reviews = append(reviews, store.VocabularyReviewWrite{ReviewID: fmt.Sprintf("review-%d", i), Language: "en", Normalized: "word", WordGeneration: 1, Rating: "GOOD", ReviewedAt: now, IntervalMinutes: 10, DueAt: now.Add(time.Hour)})
	}
	results, err := f.st.PushVocabularyReviews(t.Context(), f.user.ID, "device", reviews)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != "applied" {
			t.Fatalf("review: %+v", r)
		}
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeVocabulary)
	first := f.mustCall(t, access, "list_vocabulary", map[string]any{"limit": 1})
	cursor := first["next_cursor"].(string)
	second := f.mustCall(t, access, "list_vocabulary", map[string]any{"limit": 1, "cursor": cursor})
	if len(second["words"].([]any)) != 0 || second["next_cursor"] == nil {
		t.Fatalf("bounded scan: %v", second)
	}
	third := f.mustCall(t, access, "list_vocabulary", map[string]any{"limit": 1, "cursor": second["next_cursor"]})
	if len(third["words"].([]any)) != 0 || third["next_cursor"] != nil {
		t.Fatalf("end of feed: %v", third)
	}
}

func TestMCPReadChapterRequiresIndex(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	access := f.assistantToken(t, f.user.ID)
	for _, args := range []map[string]any{{"book_id": bookID}, {"book_id": bookID, "chapter": nil}} {
		if _, msg := f.call(t, access, "read_chapter", args); !strings.Contains(msg, "chapter is required") {
			t.Fatalf("arguments %v: %q", args, msg)
		}
	}
	f.mustCall(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": 0})
}

func TestMCPReadsOnlyTheGrantedLibrary(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	mine := f.assistantToken(t, f.user.ID)
	theirs := f.assistantToken(t, f.other.ID)

	_, raw := f.rpc(t, mine, "tools/call", map[string]any{"name": "search_library", "arguments": map[string]any{"query": "Moby"}})
	if !strings.Contains(string(raw), bookID) {
		t.Fatalf("search_library: %s", raw)
	}
	if strings.Contains(string(raw), f.root) {
		t.Fatalf("a tool result names the folder's path: %s", raw)
	}
	ch := f.chapterIndex(t, mine, bookID, "Loomings")
	out := f.mustCall(t, mine, "read_chapter", map[string]any{"book_id": bookID, "chapter": ch})
	if !strings.Contains(out["text"].(string), "Call me Ishmael") || out["content_notice"] == nil {
		t.Fatalf("read_chapter: %v", out)
	}

	if err := f.st.UnassignUserFolder(t.Context(), f.other.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	if out := f.mustCall(t, theirs, "search_library", map[string]any{"query": "Moby"}); strings.Contains(asJSON(out), bookID) {
		t.Errorf("a book outside the reader's folders was found: %v", out)
	}
	for _, tool := range []string{"get_book", "list_chapters", "read_chapter"} {
		if _, errText := f.call(t, theirs, tool, map[string]any{"book_id": bookID, "chapter": ch}); errText == "" {
			t.Errorf("%s answered for a book outside the reader's folders", tool)
		}
	}
}

func TestMCPSearchesBeyondFirstFolderPage(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	for i := range 105 {
		id := fmt.Sprintf("early-%03d", i)
		folder := store.Folder{ID: id, Name: "A", RootPath: filepath.Join(f.root, id), Kind: store.FolderPlain}
		if err := f.st.CreateFolder(t.Context(), folder); err != nil {
			t.Fatal(err)
		}
		if err := f.st.AssignUserFolder(t.Context(), f.user.ID, folder.ID); err != nil {
			t.Fatal(err)
		}
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeLibrary)
	out := f.mustCall(t, access, "search_library", map[string]any{"query": "Moby"})
	books := out["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["book_id"] != bookID || out["truncated"] != false {
		t.Fatalf("search beyond first folder page: %v", out)
	}
	now := time.Now().UTC()
	if _, err := f.st.ReconcileFolder(t.Context(), "early-000", []store.ObservedBook{{
		RelativePath: "other.epub", OriginalFilename: "other.epub", MediaType: "application/epub+zip",
		SizeBytes: 1, MTime: now, ContentSHA256: "other-sha", Title: "Moby Another",
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	out = f.mustCall(t, access, "search_library", map[string]any{"query": "Moby", "limit": 1})
	if len(out["books"].([]any)) != 1 || out["truncated"] != true {
		t.Fatalf("matches across folder pages were not marked truncated: %v", out)
	}
}

func TestMCPReadsAroundTheReadersOwnPlaces(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, sha := f.writeBook(t, "moby.epub",
		chapterEPUBWithText(t, "Call me Ishmael. Then Ahab came on deck. Ishmael watched him."))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, raw := f.postRaw(t, "/v1/ops", f.token, `{"ops":[{
		"op_id":"018e6f1a-0000-7000-8000-0000000000e1",
		"work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		"client_ts":"2024-01-01T00:00:00Z","progression":0.5,
		"locator":{"href":"OEBPS/chapter1.xhtml","locations":{"progression":0.5},
		"text":{"before":"Call me Ishmael. Then ","highlight":"Ahab"}}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, raw)
	}
	access := f.assistantToken(t, f.user.ID)

	out := f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current", "before": 0, "after": 20})
	if out["located_by"] != "anchor" || !strings.HasPrefix(out["text"].(string), "Ahab") {
		t.Fatalf("read_at current: %v", out)
	}

	all := f.mustCall(t, access, "find_mentions", map[string]any{"book_id": bookID, "names": []string{"Ishmael"}})
	read := f.mustCall(t, access, "find_mentions", map[string]any{"book_id": bookID, "names": []string{"Ishmael"}, "up_to_current": true})
	if all["total"] != float64(2) || read["total"] != float64(1) || read["stop_located_by"] != "anchor" {
		t.Fatalf("find_mentions: all %v, up to current %v", all, read)
	}
	quoted := read["occurrences"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(quoted, "Ishmael") || strings.Contains(quoted, "Ahab") || strings.Contains(quoted, "came on deck") {
		t.Fatalf("snippet crossed the reading position: %q", quoted)
	}

	chapters := f.mustCall(t, access, "list_chapters", map[string]any{"book_id": bookID})["chapters"].([]any)
	chapter := chapters[f.chapterIndex(t, access, bookID, "Loomings")].(map[string]any)
	page := f.mustCall(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": chapter["chapter"]})
	text := page["text"].(string)
	inside := strings.Index(text, "Ishmael") + 3
	if inside < 3 {
		t.Fatalf("missing Ishmael in %q", text)
	}
	start, end := chapter["start_progression"].(float64), chapter["end_progression"].(float64)
	stop := start + (end-start)*float64(inside)/chapter["length"].(float64)
	partial := f.mustCall(t, access, "find_mentions", map[string]any{
		"book_id": bookID, "names": []string{"Ishmael"}, "up_to_progression": stop,
	})
	if partial["total"] != float64(0) || len(partial["occurrences"].([]any)) != 0 {
		t.Fatalf("match crossing the stop: %v", partial)
	}

	if code, out := post(t, f.ts.URL+"/v1/annotations", f.token, map[string]any{"annotations": []map[string]any{{
		"id": "h1", "base_rev": 0, "work_id": resolved.WorkID, "edition_sha": sha, "kind": "highlight",
		"locator": map[string]any{"href": "OEBPS/chapter1.xhtml",
			"text": map[string]any{"before": "on deck. ", "highlight": "Ishmael watched"}},
		"excerpt": "Ishmael watched", "client_ts": "2025-06-01T10:00:00Z",
	}}}); code != http.StatusOK {
		t.Fatalf("annotation push: %d %v", code, out)
	}
	out = f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "annotation_id": "h1", "before": 0, "after": 20})
	if out["located_by"] != "anchor" || !strings.HasPrefix(out["text"].(string), "Ishmael watched") {
		t.Fatalf("read_at annotation: %v", out)
	}

	other := f.assistantToken(t, f.other.ID)
	if _, errText := f.call(t, other, "read_at", map[string]any{"book_id": bookID, "at": "current"}); errText == "" {
		t.Error("another reader's assistant saw this reader's position")
	}
	if _, errText := f.call(t, other, "read_at", map[string]any{"book_id": bookID, "annotation_id": "h1"}); errText == "" {
		t.Error("another reader's assistant read this reader's highlight")
	}
}

func TestMCPReadAtBounds(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name          string
		before, after int
	}{
		{"before over limit", maxWindowBytes + 1, 1},
		{"after over limit", 1, maxWindowBytes + 1},
		{"overflow", maxInt, maxInt},
		{"negative", -1, 1},
		{"both zero", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, errText := f.call(t, access, "read_at", map[string]any{
				"book_id": bookID, "progression": 0.5, "before": tc.before, "after": tc.after,
			}); errText == "" {
				t.Fatal("invalid window accepted")
			}
		})
	}
	f.mustCall(t, access, "read_at", map[string]any{
		"book_id": bookID, "progression": 0.5, "before": maxWindowBytes, "after": maxWindowBytes,
	})
}

func TestMCPReadAtRejectsTruncatedPosition(t *testing.T) {
	f := newFolderFixture(t)
	f.srv.BookTexts = NewBookTextCache(0)
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUB(t))
	f.srv.BookTexts.put(sha, &booktext.Book{Truncated: true, Chapters: []booktext.Chapter{{Href: "one.xhtml", Text: "early text", StartProg: 0, EndProg: 0.4}}})
	f.enableMCP(t, nil)
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "progression": 0.2})
	if _, msg := f.call(t, access, "read_at", map[string]any{"book_id": bookID, "progression": 0.9}); !strings.Contains(msg, "beyond the extracted") {
		t.Fatalf("truncated position: %q", msg)
	}
}

func TestMCPTruncatedUnknownProgressionIsOmitted(t *testing.T) {
	href := func(name string) manifest.HREF { return manifest.NewHREF(readiumurl.MustURLFromString(name)) }
	idx := &epub.Index{Entries: map[string]epub.IndexEntry{"one.xhtml": {}, "two.xhtml": {}}}
	idx.Manifest.ReadingOrder = manifest.LinkList{{Href: href("one.xhtml")}, {Href: href("two.xhtml")}}
	book, err := booktext.Extract(context.Background(), idx, func(name string) ([]byte, error) {
		if name == "one.xhtml" {
			return []byte("<p>early text here</p>"), nil
		}
		return []byte(strings.Repeat("x", booktext.MaxBookBytes)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f := newFolderFixture(t)
	f.srv.BookTexts = NewBookTextCache(0)
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUB(t))
	f.srv.BookTexts.put(sha, book)
	f.enableMCP(t, nil)
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	chapters := f.mustCall(t, access, "list_chapters", map[string]any{"book_id": bookID})["chapters"].([]any)
	chapter := chapters[0].(map[string]any)
	if chapter["start_progression"] != nil || chapter["end_progression"] != nil {
		t.Fatalf("chapter: %v", chapter)
	}
	passage := f.mustCall(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": 0})
	if passage["progression"] != nil {
		t.Fatalf("passage: %v", passage)
	}
	matches := f.mustCall(t, access, "search_text", map[string]any{"book_id": bookID, "query": "early"})["results"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["progression"] != nil {
		t.Fatalf("matches: %v", matches)
	}
}

func TestMCPSearchIncludesWholeMatch(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "search.epub", chapterEPUBWithText(t, "Mary Jane-"+strings.Repeat("x", 800)))
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	result := f.mustCall(t, access, "search_text", map[string]any{"book_id": bookID, "query": "Mary Jane"})
	hits := result["results"].([]any)
	if len(hits) != 1 || !strings.Contains(hits[0].(map[string]any)["text"].(string), "Mary Jane") {
		t.Fatalf("search match: %v", result)
	}
}

func TestMCPMentionAtReadingBoundary(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUBWithText(t, "Mary Jane! A secret follows."))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, body := f.postRaw(t, "/v1/ops", f.token, `{"ops":[{
		"op_id":"position","work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		"client_ts":"2024-01-01T12:00:00Z","progression":0.5,
		"locator":{"href":"OEBPS/chapter1.xhtml","text":{"highlight":"!"}}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText, oauth.ScopeReading)
	start := f.mustCall(t, access, "find_mentions", map[string]any{
		"book_id": bookID, "names": []string{"Mary Jane"}, "up_to_progression": 0,
	})
	if start["total"] != float64(0) || len(start["occurrences"].([]any)) != 0 {
		t.Fatalf("mentions at start: %v", start)
	}
	result := f.mustCall(t, access, "find_mentions", map[string]any{
		"book_id": bookID, "names": []string{"Mary Jane"}, "up_to_current": true,
	})
	occurrences := result["occurrences"].([]any)
	if len(occurrences) != 1 {
		t.Fatalf("mentions: %v", result)
	}
	quote := occurrences[0].(map[string]any)["text"].(string)
	if !strings.Contains(quote, "Mary Jane") || strings.Contains(quote, "!") || strings.Contains(quote, "secret") {
		t.Fatalf("mention at boundary: %q", quote)
	}
}

func TestMCPReadsAtAKOReaderPosition(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
	book, err := f.st.CatalogBookByID(t.Context(), "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	// A reader who only syncs from KOReader has no link to the catalog
	// book, only a work carrying the file's fingerprint.
	f.readOnX3(t, f.user.ID, book.PartialMD5)
	access := f.assistantToken(t, f.user.ID)
	out := f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current", "before": 0, "after": 7})
	if out["located_by"] != "kosync_xpath" || out["text"] != "Ishmael" {
		t.Fatalf("read_at: %v", out)
	}

	collisionFolder := store.Folder{ID: store.NewID(), Name: "Collision", RootPath: t.TempDir(), Kind: store.FolderPlain}
	if err := f.st.CreateFolder(t.Context(), collisionFolder); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := f.st.ReconcileFolder(t.Context(), collisionFolder.ID, []store.ObservedBook{{
		RelativePath: "other.epub", OriginalFilename: "other.epub", MediaType: "application/epub+zip",
		SizeBytes: 1, MTime: now, ContentSHA256: "different-sha", PartialMD5: book.PartialMD5, Title: "Other",
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.other.ID, collisionFolder.ID); err != nil {
		t.Fatal(err)
	}
	if out := f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current"}); out["located_by"] != "kosync_xpath" {
		t.Fatalf("collision outside the reader's folders: %v", out)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, collisionFolder.ID); err != nil {
		t.Fatal(err)
	}
	if out := f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current"}); out["located_by"] != "book_fraction" {
		t.Fatalf("ambiguous fingerprint: %v", out)
	}
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, collisionFolder.ID); err != nil {
		t.Fatal(err)
	}
	if out := f.mustCall(t, access, "read_at", map[string]any{"book_id": bookID, "at": "current"}); out["located_by"] != "kosync_xpath" {
		t.Fatalf("removed collision: %v", out)
	}
}

func TestMCPLocatorNeedsEditionProof(t *testing.T) {
	text := &booktext.Book{Chapters: []booktext.Chapter{{
		Href: "chapter.xhtml", Text: "Ishmael found Ahab", StartProg: 0, EndProg: 1,
	}}}
	book := store.CatalogBook{ContentSHA256: "edition"}
	locator := []byte(`{"href":"chapter.xhtml","text":{"highlight":"Ahab"}}`)
	correct, other := "edition", "other"
	for _, tc := range []struct {
		name    string
		edition *string
		want    string
	}{
		{"matching edition", &correct, "anchor"},
		{"different edition", &other, "book_fraction"},
		{"unknown edition", nil, "book_fraction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progression := 0.25
			pl, err := (&mcpService{}).locate(t.Context(), book, text, tc.edition, locator, &progression, nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.by != tc.want {
				t.Fatalf("location: %+v", pl)
			}
		})
	}
	if _, err := (&mcpService{}).locate(t.Context(), book, text, &other, locator, nil, nil); err == nil {
		t.Fatal("unresolvable locator without progression accepted")
	}
	zero := 0.0
	if _, err := (&mcpService{}).locate(t.Context(), book, text, &other, locator, &zero, nil); err != nil {
		t.Fatalf("explicit zero: %v", err)
	}
}

func TestMCPBookTextIsBudgeted(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, func(c *config.MCPConfig) {
		c.TextBytesPerCall = 1024
		c.TextBytesPerHour = 2500
	})
	bookID, _ := f.writeBook(t, "long.epub", chapterEPUBWithText(t, strings.Repeat("Ishmael went to sea. ", 400)))
	access := f.assistantToken(t, f.user.ID)
	ch := f.chapterIndex(t, access, bookID, "Loomings")

	total, cursor := 0, ""
	for range 3 {
		args := map[string]any{"book_id": bookID, "chapter": ch}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out := f.mustCall(t, access, "read_chapter", args)
		n := len(out["text"].(string))
		if n > 1024 || n == 0 {
			t.Fatalf("one call returned %d bytes", n)
		}
		total += n
		cursor, _ = out["next_cursor"].(string)
	}
	if total > 2500 {
		t.Fatalf("returned %d bytes against an hourly budget of 2500", total)
	}
	_, errText := f.call(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": ch, "cursor": cursor})
	if !strings.Contains(errText, "allowance is spent") {
		t.Fatalf("spent budget: %q", errText)
	}
	// The budget belongs to the grant, so a second connection starts afresh.
	fresh := f.assistantToken(t, f.user.ID)
	f.mustCall(t, fresh, "read_chapter", map[string]any{"book_id": bookID, "chapter": ch})
}

func TestMCPCachedTextRequiresAvailableFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(string) error
	}{
		{"removed", os.Remove},
		{"replaced", func(path string) error { return os.WriteFile(path, []byte("changed"), 0600) }},
		{"symlink", func(path string) error {
			if err := os.Rename(path, path+".saved"); err != nil {
				return err
			}
			return os.Symlink(path+".saved", path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFolderFixture(t)
			f.enableMCP(t, nil)
			bookID, _ := f.writeBook(t, "moby.epub", chapterEPUB(t))
			access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
			chapter := f.chapterIndex(t, access, bookID, "Loomings")
			if err := tc.change(filepath.Join(f.root, "moby.epub")); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"list_chapters", "read_chapter"} {
				args := map[string]any{"book_id": bookID}
				if tool == "read_chapter" {
					args["chapter"] = chapter
				}
				if _, errText := f.call(t, access, tool, args); !strings.Contains(errText, "not available") {
					t.Errorf("%s after file %s: %q", tool, tc.name, errText)
				}
			}
		})
	}
}

func TestMCPTextRejectsStalePublicationIndex(t *testing.T) {
	f := newFolderFixture(t)
	f.srv.PublicationIndexes = NewPublicationIndexCache(0)
	f.enableMCP(t, nil)
	bookID, digest := f.writeBook(t, "moby.epub", chapterEPUB(t))
	if resp, body := f.get(t, "/v1/books/"+bookID+"/publication/manifest.json", f.token); resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest: %d %s", resp.StatusCode, body)
	}
	idx, ok := f.srv.PublicationIndexes.Get(digest)
	if !ok {
		t.Fatal("publication index was not cached")
	}
	entry := idx.Entries["OEBPS/chapter1.xhtml"]
	entry.Offset++
	idx.Entries["OEBPS/chapter1.xhtml"] = entry
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	if _, errText := f.call(t, access, "list_chapters", map[string]any{"book_id": bookID}); !strings.Contains(errText, "file changed") {
		t.Fatalf("stale index: %q", errText)
	}
	if _, ok := f.srv.PublicationIndexes.Get(digest); ok {
		t.Fatal("stale index was not evicted")
	}
	f.mustCall(t, access, "list_chapters", map[string]any{"book_id": bookID})
}

func TestMCPConcurrentBookTextBudget(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, func(c *config.MCPConfig) {
		c.TextBytesPerCall = 1024
		c.TextBytesPerHour = 2500
	})
	bookID, _ := f.writeBook(t, "long.epub", chapterEPUBWithText(t, strings.Repeat("Ishmael went to sea. ", 400)))
	access := f.assistantToken(t, f.user.ID)
	ch := f.chapterIndex(t, access, bookID, "Loomings")
	const calls = 32
	start := make(chan struct{})
	results := make(chan int, calls)
	for range calls {
		go func() {
			n := 0
			defer func() { results <- n }()
			<-start
			out, errText := f.call(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": ch})
			if errText == "" {
				n = len(out["text"].(string))
			}
		}()
	}
	close(start)
	total := 0
	for range calls {
		total += <-results
	}
	if total == 0 || total > 2500 {
		t.Fatalf("concurrent calls returned %d bytes against an hourly budget of 2500", total)
	}
}

func TestMCPReadChapterRefusesIncompleteCharacter(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, func(c *config.MCPConfig) {
		c.TextBytesPerCall = 1024
		c.TextBytesPerHour = 1025 + int64(len("Loomings"))
	})
	prefix := "Loomings\nChapter one.\n"
	body := strings.Repeat("a", 1024-len(prefix)) + "éclair"
	bookID, _ := f.writeBook(t, "moby.epub", chapterEPUBWithText(t, body))
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	chapter := f.chapterIndex(t, access, bookID, "Loomings")
	first := f.mustCall(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": chapter})
	if len(first["text"].(string)) != 1024 || first["next_cursor"] != "1024" {
		t.Fatalf("first page: %v", first)
	}
	out, errText := f.call(t, access, "read_chapter", map[string]any{"book_id": bookID, "chapter": chapter, "cursor": first["next_cursor"]})
	if out != nil || !strings.Contains(errText, "allowance is too low") {
		t.Fatalf("incomplete character: %v %q", out, errText)
	}
}

func TestMCPChapterTitlesCountTowardsTextLimits(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.writeBook(t, "moby.epub", chapterEPUB(t))
	title := strings.Repeat("é", booktext.MaxTitleBytes/2)
	text := &booktext.Book{}
	for i := range 12 {
		text.Chapters = append(text.Chapters, booktext.Chapter{
			Index: i, Href: fmt.Sprintf("chapter%d.xhtml", i), Title: title,
			Text: "Alice went home.", StartProg: float64(i) / 12, EndProg: float64(i+1) / 12,
		})
	}
	f.srv.BookTexts = NewBookTextCache(0)
	f.srv.BookTexts.put(digest, text)
	f.enableMCP(t, func(c *config.MCPConfig) {
		c.TextBytesPerCall = 1024
		c.TextBytesPerHour = 2048
	})
	titleBytes := func(t *testing.T, v map[string]any, key string) int {
		t.Helper()
		s, _ := v[key].(string)
		if len(s) > booktext.MaxTitleBytes {
			t.Fatalf("unbounded title: %d bytes", len(s))
		}
		return len(s)
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
	for range 2 {
		out := f.mustCall(t, access, "list_chapters", map[string]any{"book_id": bookID})
		used := 0
		for _, v := range out["chapters"].([]any) {
			used += titleBytes(t, v.(map[string]any), "title")
		}
		if used != 1024 || out["titles_truncated"] != true {
			t.Fatalf("chapter titles: %d bytes, %v", used, out)
		}
	}
	if _, errText := f.call(t, access, "list_chapters", map[string]any{"book_id": bookID}); !strings.Contains(errText, "allowance is spent") {
		t.Fatalf("titles were not charged: %q", errText)
	}
	for _, tool := range []string{"book_outline", "read_chapter", "read_at", "search_text", "find_mentions"} {
		t.Run(tool, func(t *testing.T) {
			access := f.assistantToken(t, f.user.ID, oauth.ScopeText)
			args := map[string]any{"book_id": bookID}
			switch tool {
			case "read_chapter":
				args["chapter"] = 0
			case "read_at":
				args["progression"] = 0.5
			case "search_text":
				args["query"] = "Alice"
			case "find_mentions":
				args["names"] = []string{"Alice"}
			}
			out := f.mustCall(t, access, tool, args)
			used := lenString(out["text"]) + titleBytes(t, out, "chapter_title")
			for _, key := range []string{"chapters", "results", "occurrences", "by_chapter"} {
				list, _ := out[key].([]any)
				for _, v := range list {
					entry := v.(map[string]any)
					used += lenString(entry["text"]) + lenString(entry["opening"])
					used += titleBytes(t, entry, "title") + titleBytes(t, entry, "chapter_title")
				}
			}
			if used == 0 || used > 1024 {
				t.Fatalf("%s returned %d bytes of book text", tool, used)
			}
		})
	}
}

func TestMCPReadAtMultibyteWindowRespectsLimit(t *testing.T) {
	f := newFolderFixture(t)
	f.enableMCP(t, func(c *config.MCPConfig) {
		c.TextBytesPerCall = 1024
		c.TextBytesPerHour = 2048
	})
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUBWithText(t, "éSECRET"+strings.Repeat("a", 2048)))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, body := f.postRaw(t, "/v1/ops", f.token, `{"ops":[{
		"op_id":"position","work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		"client_ts":"2024-01-01T12:00:00Z","progression":0.5,
		"locator":{"href":"OEBPS/chapter1.xhtml","text":{"highlight":"SECRET"}}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	access := f.assistantToken(t, f.user.ID, oauth.ScopeText, oauth.ScopeReading)
	out := f.mustCall(t, access, "read_at", map[string]any{
		"book_id": bookID, "at": "current", "before": 1, "after": 1023,
	})
	if used := lenString(out["text"]) + lenString(out["chapter_title"]); used > 1024 {
		t.Fatalf("read_at returned %d bytes of book text", used)
	}
}

func lenString(v any) int {
	s, _ := v.(string)
	return len(s)
}

func asJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
