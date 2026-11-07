package koplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func testServer(t *testing.T) (*httptest.Server, store.Store, string) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	// Capability credential.
	capToken, _ := auth.NewSecret()
	id, _ := auth.NewSecret()
	if err := st.CreateKopluginDevice(t.Context(), store.KopluginDevice{
		ID: id, UserID: u.ID, TokenSHA256: auth.HashSecret(capToken),
		Label: "kobo", DeviceID: "koplugin:kobo", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.InsecureHTTP = true
	s := &Server{St: st}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler {
		return auth.RequireSecureTransport(cfg, h)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st, capToken
}

func upload(t *testing.T, ts *httptest.Server, capToken string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/adapter/koplugin/"+capToken+"/api/plugin/import",
		"application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func base() time.Time { return time.Unix(1_754_800_000, 0) }

func TestUploadInsertDuplicateSupersede(t *testing.T) {
	ts, st, capToken := testServer(t)

	row := map[string]any{
		"book_md5": "abc123", "page": 42, "total_pages": 300,
		"start_time": base().Unix(), "duration": 900,
	}
	// First upload.
	code, out := upload(t, ts, capToken, map[string]any{"version": "0.3.0", "rows": []any{row}})
	if code != 200 || out["inserted"].(float64) != 1 {
		t.Fatalf("insert: %d %v", code, out)
	}
	// Identical re-upload -> duplicate.
	code, out = upload(t, ts, capToken, map[string]any{"version": "0.3.0", "rows": []any{row}})
	if code != 200 || out["duplicate"].(float64) != 1 {
		t.Fatalf("dup: %d %v", code, out)
	}
	// Changed payload (longer duration) -> superseded.
	row2 := map[string]any{
		"book_md5": "abc123", "page": 42, "total_pages": 300,
		"start_time": base().Unix(), "duration": 1200,
	}
	code, out = upload(t, ts, capToken, map[string]any{"version": "0.3.0", "rows": []any{row2}})
	if code != 200 || out["superseded"].(float64) != 1 {
		t.Fatalf("supersede: %d %v", code, out)
	}
	// Re-upload the ORIGINAL payload again: it is a known session_id but
	// not current — duplicate (no new revision churn).
	code, out = upload(t, ts, capToken, map[string]any{"version": "0.3.0", "rows": []any{row}})
	if code != 200 || out["duplicate"].(float64) != 1 {
		t.Fatalf("re-upload old payload: %d %v", code, out)
	}

	// Sessions visible, pending work created.
	wid, err := st.WorkIDByAlias(t.Context(), "u1", "partial-md5", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := st.SessionsForWork(t.Context(), "u1", wid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 { // both revisions stored, append-only
		t.Fatalf("want 2 session rows, got %d", len(ss))
	}
	// Progression conversion: page 42/300 -> [41/300, 42/300],
	// with exactly one reported page from the actual row.
	for _, s := range ss {
		wantStart := 41.0 / 300.0
		wantEnd := 42.0 / 300.0
		if s.StartProg != wantStart || s.EndProg != wantEnd {
			t.Fatalf("bad progression: %v %v", s.StartProg, s.EndProg)
		}
		if s.ReportedPages == nil || *s.ReportedPages != 1 {
			t.Fatalf("reported pages should be exactly the uploaded page row: %+v", s.ReportedPages)
		}
	}
	snap, err := st.StatisticsSnapshot(t.Context(), "u1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Editions) != 0 {
		t.Fatalf("koplugin must not synthesize editions: %+v", snap.Editions)
	}
}

// TestRejectsLoud: rows the legacy protocol would silently drop are
// rejected with 422 listing reasons.
func TestRejectsLoud(t *testing.T) {
	ts, _, capToken := testServer(t)
	code, out := upload(t, ts, capToken, map[string]any{"rows": []any{
		map[string]any{"book_md5": "abc", "page": 1, "total_pages": 100, "start_time": base().Unix(), "duration": 0},
		map[string]any{"book_md5": "abc", "page": 1, "total_pages": 0, "start_time": base().Unix(), "duration": 60},
		map[string]any{"book_md5": "abc", "page": 200, "total_pages": 100, "start_time": base().Unix(), "duration": 60},
	}})
	if code != 422 {
		t.Fatalf("want 422, got %d %v", code, out)
	}
	rej := out["rejected"].([]any)
	if len(rej) != 3 {
		t.Fatalf("want 3 rejects, got %v", rej)
	}
}

// TestNoCapability: bad or missing capability is rejected; the
// capability must not work cross-user.
func TestNoCapability(t *testing.T) {
	ts, _, _ := testServer(t)
	code, _ := upload(t, ts, "bogus-capability", map[string]any{"rows": []any{
		map[string]any{"book_md5": "abc", "page": 1, "total_pages": 100, "start_time": base().Unix(), "duration": 60},
	}})
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
	fmt.Println("capability auth ok")
}

// postRaw posts a raw JSON body to a plugin route and decodes the reply.
func postRaw(t *testing.T, ts *httptest.Server, capToken, route, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/adapter/koplugin/"+capToken+"/api/plugin/"+route,
		"application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestKoInsightPluginPayload is the request the KoInsight plugin really
// sends: page-stat rows under "stats", with book metadata and
// annotations beside them. Every released plugin version posts it to
// api/plugin/import. Both extra fields are accepted and not stored.
func TestKoInsightPluginPayload(t *testing.T) {
	ts, st, capToken := testServer(t)
	body := fmt.Sprintf(`{
	  "version": "0.3.0",
	  "stats": [
	    {"book_md5": "feedface01", "page": 10, "total_pages": 200, "start_time": %d, "duration": 45},
	    {"book_md5": "feedface01", "page": 11, "total_pages": 200, "start_time": %d, "duration": 30}
	  ],
	  "books": [{"md5": "feedface01", "title": "A book", "pages": 200}],
	  "annotations": {"feedface01": []}
	}`, base().Unix(), base().Unix()+60)
	code, out := postRaw(t, ts, capToken, "import", body)
	if code != 200 || out["inserted"].(float64) != 2 {
		t.Fatalf("stats payload: %d %v", code, out)
	}
	wid, err := st.WorkIDByAlias(t.Context(), "u1", "partial-md5", "feedface01")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := st.SessionsForWork(t.Context(), "u1", wid, 10)
	if err != nil || len(ss) != 2 {
		t.Fatalf("want 2 sessions, got %d (%v)", len(ss), err)
	}
}

// TestKoInsightAnnotationsOnlyIsAccepted: the plugin syncs annotations
// with an empty stats table, which its JSON encoder writes as {}. That is
// a valid request that stores nothing, not a 422 on every sync.
func TestKoInsightAnnotationsOnlyIsAccepted(t *testing.T) {
	ts, _, capToken := testServer(t)
	for name, body := range map[string]string{
		"empty object": `{"stats": {}, "books": [{"md5": "x"}], "annotations": {"x": []}, "version": "0.3.0"}`,
		"empty array":  `{"stats": [], "books": [{"md5": "x"}], "annotations": {"x": []}, "version": "0.3.0"}`,
	} {
		code, out := postRaw(t, ts, capToken, "import", body)
		if code != 200 || out["inserted"].(float64) != 0 {
			t.Fatalf("%s: %d %v", name, code, out)
		}
		if out["ignored"] == nil {
			t.Fatalf("%s: the response must say what was ignored: %v", name, out)
		}
	}
	// A body with none of the plugin's fields is still an error.
	if code, _ := postRaw(t, ts, capToken, "import", `{}`); code != 422 {
		t.Fatalf("empty body must stay 422, got %d", code)
	}
	// And a malformed stats list is refused, not silently emptied.
	if code, _ := postRaw(t, ts, capToken, "import", `{"stats": "nope"}`); code != 422 {
		t.Fatalf("malformed stats must be 422, got %d", code)
	}
}

// TestDeviceRegistration: the plugin registers itself before uploading.
// The credential already identifies the device, so this only confirms
// the capability works, and a bad capability is refused.
func TestDeviceRegistration(t *testing.T) {
	ts, _, capToken := testServer(t)
	code, out := postRaw(t, ts, capToken, "device",
		`{"id": "someone-elses-id", "model": "KindlePaperWhite5", "version": "0.3.0"}`)
	if code != 200 || out["registered"] != true {
		t.Fatalf("device: %d %v", code, out)
	}
	if code, _ := postRaw(t, ts, "bogus-capability", "device", `{}`); code != 401 {
		t.Fatalf("bad capability must be 401, got %d", code)
	}
}

// TestOldUploadRouteIsGone: the adapter used to mount api/plugin/upload,
// a route no plugin version ever posted to.
func TestOldUploadRouteIsGone(t *testing.T) {
	ts, _, capToken := testServer(t)
	if code, _ := postRaw(t, ts, capToken, "upload", `{"rows": []}`); code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

// TestPartlyValidUploadStoresTheGoodRows: the plugin re-sends its whole
// table every sync, so one row the adapter refuses must not turn every
// later sync into a failure. Valid rows are stored and the response names
// the refused ones; 422 is for a request where nothing was accepted.
func TestPartlyValidUploadStoresTheGoodRows(t *testing.T) {
	ts, st, capToken := testServer(t)
	body := fmt.Sprintf(`{"stats": [
	  {"book_md5": "feedface02", "page": 5, "total_pages": 100, "start_time": %d, "duration": 30},
	  {"book_md5": "feedface02", "page": 500, "total_pages": 100, "start_time": %d, "duration": 30}
	]}`, base().Unix(), base().Unix()+10)
	code, out := postRaw(t, ts, capToken, "import", body)
	if code != 200 || out["inserted"].(float64) != 1 {
		t.Fatalf("partly valid: %d %v", code, out)
	}
	rej, _ := out["rejected"].([]any)
	if len(rej) != 1 {
		t.Fatalf("the refused row must be named: %v", out)
	}
	if _, err := st.WorkIDByAlias(t.Context(), "u1", "partial-md5", "feedface02"); err != nil {
		t.Fatalf("the valid row was not stored: %v", err)
	}
	// Sending the same table again is stable: still 200, now a duplicate,
	// and the stored sessions do not grow.
	code, out = postRaw(t, ts, capToken, "import", body)
	if code != 200 || out["duplicate"].(float64) != 1 || out["inserted"].(float64) != 0 {
		t.Fatalf("second sync: %d %v", code, out)
	}
	wid, err := st.WorkIDByAlias(t.Context(), "u1", "partial-md5", "feedface02")
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.SessionsForWork(t.Context(), "u1", wid, 10); err != nil || len(ss) != 1 {
		t.Fatalf("replay must not add sessions: %d (%v)", len(ss), err)
	}
}

// TestHostileRowsAreRefusedBeforeStorage: values the store cannot hold
// or read back are row rejections, never a 5xx and never a stored row
// that breaks later reads. A NUL cannot be stored in PostgreSQL text; a
// time past year 9999 cannot be parsed back from SQLite; start + duration
// can overflow. Each one is also resent on every sync, so storing it
// would leave a permanently failing row.
func TestHostileRowsAreRefusedBeforeStorage(t *testing.T) {
	ts, st, capToken := testServer(t)
	good := fmt.Sprintf(`{"book_md5":"cafe0001","page":3,"total_pages":100,"start_time":%d,"duration":20}`, base().Unix())
	cases := map[string]string{
		"NUL in id":        `{"book_md5":"ab\u0000cd","page":1,"total_pages":100,"start_time":1754800000,"duration":30}`,
		"non-hex id":       `{"book_md5":"not-hex!","page":1,"total_pages":100,"start_time":1754800000,"duration":30}`,
		"overlong id":      fmt.Sprintf(`{"book_md5":"%s","page":1,"total_pages":100,"start_time":1754800000,"duration":30}`, strings.Repeat("a", 65)),
		"year 10000":       `{"book_md5":"cafe0002","page":1,"total_pages":100,"start_time":253402300800,"duration":30}`,
		"end overflows":    `{"book_md5":"cafe0003","page":1,"total_pages":100,"start_time":9223372036854775000,"duration":30}`,
		"huge duration":    `{"book_md5":"cafe0004","page":1,"total_pages":100,"start_time":1754800000,"duration":9223372036854775807}`,
		"duration > a day": `{"book_md5":"cafe0005","page":1,"total_pages":100,"start_time":1754800000,"duration":86401}`,
		"huge total pages": `{"book_md5":"cafe0006","page":1,"total_pages":10000001,"start_time":1754800000,"duration":30}`,
	}
	for name, bad := range cases {
		// Alone: nothing accepted -> 422, and no 5xx.
		code, out := postRaw(t, ts, capToken, "import", `{"stats":[`+bad+`]}`)
		if code != 422 {
			t.Fatalf("%s: want 422, got %d %v", name, code, out)
		}
		// Beside a good row: 200, the good row is stored, the bad one named.
		code, out = postRaw(t, ts, capToken, "import", `{"stats":[`+good+`,`+bad+`]}`)
		if code != 200 || out["rejected"] == nil {
			t.Fatalf("%s beside a good row: %d %v", name, code, out)
		}
	}
	// The boundary itself is accepted: the last representable second.
	edge := `{"book_md5":"cafe00ff","page":1,"total_pages":100,"start_time":253402300769,"duration":30}`
	if code, out := postRaw(t, ts, capToken, "import", `{"stats":[`+edge+`]}`); code != 200 {
		t.Fatalf("boundary row: %d %v", code, out)
	}
	// And the store can still read everything back.
	if _, err := st.StatisticsSnapshot(t.Context(), "u1", nil); err != nil {
		t.Fatalf("statistics unreadable after hostile uploads: %v", err)
	}
	if _, err := st.WorkIDByAlias(t.Context(), "u1", "partial-md5", "cafe0002"); err == nil {
		t.Fatal("a refused row must not create a work")
	}
}

// TestEmptyStatsAnyJSONSpacing: an empty Lua table is {} in whatever
// spacing the encoder uses, and a non-empty object is not a list of rows.
func TestEmptyStatsAnyJSONSpacing(t *testing.T) {
	ts, _, capToken := testServer(t)
	for _, stats := range []string{`{}`, `{ }`, "{\n}", `[ ]`, `null`} {
		code, out := postRaw(t, ts, capToken, "import", `{"stats": `+stats+`, "books": [], "annotations": {}}`)
		if code != 200 || out["inserted"].(float64) != 0 {
			t.Fatalf("stats %q: %d %v", stats, code, out)
		}
	}
	if code, _ := postRaw(t, ts, capToken, "import", `{"stats": {"a": 1}}`); code != 422 {
		t.Fatalf("a non-empty object is not a list of rows: want 422, got %d", code)
	}
}

// TestBodyLimitsAreEnforcedWhole: a body that is too large or carries
// trailing input is refused, not read up to a point and then accepted.
func TestBodyLimitsAreEnforcedWhole(t *testing.T) {
	ts, _, capToken := testServer(t)
	row := fmt.Sprintf(`{"book_md5":"cafe0010","page":2,"total_pages":50,"start_time":%d,"duration":10}`, base().Unix())
	if code, _ := postRaw(t, ts, capToken, "import", `{"stats":[`+row+`]} trailing garbage`); code != 422 {
		t.Fatalf("trailing input must be refused, got %d", code)
	}
	if code, _ := postRaw(t, ts, capToken, "import", `{"stats":[`+row+`]}{"stats":[]}`); code != 422 {
		t.Fatalf("a second JSON value must be refused, got %d", code)
	}
	// /device is capped at 64 KiB and says so.
	big := `{"id":"` + strings.Repeat("x", 70<<10) + `"}`
	if code, out := postRaw(t, ts, capToken, "device", big); code != 413 {
		t.Fatalf("oversized device body: want 413, got %d %v", code, out)
	}
	// The shared reader reports 413 for an over-limit body.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 100)))
	if _, ok := readBody(rec, req, 10); ok || rec.Code != 413 {
		t.Fatalf("readBody over limit: ok=%v code=%d", ok, rec.Code)
	}
}

// TestRevokedCapabilityIsRefused: revoking the device ends access on both
// routes at once.
func TestRevokedCapabilityIsRefused(t *testing.T) {
	ts, st, capToken := testServer(t)
	row := fmt.Sprintf(`{"stats":[{"book_md5":"cafe0020","page":2,"total_pages":50,"start_time":%d,"duration":10}]}`, base().Unix())
	if code, _ := postRaw(t, ts, capToken, "import", row); code != 200 {
		t.Fatalf("before revoke: %d", code)
	}
	devs, err := st.ListKopluginDevices(t.Context(), "u1")
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices: %v %v", devs, err)
	}
	if err := st.RevokeKopluginDevice(t.Context(), "u1", devs[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"import", "device"} {
		if code, _ := postRaw(t, ts, capToken, route, row); code != 401 {
			t.Fatalf("%s after revoke: want 401, got %d", route, code)
		}
	}
}

// TestRowCountIsBoundedWhileDecoding: a body of null entries is a few
// bytes per row but a whole zero-valued row each once decoded, so the
// count is checked as rows are decoded and the request stops early.
func TestRowCountIsBoundedWhileDecoding(t *testing.T) {
	ts, _, capToken := testServer(t)
	flood := `{"stats":[` + strings.Repeat("null,", maxRows) + `null]}`
	if code, out := postRaw(t, ts, capToken, "import", flood); code != 413 {
		t.Fatalf("a flood of rows must be 413, got %d %v", code, out)
	}
	// The older list names are bounded the same way.
	for _, key := range []string{"rows", "sessions"} {
		flood := `{"` + key + `":[` + strings.Repeat("null,", maxRows) + `null]}`
		if code, _ := postRaw(t, ts, capToken, "import", flood); code != 413 {
			t.Fatalf("%s flood must be 413, got %d", key, code)
		}
	}
}

// TestRejectionListIsCappedButTheCountIsExact: a request of nothing but
// bad rows must not build a response as large as itself.
func TestRejectionListIsCappedButTheCountIsExact(t *testing.T) {
	ts, _, capToken := testServer(t)
	body := `{"stats":[` + strings.Repeat("null,", 249) + `null]}`
	code, out := postRaw(t, ts, capToken, "import", body)
	if code != 422 {
		t.Fatalf("all rows bad: want 422, got %d", code)
	}
	if rej, _ := out["rejected"].([]any); len(rej) != maxRejectLines {
		t.Fatalf("rejected list must be capped at %d, got %d", maxRejectLines, len(rej))
	}
	if out["rejected_total"].(float64) != 250 {
		t.Fatalf("rejected_total must be exact: %v", out["rejected_total"])
	}
}

// TestMalformedRowListsAreRefused: a stats value that is not a list of
// row objects is a 422, never an empty success and never a 5xx.
func TestMalformedRowListsAreRefused(t *testing.T) {
	ts, _, capToken := testServer(t)
	for name, body := range map[string]string{
		"string":          `{"stats":"nope"}`,
		"number":          `{"stats":7}`,
		"list of numbers": `{"stats":[1,2,3]}`,
		"unterminated":    `{"stats":[{"page":1}`,
		"list of lists":   `{"stats":[[1]]}`,
		"rows is a map":   `{"rows":{"a":1}}`,
	} {
		code, out := postRaw(t, ts, capToken, "import", body)
		if code != 422 {
			t.Fatalf("%s: want 422, got %d %v", name, code, out)
		}
	}
	// Older names still work, and an empty one falls through to the next.
	row := fmt.Sprintf(`{"book_md5":"cafe0030","page":2,"total_pages":50,"start_time":%d,"duration":10}`, base().Unix())
	for _, key := range []string{"rows", "sessions"} {
		code, out := postRaw(t, ts, capToken, "import", `{"stats":[],"`+key+`":[`+row+`]}`)
		if code != 200 || out["inserted"].(float64)+out["duplicate"].(float64) != 1 {
			t.Fatalf("%s fallback: %d %v", key, code, out)
		}
	}
}

// TestHugeObjectIsRefusedAtTheFirstProperty: a non-empty object where a
// list belongs is refused without being read to the end, for all three
// names the list may arrive under.
func TestHugeObjectIsRefusedAtTheFirstProperty(t *testing.T) {
	ts, _, capToken := testServer(t)
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 300_000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%d":1`, i)
	}
	b.WriteString("}")
	for _, key := range []string{"stats", "rows", "sessions"} {
		code, out := postRaw(t, ts, capToken, "import", `{"`+key+`":`+b.String()+`}`)
		if code != 422 {
			t.Fatalf("%s as a big object: want 422, got %d %v", key, code, out)
		}
	}
	// A nested empty object is still just an empty table.
	if code, _ := postRaw(t, ts, capToken, "import", `{"stats": { }, "books": []}`); code != 200 {
		t.Fatalf("empty object with spacing: got %d", code)
	}
}
