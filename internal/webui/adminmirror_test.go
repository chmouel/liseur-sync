package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/config"
)

// mirrorTestServer stands in for the peer: it answers the one request
// the save makes, so the enabled path can be tested without a network.
func mirrorTestServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"username":"reader"}`))
	}))
	t.Cleanup(peer.Close)
	return peer
}

// mirrorAdmin signs in as an administrator on a server whose config
// file lives in a temporary directory, and hands back the path so a
// test can read what the form wrote.
func mirrorAdmin(t *testing.T) (*httptest.Server, *http.Cookie, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	return ts, cookie, path, extractCSRF(t, body)
}

// TestSavingTheMirrorLeavesTheBrowserOnTheSettingsPage is the same rule
// the folder mutations follow: /ui/admin/mirror is two segments below
// /ui, so a page rendered there resolves its stylesheet into a
// directory that does not exist and a refresh replays the post.
func TestSavingTheMirrorLeavesTheBrowserOnTheSettingsPage(t *testing.T) {
	ts, cookie, path, csrf := mirrorAdmin(t)
	peer := mirrorTestServer(t, http.StatusOK)

	form := url.Values{
		"csrf": {csrf}, "enabled": {"on"},
		"base_url": {peer.URL + "/sync/"}, "account": {"alice"},
		"remote_user": {"reader"}, "remote_password": {"hunter2"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/admin/mirror",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("saving the mirror answered %d, want 303", resp.StatusCode)
	}
	loc, err := req.URL.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Path != "/ui/settings" || loc.Query().Get("view") != settingsAdminMirror {
		t.Fatalf("the browser was sent to %s, want the mirror settings page", loc)
	}
	if !strings.Contains(loc.Query().Get("notice"), "connection tested") {
		t.Fatalf("no notice travelled with the redirect: %s", loc)
	}

	// The page the browser lands on renders from the server's own copy
	// of the mirror settings, so it has to show what was just written
	// rather than what this process started with. The peer URL also
	// comes back without its trailing slash, because what is saved is
	// the copy the config validator normalized.
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	if !strings.Contains(body, peer.URL+"/sync\"") {
		t.Fatalf("the saved peer URL is not on the page:\n%s", body)
	}
	if !strings.Contains(body, "orbit") || !strings.Contains(body, "liseur-sync") {
		t.Fatalf("the saved settings are not on the page:\n%s", body)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "enabled = true") ||
		!strings.Contains(string(saved), "base_url = \""+peer.URL+"/sync\"") {
		t.Fatalf("the config file does not hold what was submitted:\n%s", saved)
	}
	// The password becomes the kosync key on the way in and is never
	// written down as itself.
	if strings.Contains(string(saved), "hunter2") {
		t.Fatalf("the peer password was written to disk:\n%s", saved)
	}
}

// TestSavingADisabledMirrorDoesNotClaimATest covers the wording: with
// the mirror off there is no peer to talk to, so the page must not
// report a connection it never made.
func TestSavingADisabledMirrorDoesNotClaimATest(t *testing.T) {
	ts, cookie, path, csrf := mirrorAdmin(t)
	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "base_url": {"https://orbit.example/sync"},
		"account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving a disabled mirror answered %d", code)
	}
	if strings.Contains(body, "connection tested") {
		t.Fatalf("a save with no peer contact claimed to have tested one:\n%s", body)
	}
	if !strings.Contains(body, "Mirror saved and disabled") {
		t.Fatalf("the save was not reported:\n%s", body)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Mirror.Enabled || saved.Mirror.Account != "alice" {
		t.Fatalf("the disabled mirror was not saved: %+v", saved.Mirror)
	}
}

// TestAPeerThatRejectsTheCredentialIsNotSaved. Testing before writing
// is the point of the form: a credential that cannot work never reaches
// the config file.
func TestAPeerThatRejectsTheCredentialIsNotSaved(t *testing.T) {
	ts, cookie, path, csrf := mirrorAdmin(t)
	peer := mirrorTestServer(t, http.StatusUnauthorized)
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "base_url": {peer.URL},
		"account": {"alice"}, "remote_user": {"reader"},
		"remote_password": {"wrong"}, "name": {"orbit"},
		"device_id": {"liseur-sync"},
	})
	if !strings.Contains(body, "The peer rejected the connection") {
		t.Fatalf("a rejected credential was not reported:\n%s", body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a rejected credential was written to the config file: %v", err)
	}
}

func TestSavingWithNewCredentialRepairsMalformedMirrorTable(t *testing.T) {
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(`listen_addr = "127.0.0.1:8585"
insecure_http = true

[mirror]
enabled = true
protocol = "kosync"
remote_key = "unterminated
`), 0600); err != nil {
		t.Fatal(err)
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.InsecureHTTP = true
		s.Cfg.Mirror = config.Default().Mirror
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"remote_password": {"fixed-password"}, "name": {"orbit"},
		"device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the repaired mirror was not tested:\n%s", body)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Mirror.Account != "alice" || saved.Mirror.RemoteKey == "" {
		t.Fatalf("the mirror table was not repaired: %+v", saved.Mirror)
	}
}

// TestSavingWithAnEnvironmentCredentialDoesNotCopyItToTheFile. A
// deployment may keep the peer credential beside the database URL in
// the environment. The admin form can use it to test a save, but a
// blank password field must not serialize that secret into TOML.
func TestSavingWithAnEnvironmentCredentialDoesNotCopyItToTheFile(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "env-secret-key")
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.Mirror.Enabled = true
		s.Cfg.Mirror.Protocol = config.ProtocolKosync
		s.Cfg.Mirror.BaseURL = peer.URL
		s.Cfg.Mirror.Account = "alice"
		s.Cfg.Mirror.RemoteUser = "reader"
		s.Cfg.Mirror.RemoteKey = "env-secret-key"
		s.Cfg.Mirror.DeviceID = "liseur-sync"
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the environment credential was not used to test the peer:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "env-secret-key") {
		t.Fatalf("the environment credential was written to disk:\n%s", saved)
	}
	if !strings.Contains(string(saved), `remote_key = ""`) {
		t.Fatalf("the saved mirror should leave the file credential empty:\n%s", saved)
	}
}

func TestSavingWithAnEnvironmentCredentialDoesNotTestTheStaleFileCredential(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "env-secret-key")
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(`[mirror]
enabled = true
protocol = "kosync"
name = "orbit"
base_url = "`+peer.URL+`"
account = "alice"
remote_user = "reader"
remote_key = "stale-file-key"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"
`), 0600); err != nil {
		t.Fatal(err)
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.Mirror.Enabled = true
		s.Cfg.Mirror.Protocol = config.ProtocolKosync
		s.Cfg.Mirror.BaseURL = peer.URL
		s.Cfg.Mirror.Account = "alice"
		s.Cfg.Mirror.RemoteUser = "reader"
		s.Cfg.Mirror.RemoteKey = "env-secret-key"
		s.Cfg.Mirror.DeviceID = "liseur-sync"
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the effective environment credential was not used:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `remote_key = "stale-file-key"`) {
		t.Fatalf("the file credential should have stayed on disk:\n%s", saved)
	}
	if strings.Contains(string(saved), "env-secret-key") {
		t.Fatalf("the environment credential was written to disk:\n%s", saved)
	}
}

func TestSavingWithAFileCredentialTestsTheCurrentFileCredential(t *testing.T) {
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(`[mirror]
enabled = true
protocol = "kosync"
name = "orbit"
base_url = "`+peer.URL+`"
account = "alice"
remote_user = "reader"
remote_key = "fresh-file-key"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"
`), 0600); err != nil {
		t.Fatal(err)
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		// Stands in for a long-running process whose startup config is
		// older than the current file. No LISEUR_MIRROR_REMOTE_KEY is
		// set, so the file credential is the effective one for this
		// save.
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.Mirror.Enabled = true
		s.Cfg.Mirror.Protocol = config.ProtocolKosync
		s.Cfg.Mirror.BaseURL = peer.URL
		s.Cfg.Mirror.Account = "alice"
		s.Cfg.Mirror.RemoteUser = "reader"
		s.Cfg.Mirror.RemoteKey = "stale-startup-key"
		s.Cfg.Mirror.DeviceID = "liseur-sync"
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the current file credential was not used:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `remote_key = "fresh-file-key"`) {
		t.Fatalf("the current file credential did not stay on disk:\n%s", saved)
	}
	if strings.Contains(string(saved), "stale-startup-key") {
		t.Fatalf("the stale startup credential was written to disk:\n%s", saved)
	}
}

func TestSavingWithAnEmptyEnvironmentCredentialIsRejected(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "")
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(`[mirror]
enabled = false
protocol = "kosync"
name = "orbit"
base_url = "`+peer.URL+`"
account = "alice"
remote_user = "reader"
remote_key = "file-key"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"
`), 0600); err != nil {
		t.Fatal(err)
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.Mirror.Enabled = false
		s.Cfg.Mirror.Protocol = config.ProtocolKosync
		s.Cfg.Mirror.BaseURL = peer.URL
		s.Cfg.Mirror.Account = "alice"
		s.Cfg.Mirror.RemoteUser = "reader"
		s.Cfg.Mirror.RemoteKey = ""
		s.Cfg.Mirror.DeviceID = "liseur-sync"
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "mirror.remote_key is required") {
		t.Fatalf("an empty environment credential was not rejected:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "enabled = true") {
		t.Fatalf("an invalid enabled mirror was written:\n%s", saved)
	}
}

func TestSavingAProtocolSwitchUsesTheTargetEnvironmentCredential(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "env-kosync-key")
	peer := mirrorTestServer(t, http.StatusOK)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.Mirror.Enabled = true
		s.Cfg.Mirror.Protocol = config.ProtocolBookOrbit
		s.Cfg.Mirror.BaseURL = "https://orbit.example/api/v1"
		s.Cfg.Mirror.Account = "alice"
		s.Cfg.Mirror.RemoteUser = "reader"
		s.Cfg.Mirror.RemotePassword = "bookorbit-password"
		s.Cfg.Mirror.DeviceID = "liseur-sync"
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {csrf}, "enabled": {"on"}, "protocol": {config.ProtocolKosync},
		"base_url": {peer.URL}, "account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusOK {
		t.Fatalf("saving the mirror answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the target protocol environment credential was not used:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "env-kosync-key") || strings.Contains(string(saved), "bookorbit-password") {
		t.Fatalf("an environment credential was written to disk:\n%s", saved)
	}
}

// TestAForgedMirrorSaveIsRefused. Every UI mutation carries the
// per-session token; this one writes a file on the server's disk.
func TestAForgedMirrorSaveIsRefused(t *testing.T) {
	ts, cookie, path, _ := mirrorAdmin(t)
	code, _ := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"csrf": {"forged"}, "base_url": {"https://orbit.example/sync"},
		"account": {"alice"}, "remote_user": {"reader"},
		"name": {"orbit"}, "device_id": {"liseur-sync"},
	})
	if code != http.StatusForbidden {
		t.Fatalf("a forged mirror save answered %d, want 403", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a forged request wrote the config file: %v", err)
	}
}

// TestOnlyAnAdministratorSavesTheMirror. The form names a local
// account and writes a credential into the server's own configuration,
// so it is not a page a reader reaches.
func TestOnlyAnAdministratorSavesTheMirror(t *testing.T) {
	ts, _ := testServerCfg(t, nil, generousReauth)
	cookie := loginCookie(t, ts)
	code, _ := postForm(t, ts, cookie, "/ui/admin/mirror", url.Values{
		"base_url": {"https://orbit.example/sync"},
	})
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("a reader posting to the mirror form got %d", code)
	}
}
