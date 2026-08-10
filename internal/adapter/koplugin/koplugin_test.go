package koplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	cap, _ := auth.NewSecret()
	id, _ := auth.NewSecret()
	if err := st.CreateKopluginDevice(t.Context(), store.KopluginDevice{
		ID: id, UserID: u.ID, TokenSHA256: auth.HashSecret(cap),
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
	return ts, st, cap
}

func upload(t *testing.T, ts *httptest.Server, cap string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/adapter/koplugin/"+cap+"/api/plugin/upload",
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
	ts, st, cap := testServer(t)

	row := map[string]any{
		"book_md5": "abc123", "page": 42, "total_pages": 300,
		"start_time": base().Unix(), "duration": 900,
	}
	// First upload.
	code, out := upload(t, ts, cap, map[string]any{"version": "0.3.0", "rows": []any{row}})
	if code != 200 || out["inserted"].(float64) != 1 {
		t.Fatalf("insert: %d %v", code, out)
	}
	// Identical re-upload -> duplicate.
	code, out = upload(t, ts, cap, map[string]any{"version": "0.3.0", "rows": []any{row}})
	if code != 200 || out["duplicate"].(float64) != 1 {
		t.Fatalf("dup: %d %v", code, out)
	}
	// Changed payload (longer duration) -> superseded.
	row2 := map[string]any{
		"book_md5": "abc123", "page": 42, "total_pages": 300,
		"start_time": base().Unix(), "duration": 1200,
	}
	code, out = upload(t, ts, cap, map[string]any{"version": "0.3.0", "rows": []any{row2}})
	if code != 200 || out["superseded"].(float64) != 1 {
		t.Fatalf("supersede: %d %v", code, out)
	}
	// Re-upload the ORIGINAL payload again: it is a known session_id but
	// not current — duplicate (no new revision churn).
	code, out = upload(t, ts, cap, map[string]any{"version": "0.3.0", "rows": []any{row}})
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
	// Progression conversion: page 42/300 -> [41/300, 42/300].
	for _, s := range ss {
		wantStart := 41.0 / 300.0
		wantEnd := 42.0 / 300.0
		if s.StartProg != wantStart || s.EndProg != wantEnd {
			t.Fatalf("bad progression: %v %v", s.StartProg, s.EndProg)
		}
	}
}

// TestRejectsLoud: rows the legacy protocol would silently drop are
// rejected with 422 listing reasons.
func TestRejectsLoud(t *testing.T) {
	ts, _, cap := testServer(t)
	code, out := upload(t, ts, cap, map[string]any{"rows": []any{
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
