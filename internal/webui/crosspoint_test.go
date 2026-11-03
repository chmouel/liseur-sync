package webui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/adapter/kosync"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestCrossPointPluginDownload(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	const password = "original-pairing-password"
	const slot = "CrossPoint New"
	key := auth.KosyncUserKey(password)
	if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: "u1", DeviceSlot: slot, KeySHA256: auth.HashSecret(key)}); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, body)
	route := "/ui/kosync/" + url.PathEscape(slot) + "/crosspoint"
	for _, form := range []url.Values{{"password": {password}}, {"csrf": {csrf}, "password": {"wrong"}}} {
		if code, _ := postForm(t, ts, cookie, route, form); code != http.StatusForbidden {
			t.Fatalf("invalid setup: %d", code)
		}
	}
	devices, err := st.ListKosyncDevices(ctx, "u1")
	if err != nil || devices[0].MeasuredSessionsFrom != nil {
		t.Fatalf("failed setup activated slot: %+v %v", devices, err)
	}
	download := func() []byte {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+route, strings.NewReader(url.Values{"csrf": {csrf}, "password": {password}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		resp, err := noRedirect().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("download: %d %v", resp.StatusCode, err)
		}
		return data
	}
	data := download()
	devices, err = st.ListKosyncDevices(ctx, "u1")
	if err != nil || devices[0].MeasuredSessionsFrom == nil {
		t.Fatalf("setup did not activate: %+v %v", devices, err)
	}
	cutoff := *devices[0].MeasuredSessionsFrom
	download()
	devices, err = st.ListKosyncDevices(ctx, "u1")
	if err != nil || !devices[0].MeasuredSessionsFrom.Equal(cutoff) {
		t.Fatalf("download moved cutoff: %+v %v", devices, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range archive.File {
		reader, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name], err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) != 3 || len(files["liseur-stats/README.txt"]) == 0 {
		t.Fatalf("plugin files: %v", files)
	}
	var cfg map[string]string
	if err := json.Unmarshal(files["liseur-stats/config.json"], &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["server"] != ts.URL || cfg["username"] != slot || cfg["key"] != key || bytes.Contains(data, []byte(password)) {
		t.Fatal("incorrect private configuration")
	}
	var manifest struct {
		Config struct {
			File string `json:"file"`
		} `json:"config"`
		Events map[string]struct {
			Connect bool `json:"connect"`
			Request struct {
				Method  string            `json:"method"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
				Body    string            `json:"body"`
			} `json:"request"`
		} `json:"events"`
	}
	if err := json.Unmarshal(files["liseur-stats/device.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	event := manifest.Events["reader.session"]
	if len(manifest.Events) != 1 || !event.Connect || manifest.Config.File != "config.json" || event.Request.Method != "POST" || event.Request.URL != "{cfg.server}/adapter/kosync/sessions" || event.Request.Headers["x-auth-key"] != "{cfg.key}" || !strings.Contains(event.Request.Body, `"event_ts":"{event.ts}"`) || strings.Contains(event.Request.Body, "{event.book}") {
		t.Fatalf("invalid subscription: %+v", manifest)
	}
	start := cutoff.Add(time.Second).Unix()
	replacements := []string{
		"{event.id}", "firmware-event-1", "{event.document}", "0123456789abcdef0123456789abcdef",
		"{event.start_time}", strconv.FormatInt(start, 10), "{event.end_time}", strconv.FormatInt(start+60, 10),
		"{event.ts}", strconv.FormatInt(start+120, 10), "{event.duration_seconds}", "60",
		"{event.start_progress_bp}", "0", "{event.end_progress_bp}", "100", "{event.progress_scale}", "10000",
	}
	for name, value := range cfg {
		replacements = append(replacements, "{cfg."+name+"}", value)
	}
	expand := strings.NewReplacer(replacements...)
	request := httptest.NewRequest(event.Request.Method, expand.Replace(event.Request.URL), strings.NewReader(expand.Replace(event.Request.Body)))
	for name, value := range event.Request.Headers {
		request.Header.Set(name, expand.Replace(value))
	}
	response := httptest.NewRecorder()
	adapter := kosync.Server{St: st}
	adapter.HandleSession(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("configured event upload: %d %s", response.Code, response.Body.String())
	}
	sessions, err := st.SessionsInRange(ctx, "u1", cutoff, cutoff.Add(time.Hour))
	if err != nil || len(sessions) != 1 || sessions[0].ActiveMs == nil || *sessions[0].ActiveMs != 60000 || sessions[0].EndedAt.Unix() != start+120 {
		t.Fatalf("configured session: %+v %v", sessions, err)
	}

	_, body = page(t, ts, cookie, "/ui/settings?section=devices")
	if !strings.Contains(body, "Measured statistics enabled") {
		t.Fatal("active mode not displayed")
	}
	if err := st.CreateUser(ctx, store.User{ID: "other", Name: "other", Argon2Hash: "x", KosyncEnabled: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: "other", DeviceSlot: "other", KeySHA256: auth.HashSecret(auth.KosyncUserKey("other-password"))}); err != nil {
		t.Fatal(err)
	}
	if code, _ := postForm(t, ts, cookie, "/ui/kosync/other/crosspoint", url.Values{"csrf": {csrf}, "password": {"other-password"}}); code != http.StatusForbidden {
		t.Fatalf("other user's credentials accepted: %d", code)
	}
	if err := st.RevokeKosyncDevice(ctx, "u1", slot); err != nil {
		t.Fatal(err)
	}
	if code, _ := postForm(t, ts, cookie, route, url.Values{"csrf": {csrf}, "password": {password}}); code != http.StatusForbidden {
		t.Fatalf("revoked bundle: %d", code)
	}
}

// TestCrossPointPluginConfigTooLarge: a device name long enough to push
// config.json past CrossPoint's 2KB budget is the person's mistake, not a
// server fault, and it must not activate statistics on the way out.
func TestCrossPointPluginConfigTooLarge(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	const password = "long-name-pairing-password"
	slot := strings.Repeat("very-long-device-name-", 200)
	key := auth.KosyncUserKey(password)
	if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: "u1", DeviceSlot: slot, KeySHA256: auth.HashSecret(key)}); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, body)

	code, text := postForm(t, ts, cookie, "/ui/kosync/"+url.PathEscape(slot)+"/crosspoint",
		url.Values{"csrf": {csrf}, "password": {password}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(text, "shorter name") {
		t.Fatalf("oversized plugin config: %d %q", code, text)
	}
	devices, err := st.ListKosyncDevices(ctx, "u1")
	if err != nil || devices[0].MeasuredSessionsFrom != nil {
		t.Fatalf("a refused bundle activated the slot: %+v %v", devices, err)
	}
}
