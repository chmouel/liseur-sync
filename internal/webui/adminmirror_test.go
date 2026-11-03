package webui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/mirror"
	"github.com/chmouel/liseur-sync/internal/store"
)

// mirrorTestServer stands in for the peer: it answers the one request a
// save makes, so the enabled path can be tested without a network.
func mirrorTestServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"username":"reader"}`))
	}))
	t.Cleanup(peer.Close)
	return peer
}

// mirrorAdminWithFile is the same with a config file already in place.
func mirrorAdminWithFile(t *testing.T, body string) (*httptest.Server, *http.Cookie, string, string) {
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
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	return ts, cookie, path, extractCSRF(t, page)
}

// mirrorForm is a save from the page: the token, the administrator's
// password, and whatever the form in question adds.
func mirrorForm(csrf string) url.Values {
	return url.Values{"csrf": {csrf}, "admin_password": {"hunter2hunter"}}
}

// addForm opens the new-entry form and returns its token, because the
// page it renders is the one that carries the fields a test posts.
func addForm(t *testing.T, ts *httptest.Server, cookie *http.Cookie) string {
	t.Helper()
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&add=1")
	if code != http.StatusOK {
		t.Fatalf("the add form answered %d", code)
	}
	if !strings.Contains(body, "Add a mirror") {
		t.Fatalf("the add form is not on the page:\n%s", body)
	}
	return extractCSRF(t, body)
}

// singleEntryFile is a deployment that already mirrors through the one
// table, with a setting beside it that a save must not touch.
const singleEntryFile = `listen_addr = "127.0.0.1:8585"
insecure_http = true

[mirror]
enabled = true
name = "peer"
base_url = "https://orbit.example.com/sync"
account = "alice"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"

[content]
cache_dir = "covers"
`

func TestThePageListsTheSingleEntryAndTheList(t *testing.T) {
	body := singleEntryFile + `
[[mirrors]]
enabled = false
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
`
	ts, cookie, _, _ := mirrorAdminWithFile(t, body)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	for _, want := range []string{"peer", "kobo", "the single [mirror] entry",
		"KOReader sync", "https://kobo.example.com/sync", "the configuration file"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page does not show %q:\n%s", want, page)
		}
	}
	// The disabled entry is there to be edited, which is the whole
	// point of reading the file rather than the running configuration.
	if !strings.Contains(page, "view=mirror&amp;mirror=kobo") {
		t.Fatalf("the disabled entry has no edit link:\n%s", page)
	}
}

func TestThePageNeverCarriesACredential(t *testing.T) {
	t.Setenv("LISEUR_MIRRORS_KOBO_REMOTE_KEY", "env-secret-key")
	body := singleEntryFile + `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
remote_key = "file-secret-key"
`
	ts, cookie, _, _ := mirrorAdminWithFile(t, body)
	for _, path := range []string{
		"/ui/settings?section=admin&view=mirror",
		"/ui/settings?section=admin&view=mirror&mirror=kobo",
		"/ui/settings?section=admin&view=mirror&mirror=peer",
		"/ui/settings?section=admin&view=mirror&add=1",
	} {
		_, page := page(t, ts, cookie, path)
		for _, secret := range []string{"file-secret-key", "env-secret-key"} {
			if strings.Contains(page, secret) {
				t.Fatalf("%s renders %q", path, secret)
			}
		}
	}
	// Nor in a redirect: the notice travels in the query string.
	peer := mirrorTestServer(t, http.StatusOK)
	form := mirrorForm(extractCSRF(t, func() string {
		_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&add=1")
		return page
	}()))
	form.Set("enabled", "on")
	form.Set("name", "third")
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader3")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ui/admin/mirror",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	for _, secret := range []string{"file-secret-key", "env-secret-key"} {
		if strings.Contains(loc, secret) {
			t.Fatalf("the redirect carries %q: %s", secret, loc)
		}
	}
}

func TestSavingAListEntryLeavesTheSingleEntryAlone(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)
	csrf := addForm(t, ts, cookie)

	form := mirrorForm(csrf)
	form.Set("enabled", "on")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("name", "kobo")
	form.Set("base_url", peer.URL+"/sync/")
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "hunter2")
	form.Set("device_id", "liseur-sync")
	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if code != http.StatusOK {
		t.Fatalf("saving a list entry answered %d", code)
	}
	if !strings.Contains(body, "connection tested") {
		t.Fatalf("the enabled entry was not tested:\n%s", body)
	}
	if !strings.Contains(body, "Restart liseur-sync") {
		t.Fatalf("the page does not say a restart is needed:\n%s", body)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if !strings.HasPrefix(text, singleEntryFile[:strings.Index(singleEntryFile, "[content]")]) {
		t.Fatalf("the single entry or what came before it moved:\n%s", text)
	}
	if strings.Count(text, "[[mirrors]]") != 1 {
		t.Fatalf("the list entry was not written:\n%s", text)
	}
	if strings.Contains(text, "hunter2") {
		t.Fatalf("the peer password was written to disk:\n%s", text)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Mirrors) != 1 || loaded.Mirrors[0].Name != "kobo" {
		t.Fatalf("the list entry did not load: %+v", loaded.Mirrors)
	}
	// The URL comes back normalised, because what is saved is the copy
	// the config validator cleaned.
	if got := loaded.Mirrors[0].BaseURL; got != peer.URL+"/sync" {
		t.Fatalf("peer URL after a save: %q", got)
	}
}

func TestSavingTheSingleEntryFromThePage(t *testing.T) {
	ts, cookie, path, csrf := mirrorAdminWithFile(t, singleEntryFile)
	form := mirrorForm(csrf)
	form.Set("original", "peer")
	form.Set("legacy", "1")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", "https://orbit.example.com/sync")
	form.Set("account", "alice")
	form.Set("remote_user", "reader")
	form.Set("device_id", "liseur-sync")
	code, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if code != http.StatusOK {
		t.Fatalf("saving the single entry answered %d", code)
	}
	if !strings.Contains(body, "Mirror saved and disabled") {
		t.Fatalf("the save was not reported:\n%s", body)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.Enabled {
		t.Fatal("an unchecked enable box left the mirror on")
	}
	if loaded.Mirror.Name != "peer" {
		t.Fatalf("the name changed: %+v", loaded.Mirror)
	}
}

func TestEditingAnExistingMirrorCannotRenameIt(t *testing.T) {
	body := `[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
remote_key = "0123456789abcdef0123456789abcdef"
`
	ts, cookie, path, _ := mirrorAdminWithFile(t, body)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&mirror=kobo")
	form := mirrorForm(extractCSRF(t, page))
	form.Set("original", "kobo")
	form.Set("name", "orbit")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", "https://kobo.example.com/sync")
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("device_id", "liseur-sync")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(page, "Mirror saved and disabled") {
		t.Fatalf("save failed:\n%s", page)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Mirrors) != 1 || loaded.Mirrors[0].Name != "kobo" {
		t.Fatalf("the mirror was renamed: %+v", loaded.Mirrors)
	}
}

func TestAPeerThatRejectsTheCredentialIsNotSaved(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusUnauthorized)
	csrf := addForm(t, ts, cookie)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := mirrorForm(csrf)
	form.Set("enabled", "on")
	form.Set("name", "kobo")
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "wrong")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "peer rejected the connection") {
		t.Fatalf("a rejected credential was not reported:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected credential was written:\n%s", after)
	}
}

func TestAWrongAdminPasswordWritesNothing(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)
	csrf := addForm(t, ts, cookie)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := mirrorForm(csrf)
	form.Set("admin_password", "not-my-password")
	form.Set("enabled", "on")
	form.Set("name", "kobo")
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "hunter2")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "your password is wrong") {
		t.Fatalf("a wrong password was not reported:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a wrong password still wrote the file:\n%s", after)
	}
}

func TestAWrongAdminPasswordOnDeleteWritesNothing(t *testing.T) {
	body := singleEntryFile + `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
remote_key = "0123456789abcdef0123456789abcdef"
`
	ts, cookie, path, _ := mirrorAdminWithFile(t, body)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&mirror=kobo")
	csrf := extractCSRF(t, page)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := mirrorForm(csrf)
	form.Set("name", "kobo")
	form.Set("admin_password", "not-my-password")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror/delete", form)
	if !strings.Contains(page, "your password is wrong") {
		t.Fatalf("a wrong password was not reported:\n%s", page)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a wrong password still wrote the file:\n%s", after)
	}
}

func TestAForgedMirrorSaveIsRefused(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := mirrorForm("forged")
	form.Set("name", "kobo")
	code, _ := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if code != http.StatusForbidden {
		t.Fatalf("a forged mirror save answered %d, want 403", code)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a forged request wrote the config file:\n%s", after)
	}
}

func TestAForgedMirrorDeleteIsRefused(t *testing.T) {
	body := singleEntryFile + `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
remote_key = "0123456789abcdef0123456789abcdef"
`
	ts, cookie, path, _ := mirrorAdminWithFile(t, body)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	form := mirrorForm("forged")
	form.Set("name", "kobo")
	code, _ := postForm(t, ts, cookie, "/ui/admin/mirror/delete", form)
	if code != http.StatusForbidden {
		t.Fatalf("a forged mirror delete answered %d, want 403", code)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a forged delete wrote the config file:\n%s", after)
	}
}

func TestOnlyAnAdministratorSavesTheMirror(t *testing.T) {
	ts, _ := testServerCfg(t, nil, generousReauth)
	cookie := loginCookie(t, ts)
	for _, path := range []string{"/ui/admin/mirror", "/ui/admin/mirror/delete"} {
		code, _ := postForm(t, ts, cookie, path, url.Values{
			"name": {"kobo"}, "admin_password": {"hunter2hunter"},
		})
		if code != http.StatusForbidden && code != http.StatusNotFound {
			t.Fatalf("a reader posting to %s got %d", path, code)
		}
	}
}

func TestATakenMirrorNameIsRefused(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	csrf := addForm(t, ts, cookie)
	form := mirrorForm(csrf)
	form.Set("enabled", "on")
	form.Set("name", "peer")
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "hunter2")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "names must be unique") {
		t.Fatalf("a taken name was not refused:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused name still wrote the file:\n%s", after)
	}
}

// TestTwoMirrorsOnOnePeerAccountAreRefused is the leak this validation
// exists for: two local accounts reaching one remote account would merge
// their reading through it.
func TestTwoMirrorsOnOnePeerAccountAreRefused(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	csrf := addForm(t, ts, cookie)
	form := mirrorForm(csrf)
	form.Set("enabled", "on")
	form.Set("name", "kobo")
	form.Set("base_url", "https://orbit.example.com/sync")
	form.Set("account", "bob")
	form.Set("remote_user", "reader")
	form.Set("remote_password", "hunter2")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "same account on the same peer") {
		t.Fatalf("a duplicate peer target was not refused:\n%s", body)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused duplicate still wrote the file:\n%s", after)
	}
	_ = peer
}

// TestSavingWithAnEnvironmentCredentialDoesNotCopyItToTheFile. A
// deployment may keep a list entry's credential beside the database URL in
// the environment. The form can use it to test a save, but a blank
// password field must not serialize that secret into TOML.
func TestSavingWithAnEnvironmentCredentialDoesNotCopyItToTheFile(t *testing.T) {
	t.Setenv("LISEUR_MIRRORS_KOBO_REMOTE_KEY", "env-secret-key")
	body := `insecure_http = true

[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
`
	ts, cookie, path, _ := mirrorAdminWithFile(t, body)
	var tested bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-auth-key") != "env-secret-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		tested = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":"OK"}`))
	}))
	t.Cleanup(peer.Close)

	csrf := extractCSRF(t, func() string {
		_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
		return page
	}())
	form := mirrorForm(csrf)
	form.Set("original", "kobo")
	form.Set("enabled", "on")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("device_id", "liseur-sync")
	_, page := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !tested || !strings.Contains(page, "connection tested") {
		t.Fatalf("the environment credential was not used to test the peer:\n%s", page)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "env-secret-key") {
		t.Fatalf("the environment credential was written to disk:\n%s", saved)
	}
	if !strings.Contains(string(saved), `remote_key = ""`) {
		t.Fatalf("the saved entry should leave the file credential empty:\n%s", saved)
	}
}

// TestAnEnvironmentManagedSingleEntryIsReadOnly. Every non-secret
// LISEUR_MIRROR_* variable is an override the next start applies, so a
// form that saved those settings would be saving values nothing reads.
func TestAnEnvironmentManagedSingleEntryIsReadOnly(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_BASE_URL", "https://from-env.example/sync")
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	if !strings.Contains(page, "from the environment") {
		t.Fatalf("the page does not say the entry is environment-managed:\n%s", page)
	}
	form := mirrorForm(extractCSRF(t, page))
	form.Set("original", "peer")
	form.Set("legacy", "1")
	form.Set("base_url", "https://elsewhere.example/sync")
	form.Set("account", "alice")
	form.Set("remote_user", "reader")
	form.Set("device_id", "liseur-sync")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(page, "come from the environment") {
		t.Fatalf("an environment-managed entry was editable:\n%s", page)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("an environment-managed entry was written:\n%s", after)
	}
}

func TestRemovingAListEntry(t *testing.T) {
	body := singleEntryFile + `
[[mirrors]]
enabled = false
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"
`
	ts, cookie, path, _ := mirrorAdminWithFile(t, body)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&mirror=kobo")
	form := mirrorForm(extractCSRF(t, page))
	form.Set("name", "kobo")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror/delete", form)
	if !strings.Contains(page, "Mirror removed") {
		t.Fatalf("the removal was not reported:\n%s", page)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "kobo") {
		t.Fatalf("the entry was not removed:\n%s", text)
	}
	if !strings.Contains(string(text), "[mirror]") {
		t.Fatalf("removing a list entry removed the single one too:\n%s", text)
	}
}

func TestRemovingTheSingleEntry(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror&mirror=peer")
	form := mirrorForm(extractCSRF(t, page))
	form.Set("name", "peer")
	form.Set("legacy", "1")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror/delete", form)
	if !strings.Contains(page, "Mirror removed") {
		t.Fatalf("the removal was not reported:\n%s", page)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "[mirror]") {
		t.Fatalf("the table was not removed:\n%s", text)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MirrorTableDefined() || len(loaded.EnabledMirrors()) != 0 {
		t.Fatalf("a removed entry still starts a worker: %+v", loaded.Mirror)
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
		s.Cfg.SetMirrorTableDefined(true)
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	if !strings.Contains(page, "could not be read") {
		t.Fatalf("a broken file was not reported:\n%s", page)
	}
	form := mirrorForm(extractCSRF(t, page))
	form.Set("original", "peer")
	form.Set("legacy", "1")
	form.Set("enabled", "on")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader")
	form.Set("remote_password", "fixed-password")
	form.Set("name", "orbit")
	form.Set("device_id", "liseur-sync")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(page, "connection tested") {
		t.Fatalf("the repaired mirror was not tested:\n%s", page)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Mirror.Account != "alice" || saved.Mirror.RemoteKey == "" {
		t.Fatalf("the mirror table was not repaired: %+v", saved.Mirror)
	}
}

// TestTheListCannotBeEditedThroughABrokenFile. The list is rewritten
// whole, so a file that does not parse gives nothing to prove the rest of
// it against; the single entry is the one repair the page offers.
func TestTheListCannotBeEditedThroughABrokenFile(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t,
		"[mirror]\nremote_key = \"unterminated\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	form := mirrorForm(extractCSRF(t, page))
	form.Set("name", "kobo")
	form.Set("base_url", "https://kobo.example.com/sync")
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "hunter2")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(page, "does not parse") {
		t.Fatalf("a list change into a broken file was not refused:\n%s", page)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("the broken file was changed anyway:\n%s", after)
	}
}

// TestRepairingTheSingleEntryCannotDuplicateAPreservedEntry. A broken file
// hands the page no list, so the repair is only proved against the preserved
// bytes once the rendered file is read whole. A list entry already reaching
// the peer account the repair names makes that file one the next start
// refuses, and a refused save leaves the bytes alone.
func TestRepairingTheSingleEntryCannotDuplicateAPreservedEntry(t *testing.T) {
	peer := mirrorTestServer(t, http.StatusOK)
	original := `listen_addr = "127.0.0.1:8585"
insecure_http = true

[[mirrors]]
enabled = true
name = "bob"
base_url = "` + peer.URL + `"
account = "bob"
remote_user = "reader"
remote_key = "fedcba9876543210fedcba9876543210"
device_id = "liseur-sync"

[mirror]
enabled = true
protocol = "kosync"
remote_key = "unterminated
`
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.ConfigPath = path
		s.Cfg.InsecureHTTP = true
		s.Cfg.Mirror = config.Default().Mirror
		s.Cfg.SetMirrorTableDefined(true)
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	form := mirrorForm(extractCSRF(t, page))
	form.Set("original", "peer")
	form.Set("legacy", "1")
	form.Set("enabled", "on")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", peer.URL)
	form.Set("account", "alice")
	form.Set("remote_user", "reader")
	form.Set("remote_password", "hunter2")
	form.Set("device_id", "liseur-sync")
	_, page = postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(page, "same account on the same peer") {
		t.Fatalf("a repair duplicating a list entry was not refused:\n%s", page)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("a refused repair still wrote the file:\n%s", after)
	}
}

// environmentOnlyEntry configures the single entry entirely from the
// environment, which is a deployment shape the page has to know about.
func environmentOnlyEntry(t *testing.T) {
	t.Helper()
	t.Setenv("LISEUR_MIRROR_ENABLED", "true")
	t.Setenv("LISEUR_MIRROR_BASE_URL", "https://orbit.example.com/sync")
	t.Setenv("LISEUR_MIRROR_ACCOUNT", "alice")
	t.Setenv("LISEUR_MIRROR_REMOTE_USER", "reader")
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("LISEUR_MIRROR_NAME", "peer")
}

// TestAnEnvironmentSuppliedEntryIsListedAndReadOnly. The page lists the set
// the next start would run, not just the file's half of it. An entry the
// environment supplies has no [mirror] table to edit, and its name is as taken
// as a file's: adding a list entry with it would save a configuration the next
// Load refuses.
func TestAnEnvironmentSuppliedEntryIsListedAndReadOnly(t *testing.T) {
	environmentOnlyEntry(t)
	ts, cookie, path, _ := mirrorAdminWithFile(t,
		"listen_addr = \"127.0.0.1:8585\"\ninsecure_http = true\n")

	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	if !strings.Contains(page, "peer") ||
		!strings.Contains(page, "environment: LISEUR_MIRROR_REMOTE_KEY") {
		t.Fatalf("the environment's entry is not on the page:\n%s", page)
	}
	if strings.Contains(page, "mirror=peer") {
		t.Fatalf("an entry the file cannot change offered an edit form:\n%s", page)
	}

	csrf := addForm(t, ts, cookie)
	form := mirrorForm(csrf)
	form.Set("name", "peer")
	form.Set("base_url", "https://kobo.example.com/sync")
	form.Set("account", "bob")
	form.Set("remote_user", "reader2")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "names must be unique") {
		t.Fatalf("a list entry took the environment's name:\n%s", body)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("the page saved a configuration the next start refuses: %v", err)
	}
	if !loaded.MirrorTableDefined() || loaded.Mirror.Name != "peer" {
		t.Fatalf("the environment's entry did not survive the round trip: %+v", loaded.Mirror)
	}
}

// TestANameTheEnvironmentOverridesIsStillTaken. A non-secret variable replaces
// a file value at the next start, so validation has to run against the
// overridden set: the name on disk is not the name the server will use.
func TestANameTheEnvironmentOverridesIsStillTaken(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_NAME", "envpeer")
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)

	csrf := addForm(t, ts, cookie)
	form := mirrorForm(csrf)
	form.Set("name", "envpeer")
	form.Set("base_url", "https://kobo.example.com/sync")
	form.Set("account", "bob")
	form.Set("remote_user", "reader2")
	_, body := postForm(t, ts, cookie, "/ui/admin/mirror", form)
	if !strings.Contains(body, "names must be unique") {
		t.Fatalf("a list entry took the name the environment gives the entry:\n%s", body)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("the page saved a configuration the next start refuses: %v", err)
	}
}

// TestRefusedMirrorMutationsLeaveOneAuditLine. A forged token and a
// non-administrator both write nothing and are both refused before the
// handler's own audit line, so the refusal gates owe the trail one line each —
// naming the action and the outcome, never the token or the credential.
func TestRefusedMirrorMutationsLeaveOneAuditLine(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	ts, cookie, _, _ := mirrorAdminWithFile(t, singleEntryFile)
	form := mirrorForm("forged-token-xyz")
	form.Set("name", "peer")
	if code, _ := postForm(t, ts, cookie, "/ui/admin/mirror/delete", form); code != http.StatusForbidden {
		t.Fatalf("a forged mirror delete answered %d, want 403", code)
	}
	got := logs.String()
	if n := strings.Count(got, `"action":"mirror.delete"`); n != 1 {
		t.Fatalf("%d delete audit lines for one refusal: %s", n, got)
	}
	if !strings.Contains(got, `"outcome":"the form's CSRF token was refused"`) {
		t.Fatalf("the refusal was not recorded as one: %s", got)
	}
	if strings.Contains(got, "forged-token-xyz") {
		t.Fatalf("the audit log carried the CSRF token: %s", got)
	}

	readerTS, st := testServerCfg(t, nil, nil)
	mkTarget(t, st, "bob")
	reader := loginCookieAs(t, readerTS, "bob", "bobs-password")
	logs.Reset()
	if code, _ := postForm(t, readerTS, reader, "/ui/admin/mirror", mirrorForm("anything")); code != http.StatusForbidden {
		t.Fatalf("a reader's mirror save answered %d, want 403", code)
	}
	got = logs.String()
	if n := strings.Count(got, `"action":"mirror.save"`); n != 1 {
		t.Fatalf("%d save audit lines for one refusal: %s", n, got)
	}
	if !strings.Contains(got, `"outcome":"administrator rights are required"`) {
		t.Fatalf("the refusal was not recorded as one: %s", got)
	}
	if strings.Contains(got, "anything") {
		t.Fatalf("the audit log carried the form body: %s", got)
	}
}

// TestSavingAListEntryDoesNotCopyTheEnvironmentIntoTheSingleEntry. The page
// shows and validates the overlaid set, but the file is only ever given the
// file's own values: an account the operator keeps in LISEUR_MIRROR_ACCOUNT
// must not end up written beside the single entry by a list save.
func TestSavingAListEntryDoesNotCopyTheEnvironmentIntoTheSingleEntry(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_ACCOUNT", "env-account")
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)

	csrf := addForm(t, ts, cookie)
	form := mirrorForm(csrf)
	form.Set("enabled", "on")
	form.Set("name", "kobo")
	form.Set("base_url", peer.URL)
	form.Set("account", "bob")
	form.Set("remote_user", "reader2")
	form.Set("remote_password", "hunter2")
	if _, body := postForm(t, ts, cookie, "/ui/admin/mirror", form); !strings.Contains(body, "connection tested") {
		t.Fatalf("the list entry was not saved and tested:\n%s", body)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "env-account") {
		t.Fatalf("a list save wrote the environment into the single entry:\n%s", after)
	}
	if !strings.Contains(string(after), `account = "alice"`) {
		t.Fatalf("the single entry lost the account the file held:\n%s", after)
	}
	if !strings.Contains(string(after), `name = "kobo"`) {
		t.Fatalf("the list entry was not written:\n%s", after)
	}
}

// TestSavingAnUnrelatedMirrorLeavesASlashedAddressAsWritten. A build
// before this one removed one trailing slash when it validated a peer
// address and one more when it folded that address into a cursor
// identity, so the digest a cursor carries comes from the address as it
// was written. The page writes the whole list, and an entry nobody
// edited has to stay byte-for-byte: cleaning its address on the way past
// changes the alias the next start folds from it, and the first pass
// then takes the cursor for another library's and resets it.
func TestSavingAnUnrelatedMirrorLeavesASlashedAddressAsWritten(t *testing.T) {
	ctx := t.Context()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The peer answers a pass and holds nothing: what is under test
		// is the cursor, not an exchange.
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/syncs/progress/") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"authorized":"OK"}`))
	}))
	t.Cleanup(peer.Close)

	typed := peer.URL + "/sync///"
	ts, cookie, path, _ := mirrorAdminWithFile(t, `insecure_http = true

[mirror]
enabled = true
protocol = "kosync"
name = "peer"
base_url = "`+typed+`"
account = "alice"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"
`)

	// The cursor an earlier build left, under the digest that build
	// folded: the address with both of its slashes gone. The mirror's
	// own store, since the page and the pass meet at the file.
	const document = "43200deaa6b3a3cd67dc934fabe6f0f2"
	st := NewTestStore(t)
	if err := st.CreateUser(ctx, store.User{
		ID: "u1", Name: "reader", Timezone: "UTC",
		KosyncEnabled: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	work := store.Work{ID: "w1", UserID: "u1", Title: "A Memory Called Empire", CreatedAt: time.Now()}
	sha := "sha-w1"
	if err := st.CreateWork(ctx, work, &store.Edition{UserID: "u1", SHA256: sha, WorkID: work.ID},
		[]store.Identifier{
			{Kind: "sha256", Value: sha},
			{Kind: "partial-md5", Value: document},
		}); err != nil {
		t.Fatal(err)
	}
	appended, err := st.AppendOps(ctx, "u1", "phone", []store.Op{{
		OpID: "op-1", WorkID: work.ID, EditionSHA: &sha,
		ClientTS: time.Now().Add(-time.Hour), Progression: 0.42,
		Origin: store.OriginNative,
	}})
	if err != nil || appended[0].Status != "applied" {
		t.Fatalf("the local position: %v %+v", err, appended)
	}
	sum := sha256.Sum256([]byte("kosync\n" + strings.TrimSuffix(typed, "//") + "\nreader"))
	seen := time.Now().Add(-30 * time.Minute)
	if err := st.PutMirrorCursor(ctx, "u1", "peer", store.MirrorCursor{
		WorkID: work.ID, Document: document, PeerIdentity: hex.EncodeToString(sum[:8]),
		PushedSeq: appended[0].Seq, RemoteTS: seen.Unix(), PushedMark: "pct:0.42",
	}); err != nil {
		t.Fatal(err)
	}

	// A second, disabled mirror: a save an administrator makes on a page
	// that has nothing to do with the single entry's address.
	form := mirrorForm(addForm(t, ts, cookie))
	form.Set("name", "kobo")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", "https://kobo.example.com/sync")
	form.Set("account", "alice")
	form.Set("remote_user", "reader2")
	if _, body := postForm(t, ts, cookie, "/ui/admin/mirror", form); !strings.Contains(body, "Mirror saved") {
		t.Fatalf("the list entry was not saved:\n%s", body)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `base_url = "`+typed+`"`) {
		t.Fatalf("a save rewrote an address nobody edited:\n%s", saved)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	syncer := &mirror.Syncer{
		Store: st, Peer: loaded.Mirror.Name, UserID: "u1",
		Proto:        mirror.KosyncProtocol(mirror.NewClient(loaded.Mirror), loaded.Mirror),
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}
	rep, err := syncer.Pass(ctx)
	if err != nil || rep.FirstError != nil {
		t.Fatalf("pass: %s err=%v", rep, err)
	}
	if rep.Pushed != 0 || rep.Pulled != 0 {
		t.Fatalf("a save made an upgraded deployment repeat an exchange: %s", rep)
	}
	cursors, err := st.MirrorCursors(ctx, "u1", "peer")
	if err != nil {
		t.Fatal(err)
	}
	cur, ok := cursors[work.ID]
	if !ok {
		t.Fatal("the pass left no cursor")
	}
	if cur.PushedSeq != appended[0].Seq || cur.RemoteTS != seen.Unix() || cur.PushedMark != "pct:0.42" {
		t.Fatalf("the cursor lost what it remembered: %+v", cur)
	}
	current := sha256.Sum256([]byte("kosync\n" + config.MirrorRoot(typed) + "\nreader"))
	if want := hex.EncodeToString(current[:8]); cur.PeerIdentity != want {
		t.Fatalf("the cursor still folds the address the old way: %q", cur.PeerIdentity)
	}
}

// TestSavingAnUnrelatedMirrorLeavesAnOddSpellingAsWritten. Validation
// trims an address and lower-cases its scheme, so the spelling a file may
// hold and the spelling validation stores are two different strings naming
// one root. A cursor an earlier build left is folded from the text the file
// held, so an entry nobody edited keeps that text, whitespace and case
// included — which means the comparison has to be of roots, not of
// spellings.
func TestSavingAnUnrelatedMirrorLeavesAnOddSpellingAsWritten(t *testing.T) {
	spellings := map[string]func(raw string) string{
		"surrounding whitespace": func(raw string) string { return "  " + raw + "///  " },
		"upper-case scheme": func(raw string) string {
			return "HTTP://" + strings.TrimPrefix(raw, "http://") + "/sync///"
		},
	}
	for name, spell := range spellings {
		t.Run(name, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/syncs/progress/") {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(`{"authorized":"OK"}`))
			}))
			t.Cleanup(peer.Close)

			typed := spell(peer.URL)
			ts, cookie, path, _ := mirrorAdminWithFile(t, `insecure_http = true

[mirror]
enabled = true
protocol = "kosync"
name = "peer"
base_url = "`+typed+`"
account = "alice"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"
device_id = "liseur-sync"
poll_interval = "5m"
active_days = 30
timeout = "20s"

[[mirrors]]
enabled = false
name = "kobo"
base_url = "`+typed+`"
account = "alice"
remote_user = "reader2"
device_id = "liseur-sync"
`)

			form := mirrorForm(addForm(t, ts, cookie))
			form.Set("name", "third")
			form.Set("protocol", config.ProtocolKosync)
			form.Set("base_url", "https://third.example.com/sync")
			form.Set("account", "alice")
			form.Set("remote_user", "reader3")
			form.Set("device_id", "liseur-sync")
			if _, body := postForm(t, ts, cookie, "/ui/admin/mirror", form); !strings.Contains(body, "Mirror saved") {
				t.Fatalf("the unrelated entry was not saved:\n%s", body)
			}

			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(saved), `base_url = "`+typed+`"`); got != 2 {
				t.Fatalf("a save rewrote %d of the 2 addresses nobody edited:\n%s", 2-got, saved)
			}

			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("the saved file does not load: %v", err)
			}
			// The identity the old build folded from this text: trimmed,
			// re-written by the URL parser, then a slash off for
			// validation and another for the fold.
			want := func(raw string) string {
				u, err := url.Parse(strings.TrimSpace(raw))
				if err != nil {
					t.Fatalf("the test's address does not parse: %v", err)
				}
				return strings.TrimSuffix(u.String(), "/")
			}
			if got, want := loaded.Mirror.LegacyPeerBaseURL(), want(typed); got != want {
				t.Fatalf("the next start folds %q, the old build folded %q", got, want)
			}
			if got, want := loaded.Mirrors[0].LegacyPeerBaseURL(), want(typed); got != want {
				t.Fatalf("the list entry's next start folds %q, the old build folded %q", got, want)
			}
		})
	}
}

// TestAnAddressAnAdministratorEditedIsTheOneWritten. Keeping an entry's
// own spelling is only ever about the entry nobody touched: a save that
// changes the root writes the address the form submitted, and validation
// still has the last word on it.
func TestAnAddressAnAdministratorEditedIsTheOneWritten(t *testing.T) {
	ts, cookie, path, _ := mirrorAdminWithFile(t, singleEntryFile)
	peer := mirrorTestServer(t, http.StatusOK)
	form := mirrorForm(addForm(t, ts, cookie))
	form.Set("enabled", "on")
	form.Set("original", "peer")
	form.Set("legacy", "1")
	form.Set("protocol", config.ProtocolKosync)
	form.Set("base_url", peer.URL+"/other")
	form.Set("account", "alice")
	form.Set("remote_user", "reader")
	form.Set("device_id", "liseur-sync")
	if _, body := postForm(t, ts, cookie, "/ui/admin/mirror", form); !strings.Contains(body, "Mirror saved") {
		t.Fatalf("the edited entry was not saved:\n%s", body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `base_url = "`+peer.URL+`/other"`) {
		t.Fatalf("the address the administrator typed was not written:\n%s", saved)
	}

	// And an address validation refuses is not written for the sake of
	// whatever spelling the file held before it.
	bad := mirrorForm(addForm(t, ts, cookie))
	bad.Set("enabled", "on")
	bad.Set("original", "peer")
	bad.Set("legacy", "1")
	bad.Set("protocol", config.ProtocolKosync)
	bad.Set("base_url", peer.URL+"/other?token=abc")
	bad.Set("account", "alice")
	bad.Set("remote_user", "reader")
	bad.Set("device_id", "liseur-sync")
	if _, body := postForm(t, ts, cookie, "/ui/admin/mirror", bad); !strings.Contains(body, "must not contain a query string") {
		t.Fatalf("an address validation refuses was accepted:\n%s", body)
	}
	if after, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(after) != string(saved) {
		t.Fatalf("the refused address changed the file:\n%s", after)
	}
}

// TestThePageNeverEchoesAnAddressItCannotShow. The page reads the file, not
// the configuration this process validated, so it can hold an address
// validation would refuse: one carrying a credential, or one that is not an
// address at all. Neither the list nor the form may render such a value as
// written.
func TestThePageNeverEchoesAnAddressItCannotShow(t *testing.T) {
	ts, cookie, _, _ := mirrorAdminWithFile(t, `insecure_http = true

[mirror]
enabled = false
protocol = "kosync"
name = "peer"
base_url = "https://user:pw@peer.example/sync?token=abc#frag"
account = "alice"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"
device_id = "liseur-sync"

[[mirrors]]
enabled = false
name = "nohost"
base_url = "kobo.example/sync"

[[mirrors]]
enabled = false
name = "unreadable"
base_url = "http://[::1"
`)
	for _, path := range []string{
		"/ui/settings?section=admin&view=mirror",
		"/ui/settings?section=admin&view=mirror&mirror=peer",
		"/ui/settings?section=admin&view=mirror&mirror=nohost",
		"/ui/settings?section=admin&view=mirror&mirror=unreadable",
	} {
		_, page := page(t, ts, cookie, path)
		for _, never := range []string{"user:pw", "token=abc", "#frag",
			"kobo.example/sync", "http://[::1"} {
			if strings.Contains(page, never) {
				t.Fatalf("%s renders %q:\n%s", path, never, page)
			}
		}
	}
	// The address validation can name is still shown, and the ones it
	// cannot are shown as empty rather than as nothing at all.
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=mirror")
	if !strings.Contains(page, "https://peer.example/sync") {
		t.Fatalf("the address the page can show is missing:\n%s", page)
	}
	if got := strings.Count(page, "<td><code></code></td>"); got != 2 {
		t.Fatalf("%d of the 2 unreadable addresses render as empty:\n%s", got, page)
	}
}
