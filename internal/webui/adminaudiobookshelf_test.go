package webui

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiobookshelf"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
)

// audiobookshelfPeer answers the one request a save makes. It speaks the
// client's own Authorization header, so a test can tell which key was
// used.
func audiobookshelfPeer(t *testing.T, key string) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"abs-user","username":"alice"}`))
	}))
	t.Cleanup(peer.Close)
	return peer
}

const audiobookshelfAdminFile = `insecure_http = true

[audiobookshelf]
enabled = false
base_url = "https://abs.example.com"
account = "alice"
api_key = "file-secret"
poll_interval = "30s"
timeout = "20s"
session_overlap = "10m"
active_days = 30
stt_url = "https://stt.example/v1"
stt_api_key = "stt-secret"
stt_model = "whisper-small"

[content]
cache_dir = "covers"
`

func audiobookshelfAdminWithFile(
	t *testing.T, body string, tune func(*Server),
) (*httptest.Server, *http.Cookie, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		if tune != nil {
			tune(s)
		}
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	return ts, cookie, path, extractCSRF(t, page)
}

func audiobookshelfForm(csrf string) map[string][]string {
	return map[string][]string{
		"csrf": {csrf}, "admin_password": {"hunter2hunter"},
	}
}

func TestAudiobookshelfAdminPageShowsTheSavedSection(t *testing.T) {
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	for _, want := range []string{
		"Follow audiobooks on Audiobookshelf", "https://abs.example.com",
		"the configuration file", "Save and test connection",
		"settings?section=admin&amp;view=audiobookshelf\" aria-current=\"page\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the page is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "file-secret") || strings.Contains(body, "stt-secret") {
		t.Fatalf("the page rendered a credential:\n%s", body)
	}
}

func TestSavingEnabledAudiobookshelfTestsAndWrites(t *testing.T) {
	peer := audiobookshelfPeer(t, "hunter3")
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	form["api_key"] = []string{"hunter3"}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusOK {
		t.Fatalf("the save answered %d", code)
	}
	if !strings.Contains(body, "connection tested") || !strings.Contains(body, "Restart liseur-sync") {
		t.Fatalf("the save was not reported with its restart:\n%s", body)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Audiobookshelf.Enabled ||
		loaded.Audiobookshelf.BaseURL != peer.URL ||
		loaded.Audiobookshelf.APIKey != "hunter3" {
		t.Fatalf("the saved section did not load: %+v", loaded.Audiobookshelf)
	}
	if loaded.Audiobookshelf.STTModel != "whisper-small" ||
		loaded.Audiobookshelf.PollInterval.Duration() != 30*time.Second {
		t.Fatalf("advanced settings moved: %+v", loaded.Audiobookshelf)
	}
}

func TestSavingAKeyMakesTheConfigFileOwnerOnly(t *testing.T) {
	peer := audiobookshelfPeer(t, "hunter3")
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	form["api_key"] = []string{"hunter3"}
	if code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form); code != http.StatusOK {
		t.Fatalf("the save answered %d:\n%s", code, body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("the config file holding an API key is %04o, want 0600", got)
	}
}

func TestTheSaveRedirectsToACanonicalRelativeURL(t *testing.T) {
	peer := audiobookshelfPeer(t, "hunter3")
	ts, cookie, _, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	form["api_key"] = []string{"hunter3"}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/admin/audiobookshelf",
		strings.NewReader(url.Values(form).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the save answered %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "://") || strings.HasPrefix(loc, "/") {
		// A settings mutation answers with a relative Location, so the
		// browser resolves it against the URL it posted to.
		t.Fatalf("the redirect is not relative: %q", loc)
	}
	resolved, err := req.URL.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != "/ui/settings" ||
		resolved.Query().Get("section") != "admin" ||
		resolved.Query().Get("view") != "audiobookshelf" {
		t.Fatalf("the redirect does not land on the page: %q", resolved)
	}
}

func TestSavingDisabledAudiobookshelfNeedsNoPeerOrKey(t *testing.T) {
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	form := audiobookshelfForm(csrf)
	// Nothing listens here: a disabled save must not need a reachable
	// peer. The key field is left blank, so the file's key is kept.
	form["base_url"] = []string{"http://127.0.0.1:1"}
	form["account"] = []string{"alice"}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusOK {
		t.Fatalf("the save answered %d", code)
	}
	if !strings.Contains(body, "saved and disabled") {
		t.Fatalf("the disabled save was not reported:\n%s", body)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Audiobookshelf.Enabled {
		t.Fatal("an unchecked enable box left the integration on")
	}
	if loaded.Audiobookshelf.APIKey != "file-secret" {
		t.Fatalf("a blank key field dropped the saved key: %+v", loaded.Audiobookshelf)
	}
}

func TestARejectedConnectionWritesNothingAndScrubsTheKey(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("rejected key " + r.Header.Get("Authorization")))
	}))
	t.Cleanup(peer.Close)
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	form["api_key"] = []string{"typed-secret-key"}
	_, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if !strings.Contains(body, "refused the connection") {
		t.Fatalf("a rejected connection was not reported:\n%s", body)
	}
	if strings.Contains(body, "typed-secret-key") {
		t.Fatalf("the refusal rendered the key:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected connection still wrote the file:\n%s", after)
	}
	if strings.Contains(logs.String(), "typed-secret-key") {
		t.Fatalf("the audit log carried the key: %s", logs.String())
	}
}

func TestAnUnknownLocalAccountWritesNothing(t *testing.T) {
	peer := audiobookshelfPeer(t, "hunter3")
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"nobody"}
	form["api_key"] = []string{"hunter3"}
	_, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if !strings.Contains(body, "there is no local account named") {
		t.Fatalf("an unknown account was not refused:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused account still wrote the file:\n%s", after)
	}
}

func TestAnEnvironmentKeyTestsButIsNotWritten(t *testing.T) {
	t.Setenv("LISEUR_ABS_API_KEY", "env-secret-key")
	peer := audiobookshelfPeer(t, "env-secret-key")
	body := audiobookshelfAdminFile
	body = strings.Replace(body, `api_key = "file-secret"`, `api_key = ""`, 1)
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, body, nil)
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	// The key field is blank: the environment key is the effective one.
	_, page := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if !strings.Contains(page, "connection tested") {
		t.Fatalf("the environment key was not used to test:\n%s", page)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "env-secret-key") {
		t.Fatalf("the environment key was written to disk:\n%s", saved)
	}
	if !strings.Contains(string(saved), `api_key = ""`) {
		t.Fatalf("the file key was not left empty:\n%s", saved)
	}
}

func TestAnEmptyEnvironmentKeyRefusesAnEnabledSave(t *testing.T) {
	t.Setenv("LISEUR_ABS_API_KEY", "")
	peer := audiobookshelfPeer(t, "file-secret")
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL}
	form["account"] = []string{"alice"}
	_, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if !strings.Contains(body, "audiobookshelf.api_key is required") {
		t.Fatalf("an empty environment key was not refused:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused save wrote the file:\n%s", after)
	}
}

func TestAnEnvironmentManagedSettingIsReadOnly(t *testing.T) {
	t.Setenv("LISEUR_ABS_BASE_URL", "https://from-env.example")
	ts, cookie, path, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if !strings.Contains(body, "managed by the environment") ||
		!strings.Contains(body, "LISEUR_ABS_BASE_URL") {
		t.Fatalf("the page does not say the environment owns it:\n%s", body)
	}
	if strings.Contains(body, "Save and test connection") {
		t.Fatalf("an environment-managed section offered a form:\n%s", body)
	}
	form := audiobookshelfForm(extractCSRF(t, body))
	form["base_url"] = []string{"https://elsewhere.example"}
	form["account"] = []string{"alice"}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusOK || !strings.Contains(body, "comes from the environment") {
		t.Fatalf("an environment-managed save was not refused (%d):\n%s", code, body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("an environment-managed save wrote the file:\n%s", after)
	}
}

func TestOnlyAnAdministratorSavesAudiobookshelf(t *testing.T) {
	ts, _ := testServerCfg(t, nil, generousReauth)
	cookie := loginCookie(t, ts)
	code, _ := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", map[string][]string{
		"admin_password": {"hunter2hunter"},
	})
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("a reader's save got %d", code)
	}
}

func TestAForgedAudiobookshelfSaveIsRefusedAndAudited(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	ts, cookie, path, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm("forged-token-xyz")
	form["base_url"] = []string{"https://elsewhere.example"}
	code, _ := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusForbidden {
		t.Fatalf("a forged save answered %d, want 403", code)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a forged save wrote the file:\n%s", after)
	}
	got := logs.String()
	if !strings.Contains(got, `"action":"audiobookshelf.save"`) ||
		!strings.Contains(got, `"outcome":"the form's CSRF token was refused"`) {
		t.Fatalf("the refusal was not audited: %s", got)
	}
	if strings.Contains(got, "forged-token-xyz") {
		t.Fatalf("the audit log carried the token: %s", got)
	}
}

func TestAudiobookshelfSaveIsRateLimited(t *testing.T) {
	ts, cookie, path, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile,
		func(s *Server) {
			s.AdminReauthUserLimiter = auth.NewRateLimiter(1, time.Minute)
			s.AdminReauthIPLimiter = auth.NewRateLimiter(100, time.Minute)
		})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := audiobookshelfForm(extractCSRF(t, func() string {
		_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
		return page
	}()))
	form["admin_password"] = []string{"wrong"}
	form["base_url"] = []string{"https://elsewhere.example"}
	form["account"] = []string{"alice"}
	if code, _ := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form); code != http.StatusOK {
		t.Fatalf("the first attempt answered %d", code)
	}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusTooManyRequests {
		t.Fatalf("the second attempt answered %d, want 429", code)
	}
	if !strings.Contains(body, "too many attempts") {
		t.Fatalf("the rate-limited page does not say so:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused reauth wrote the file:\n%s", after)
	}
}

func TestSavedAudiobookshelfDiffersFromRuntimeShowsRestart(t *testing.T) {
	// This process started with the integration off; the file says on.
	text := strings.Replace(audiobookshelfAdminFile, "enabled = false", "enabled = true", 1)
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, text,
		func(s *Server) { s.Cfg.Audiobookshelf.Enabled = false })
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if !strings.Contains(body, "saved settings differ") {
		t.Fatalf("a saved-but-unapplied change was not reported:\n%s", body)
	}
}

func TestTheOverviewLinksToAudiobookshelf(t *testing.T) {
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin")
	if !strings.Contains(body, `settings?section=admin&amp;view=audiobookshelf`) {
		t.Fatalf("the overview has no Audiobookshelf link:\n%s", body)
	}
	if !strings.Contains(body, "Set up") {
		t.Fatalf("a disabled integration is not offered as setup:\n%s", body)
	}
}

// TestAudiobookshelfWithoutAConfigFileSaysSo: a server started without a
// writable file can show what it runs but not change it, and says that
// rather than failing somewhere later.
func TestAudiobookshelfWithoutAConfigFileSaysSo(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if !strings.Contains(body, "not started with a writable configuration file") {
		t.Fatalf("a missing config path was not reported:\n%s", body)
	}
	if strings.Contains(body, "Save and test connection") {
		t.Fatalf("a server without a config file offered a form:\n%s", body)
	}
	form := audiobookshelfForm(extractCSRF(t, body))
	form["base_url"] = []string{"https://abs.example.com"}
	form["account"] = []string{"alice"}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusOK || !strings.Contains(body, "writable config file") {
		t.Fatalf("a save without a config path answered %d:\n%s", code, body)
	}
}

// TestAMalformedFileDoesNotLeakItsSecret: a TOML diagnostic quotes the
// source literal it choked on, so neither the page, the redirect nor the
// audit line may carry the decoder's own message.
func TestAMalformedFileDoesNotLeakItsSecret(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const secret = "sup3r/secret+key"
	broken := "[audiobookshelf]\napi_key = \"" + secret + "\\xZ\"\n"
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, broken, nil)

	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if !strings.Contains(body, "does not parse") {
		t.Fatalf("the broken file was not reported:\n%s", body)
	}

	form := audiobookshelfForm(csrf)
	form["base_url"] = []string{"https://abs.example.com"}
	form["account"] = []string{"alice"}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/admin/audiobookshelf",
		strings.NewReader(url.Values(form).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the save answered %d, want 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	for _, leaked := range []string{secret, url.QueryEscape(secret)} {
		if strings.Contains(body, leaked) || strings.Contains(location, leaked) ||
			strings.Contains(logs.String(), leaked) {
			t.Fatalf("%q reached the page, the redirect or the audit log:\npage=%s\nlocation=%s\nlog=%s",
				leaked, body, location, logs.String())
		}
	}
	resolved, err := req.URL.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	_, landed := page(t, ts, cookie, resolved.RequestURI())
	for _, leaked := range []string{secret, url.QueryEscape(secret)} {
		if strings.Contains(landed, leaked) {
			t.Fatalf("%q reached the page a refused save landed on:\n%s", leaked, landed)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != broken {
		t.Fatalf("a refused save changed the file: %v\n%s", err, got)
	}
}

// TestThePageStripsAFileURLsCredential: the file can hold a URL this
// process never started with, and a valid environment URL can keep the
// integration running while that file value sits beside it. Neither may
// reach the browser.
func TestThePageStripsAFileURLsCredential(t *testing.T) {
	t.Setenv("LISEUR_ABS_BASE_URL", "https://abs.example.com")
	const rawURL = "https://user:pass@abs.example.com/?token=sup3r#frag"
	body := strings.Replace(audiobookshelfAdminFile,
		`base_url = "https://abs.example.com"`, `base_url = "`+rawURL+`"`, 1)
	ts, cookie, path, _ := audiobookshelfAdminWithFile(t, body, nil)

	_, rendered := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	for _, leaked := range []string{"user:pass@", "token=sup3r", "#frag"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("%q was rendered:\n%s", leaked, rendered)
		}
	}
	if !strings.Contains(rendered, "https://abs.example.com") {
		t.Fatalf("the clean server URL was not shown:\n%s", rendered)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), rawURL) {
		t.Fatalf("the page changed the stored address:\n%s", stored)
	}
}

// TestTheEditFormStripsAFileURLsCredential: the form is the other place a
// saved value reaches HTML, and it must be cleaned the same way.
func TestTheEditFormStripsAFileURLsCredential(t *testing.T) {
	const rawURL = "https://user:pass@abs.example.com/?token=sup3r#frag"
	body := strings.Replace(audiobookshelfAdminFile,
		`base_url = "https://abs.example.com"`, `base_url = "`+rawURL+`"`, 1)
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, body, nil)
	_, rendered := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	for _, leaked := range []string{"user:pass@", "token=sup3r", "#frag"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("%q was rendered:\n%s", leaked, rendered)
		}
	}
	if !strings.Contains(rendered, `value="https://abs.example.com/"`) {
		t.Fatalf("the form did not carry the clean URL:\n%s", rendered)
	}
}

// TestAnEnvironmentOverrideThatChangesNothingNeedsNoRestart: a file
// enabled=false overridden by LISEUR_ABS_ENABLED=true is exactly what the
// process runs, so a restart would apply nothing.
func TestAnEnvironmentOverrideThatChangesNothingNeedsNoRestart(t *testing.T) {
	t.Setenv("LISEUR_ABS_ENABLED", "true")
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile,
		func(s *Server) { s.Cfg.Audiobookshelf = audiobookshelfLoaded(t, s.ConfigPath) })
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if strings.Contains(body, "saved settings differ") {
		t.Fatalf("an environment override that changes nothing asked for a restart:\n%s", body)
	}
}

// TestAChangedOperationalSettingNeedsARestart: polling, timeout and the
// speech-to-text settings are not on the form, but they decide how the
// worker runs, so a saved change to one is a restart.
func TestAChangedOperationalSettingNeedsARestart(t *testing.T) {
	for name, change := range map[string]func(*config.AudiobookshelfConfig){
		"poll_interval": func(a *config.AudiobookshelfConfig) {
			a.PollInterval = config.Duration(5 * time.Minute)
		},
		"timeout": func(a *config.AudiobookshelfConfig) {
			a.Timeout = config.Duration(time.Minute)
		},
		"session_overlap": func(a *config.AudiobookshelfConfig) {
			a.SessionOverlap = config.Duration(30 * time.Minute)
		},
		"active_days": func(a *config.AudiobookshelfConfig) { a.ActiveDays = 5 },
		"stt_url": func(a *config.AudiobookshelfConfig) {
			a.STTURL = "https://other.example/v1"
		},
		"stt_model": func(a *config.AudiobookshelfConfig) { a.STTModel = "whisper-base" },
	} {
		t.Run(name, func(t *testing.T) {
			ts, cookie, _, _ := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile,
				func(s *Server) {
					// The running entry is exactly what a start loaded,
					// as it is, so the comparison is against real runtime.
					runtime := audiobookshelfLoaded(t, s.ConfigPath)
					change(&runtime)
					s.Cfg.Audiobookshelf = runtime
				})
			_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
			if !strings.Contains(body, "saved settings differ") {
				t.Fatalf("a changed %s was not reported as a restart:\n%s", name, body)
			}
		})
	}
}

// TestAnUppercaseSchemeAndAnEnvironmentOverrideNeedNoRestart: startup
// parses the address, so it lowercases the scheme; the restart check has
// to make the same address out of the saved value, or the notice never
// goes away.
func TestAnUppercaseSchemeAndAnEnvironmentOverrideNeedNoRestart(t *testing.T) {
	t.Setenv("LISEUR_ABS_ENABLED", "true")
	body := strings.Replace(audiobookshelfAdminFile,
		`base_url = "https://abs.example.com"`, `base_url = "HTTPS://abs.example.com/"`, 1)
	ts, cookie, _, _ := audiobookshelfAdminWithFile(t, body,
		func(s *Server) { s.Cfg.Audiobookshelf = audiobookshelfLoaded(t, s.ConfigPath) })
	_, rendered := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	if strings.Contains(rendered, "saved settings differ") {
		t.Fatalf("a spelling startup normalises still asked for a restart:\n%s", rendered)
	}
}

// audiobookshelfLoaded is the running configuration as config.Load would
// hand it to a starting process, which is what the page compares against.
func audiobookshelfLoaded(t *testing.T, path string) config.AudiobookshelfConfig {
	t.Helper()
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Audiobookshelf
}

// TestASavedURLTestsTheSameEndpointTheWorkerUses: the file keeps the
// address as typed, a start normalises it once, and the connection test
// speaks to exactly that normalised address — including when the
// operator leaves more than one trailing slash on it.
func TestASavedURLTestsTheSameEndpointTheWorkerUses(t *testing.T) {
	peer := audiobookshelfPeer(t, "hunter3")
	ts, cookie, path, csrf := audiobookshelfAdminWithFile(t, audiobookshelfAdminFile, nil)
	form := audiobookshelfForm(csrf)
	form["enabled"] = []string{"on"}
	form["base_url"] = []string{peer.URL + "//"}
	form["account"] = []string{"alice"}
	form["api_key"] = []string{"hunter3"}
	code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", form)
	if code != http.StatusOK || !strings.Contains(body, "connection tested") {
		t.Fatalf("the save did not test the endpoint the worker uses (%d):\n%s", code, body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `base_url = "`+peer.URL+`//"`) {
		t.Fatalf("the file did not keep the address as typed:\n%s", saved)
	}
	loaded := audiobookshelfLoaded(t, path)
	if got := audiobookshelf.NewClient(loaded).BaseURL(); got != peer.URL {
		t.Fatalf("the reloaded worker targets %q, want %q", got, peer.URL)
	}
	// The form is prefilled from the file; saving it unchanged must not
	// rewrite the address any further.
	_, rendered := page(t, ts, cookie, "/ui/settings?section=admin&view=audiobookshelf")
	again := audiobookshelfForm(extractCSRF(t, rendered))
	again["enabled"] = []string{"on"}
	again["base_url"] = []string{extractBaseURL(t, rendered)}
	again["account"] = []string{"alice"}
	if code, body := postForm(t, ts, cookie, "/ui/admin/audiobookshelf", again); code != http.StatusOK ||
		!strings.Contains(body, "connection tested") {
		t.Fatalf("re-saving the prefilled form answered %d:\n%s", code, body)
	}
	resaved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(resaved) != string(saved) {
		t.Fatalf("a save that changed nothing rewrote the address:\n%s", resaved)
	}
}

// extractBaseURL reads the server field back out of the rendered form,
// which is what a browser would post on an unchanged save.
func extractBaseURL(t *testing.T, rendered string) string {
	t.Helper()
	const marker = `<input name="base_url" type="url" value="`
	i := strings.Index(rendered, marker)
	if i < 0 {
		t.Fatalf("the form has no server field:\n%s", rendered)
	}
	rest := rendered[i+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("the server field is unterminated:\n%s", rendered)
	}
	return rest[:end]
}

// TestTheAdminViewCarriesNoCredentialOrRawURL: the template view is the
// only thing the page can render, so it must hold display values only —
// no API key, no speech-to-text key and no unsanitised address.
func TestTheAdminViewCarriesNoCredentialOrRawURL(t *testing.T) {
	const rawURL = "https://user:pass@abs.example.com/?token=sup3r#frag"
	body := strings.Replace(audiobookshelfAdminFile,
		`base_url = "https://abs.example.com"`, `base_url = "`+rawURL+`"`, 1)
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var srv *Server
	testServerCfg(t, nil, func(s *Server) { srv, s.ConfigPath = s, path })
	view := srv.audiobookshelfView(nil)
	if view.SavedURL != "https://abs.example.com/" {
		t.Fatalf("the view carries %q as the server address", view.SavedURL)
	}
	for _, leaked := range viewStrings(reflect.ValueOf(view)) {
		for _, secret := range []string{"file-secret", "stt-secret", "user:pass", "token=sup3r", "#frag"} {
			if strings.Contains(leaked, secret) {
				t.Fatalf("the view carries %q", secret)
			}
		}
	}
}

// viewStrings walks every string a handler hands the template, including
// the nested status value.
func viewStrings(v reflect.Value) []string {
	var out []string
	switch v.Kind() {
	case reflect.String:
		out = append(out, v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			out = append(out, viewStrings(v.Field(i))...)
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			out = append(out, viewStrings(v.Elem())...)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			out = append(out, viewStrings(v.Index(i))...)
		}
	}
	return out
}
