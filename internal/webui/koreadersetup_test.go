package webui

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

func unzipSetup(t *testing.T, data []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		if f.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions: %v", f.Name, f.Mode())
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(body)
	}
	return files
}

// luaSetting returns the value of a `key = "value"` line in the generated
// patch, undoing nothing: the generated values carry no escapes.
func luaSetting(t *testing.T, patch, key string) string {
	t.Helper()
	_, after, ok := strings.Cut(patch, key+" = \"")
	value, _, ok2 := strings.Cut(after, "\"")
	if !ok || !ok2 {
		t.Fatalf("patch has no %s", key)
	}
	return value
}

// credentialLines returns the values after each "label: " prefix, in order.
func credentialLines(text, label string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), label+": "); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestKOReaderSetupDownload(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	_, pageBody := page(t, ts, cookie, "/ui/settings?section=devices")
	csrf := extractCSRF(t, pageBody)
	if !strings.Contains(pageBody, `action="koreader/setup"`) {
		t.Fatal("missing setup form")
	}
	if !strings.Contains(pageBody, `href="https://chmouel.github.io/liseur-sync/koreader/"`) {
		t.Fatal("missing link to the setup guide")
	}
	for _, form := range []url.Values{
		{"name": {"Kobo"}},
		{"csrf": {"wrong"}, "name": {"Kobo"}},
	} {
		if code, _ := postForm(t, ts, cookie, "/ui/koreader/setup", form); code != http.StatusForbidden {
			t.Fatalf("CSRF: %d", code)
		}
	}
	for _, name := range []string{"", "  ", "Web reader", "bad\nname", "bad\x00name", strings.Repeat("a", 101)} {
		if code, _ := postForm(t, ts, cookie, "/ui/koreader/setup", url.Values{"csrf": {csrf}, "name": {name}}); code != http.StatusBadRequest {
			t.Fatalf("invalid name %q: %d", name, code)
		}
	}
	if tokens, _ := st.ListTokens(t.Context(), "u1"); len(tokens) != 0 {
		t.Fatal("refused setup created a token")
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/koreader/setup", strings.NewReader(url.Values{
		"csrf": {csrf}, "name": {`Kobo "L'été"`},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("download: status=%d error=%v", resp.StatusCode, err)
	}
	files := unzipSetup(t, data)
	if len(files) != 3 {
		t.Fatalf("archive holds %d files, want 3", len(files))
	}
	patch := files["patches/1-liseur-setup.lua"]
	creds := files["credentials.txt"]

	apiTokens := credentialLines(creds, "password")
	usernames := credentialLines(creds, "username")
	statsURLs := credentialLines(creds, "KoInsight server URL")
	if len(apiTokens) != 2 || len(usernames) != 2 || len(statsURLs) != 1 {
		t.Fatalf("credentials file shape: %q", creds)
	}
	apiToken, syncPassword := apiTokens[0], apiTokens[1]
	slot := usernames[1]

	tok, err := auth.NewService(st).AuthenticateToken(t.Context(), apiToken)
	if err != nil || tok.UserID != "u1" || len(tok.Scopes) != 1 || !tok.Scopes.Contains(store.ScopeLibraryRead) {
		t.Fatalf("OPDS token must carry library-read only: %+v %v", tok, err)
	}

	userKey := auth.KosyncUserKey(syncPassword)
	if luaSetting(t, patch, "progress.userkey") != userKey || luaSetting(t, patch, "progress.username") != slot {
		t.Fatal("patch and credentials disagree on the Progress sync login")
	}
	if got := luaSetting(t, patch, "progress.custom_server"); got != ts.URL+"/adapter/kosync" {
		t.Fatalf("Progress sync server: %s", got)
	}
	if luaSetting(t, patch, "password") != apiToken {
		t.Fatal("patch and credentials disagree on the OPDS token")
	}

	if !strings.Contains(patch, `.. "/opds/v2"`) || !strings.Contains(patch, "/opdsplus.lua") ||
		!strings.Contains(creds, ts.URL+"/opds/v2") {
		t.Fatal("setup must offer the OPDS 2.0 catalog to OPDS Plus")
	}

	pins := regexp.MustCompile(`\{ "[a-z_]+\.lua", "[0-9a-f]{64}" \}`).FindAllString(patch, -1)
	if len(pins) != 8 || !strings.Contains(patch, "https://raw.githubusercontent.com/Ko-Insight/KoInsight/") {
		t.Fatalf("patch must pin the KoInsight plugin files by checksum: %d entries", len(pins))
	}

	kd, err := st.KosyncDeviceByKey(t.Context(), auth.HashSecret(userKey))
	if err != nil || kd.UserID != "u1" || kd.DeviceSlot != slot || kd.Label != `Kobo "L'été"` || kd.RevokedAt != nil {
		t.Fatalf("Progress sync device: %+v %v", kd, err)
	}
	if _, err := st.KosyncDeviceByKey(t.Context(), auth.HashSecret(auth.KosyncUserKey("wrong"))); err == nil {
		t.Fatal("unrelated key authenticates")
	}

	statsDevices, err := st.ListKopluginDevices(t.Context(), "u1")
	if err != nil || len(statsDevices) != 1 || statsDevices[0].DeviceID != tok.DeviceID {
		t.Fatalf("statistics device: %+v %v", statsDevices, err)
	}
	marker := ts.URL + "/adapter/koplugin/"
	_, after, ok := strings.Cut(statsURLs[0], marker)
	if !ok || !strings.Contains(patch, statsURLs[0]) {
		t.Fatal("statistics URL missing from patch")
	}
	if d, err := st.KopluginDeviceByToken(t.Context(), auth.HashSecret(after)); err != nil || d.ID != statsDevices[0].ID {
		t.Fatalf("statistics capability does not authenticate: %v", err)
	}

	if other, _ := st.ListTokens(t.Context(), "other-user"); len(other) != 0 {
		t.Fatal("setup changed another account")
	}
	if code, body := postForm(t, ts, nil, "/ui/koreader/setup", url.Values{"csrf": {csrf}, "name": {"Other"}}); code != http.StatusSeeOther || strings.Contains(body, apiToken) {
		t.Fatalf("anonymous setup: %d", code)
	}
}

func TestKOReaderSetupNeedsBothAdapters(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
		user   store.UserSettings
	}{
		{"progress sync adapter off", func(c *config.Config) { c.Adapters.Kosync = false }, store.UserSettings{Timezone: "UTC", KosyncEnabled: true, KopluginEnabled: true}},
		{"statistics adapter off", func(c *config.Config) { c.Adapters.Koplugin = false }, store.UserSettings{Timezone: "UTC", KosyncEnabled: true, KopluginEnabled: true}},
		{"progress sync off for account", nil, store.UserSettings{Timezone: "UTC", KosyncEnabled: false, KopluginEnabled: true}},
		{"statistics off for account", nil, store.UserSettings{Timezone: "UTC", KosyncEnabled: true, KopluginEnabled: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, st := testServerCfg(t, tc.mutate, nil)
			if err := st.UpdateUserSettings(t.Context(), "u1", tc.user); err != nil {
				t.Fatal(err)
			}
			cookie := loginCookie(t, ts)
			_, body := page(t, ts, cookie, "/ui/settings?section=devices")
			if code, _ := postForm(t, ts, cookie, "/ui/koreader/setup", url.Values{"csrf": {extractCSRF(t, body)}, "name": {"Kobo"}}); code != http.StatusConflict {
				t.Fatalf("status: %d", code)
			}
			if tokens, _ := st.ListTokens(t.Context(), "u1"); len(tokens) != 0 {
				t.Fatal("refused setup minted a token")
			}
			if devices, _ := st.ListKosyncDevices(t.Context(), "u1"); len(devices) != 0 {
				t.Fatal("refused setup created a Progress sync device")
			}
		})
	}
}

type failedSetupStore struct{ store.Store }

func (s failedSetupStore) CreateKopluginDevice(context.Context, store.KopluginDevice) error {
	return errors.New("statistics store unavailable")
}

func TestKOReaderSetupFailureRevokesEverything(t *testing.T) {
	ts, st := testServerCfg(t, nil, func(s *Server) { s.St = failedSetupStore{s.St} })
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices")
	if code, _ := postForm(t, ts, cookie, "/ui/koreader/setup", url.Values{"csrf": {extractCSRF(t, body)}, "name": {"Kobo"}}); code != http.StatusInternalServerError {
		t.Fatalf("failed setup: %d", code)
	}
	if tokens, _ := st.ListTokens(t.Context(), "u1"); len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Fatal("failed setup left an active token")
	}
	if devices, _ := st.ListKosyncDevices(t.Context(), "u1"); len(devices) != 1 || devices[0].RevokedAt == nil {
		t.Fatal("failed setup left an active Progress sync device")
	}
}
