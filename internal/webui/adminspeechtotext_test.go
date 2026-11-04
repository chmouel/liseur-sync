package webui

import (
	"bytes"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chmouel/liseur-sync/internal/config"
)

const speechToTextAdminFile = `insecure_http = true

[speech_to_text]
enabled = true
url = "https://stt.example/v1"
api_key = "stt-secret"
model = "whisper-small"

[content]
cache_dir = "covers"
`

// sttPeer is an OpenAI-compatible transcription endpoint that accepts
// key and answers every accepted request with an empty transcript.
func sttPeer(t *testing.T, key string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var model atomic.Value
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "model" {
				b, _ := io.ReadAll(part)
				model.Store(string(b))
			}
		}
		calls.Add(1)
		_, _ = w.Write([]byte(`{"text":""}`))
	}))
	t.Cleanup(peer.Close)
	return peer, &calls, &model
}

func speechToTextAdminWithFile(t *testing.T, body string) (*httptest.Server, *http.Cookie, string, string) {
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
	_, page := page(t, ts, cookie, "/ui/settings?section=admin&view=speech-to-text")
	return ts, cookie, path, extractCSRF(t, page)
}

func speechToTextForm(csrf string, fields map[string]string) map[string][]string {
	form := map[string][]string{"csrf": {csrf}, "admin_password": {"hunter2hunter"}}
	for k, v := range fields {
		form[k] = []string{v}
	}
	return form
}

func fileUnchanged(t *testing.T, path, before string) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("the file changed:\n%s", after)
	}
}

func TestSpeechToTextPageShowsTheSavedSection(t *testing.T) {
	ts, cookie, _, _ := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=speech-to-text")
	for _, want := range []string{
		"https://stt.example/v1", "whisper-small", "the configuration file",
		"Save and test", "Remove the saved key",
		"settings?section=admin&amp;view=speech-to-text\" aria-current=\"page\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the page is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "stt-secret") {
		t.Fatalf("the page rendered the key:\n%s", body)
	}
}

func TestSavingEnabledSpeechToTextTestsAndWrites(t *testing.T) {
	peer, calls, model := sttPeer(t, "new-key")
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	code, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"enabled": "on", "url": peer.URL + "/v1", "model": "whisper-base", "api_key": "new-key",
	}))
	if code != http.StatusOK || !strings.Contains(body, "saved and tested") {
		t.Fatalf("the save answered %d:\n%s", code, body)
	}
	if calls.Load() != 1 || model.Load() != "whisper-base" {
		t.Fatalf("the test call: %d calls, model %v", calls.Load(), model.Load())
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := config.SpeechToTextConfig{Enabled: true, URL: peer.URL + "/v1", APIKey: "new-key", Model: "whisper-base"}
	if loaded.SpeechToText != want {
		t.Fatalf("got %+v", loaded.SpeechToText)
	}
	if loaded.Content.CacheDir != "covers" || !loaded.InsecureHTTP {
		t.Fatalf("the save touched other settings: %+v", loaded)
	}
}

func TestAFailedTranscriptionWritesNothingAndScrubsTheKey(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	peer, _, _ := sttPeer(t, "right-key")
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"enabled": "on", "url": peer.URL + "/v1", "api_key": "wrong-key",
	}))
	if !strings.Contains(body, "the service answered HTTP 401") {
		t.Fatalf("the refusal was not shown:\n%s", body)
	}
	fileUnchanged(t, path, speechToTextAdminFile)
	if strings.Contains(body, "wrong-key") || strings.Contains(logs.String(), "wrong-key") {
		t.Fatal("the key leaked into the page or the log")
	}
	if !strings.Contains(logs.String(), `"action":"speech_to_text.save"`) {
		t.Fatalf("the refusal was not audited: %s", logs.String())
	}
}

func TestSavingDisabledSpeechToTextNeedsNoService(t *testing.T) {
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"url": "http://127.0.0.1:1/v1", "model": "whisper-small",
	}))
	if !strings.Contains(body, "saved and disabled") {
		t.Fatalf("the disabled save was not reported:\n%s", body)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SpeechToText.Enabled || loaded.SpeechToText.Active() {
		t.Fatalf("an unchecked box left transcription on: %+v", loaded.SpeechToText)
	}
	if loaded.SpeechToText.APIKey != "stt-secret" {
		t.Fatalf("a blank key field dropped the saved key: %+v", loaded.SpeechToText)
	}
}

func TestRemovingTheSpeechToTextKey(t *testing.T) {
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"url": "https://stt.example/v1", "remove_key": "on",
	}))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SpeechToText.APIKey != "" {
		t.Fatalf("the key survived its removal: %+v", loaded.SpeechToText)
	}
}

func TestEnabledGroqWithoutAKeyIsRefused(t *testing.T) {
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"enabled": "on", "url": "", "remove_key": "on",
	}))
	if !strings.Contains(body, "needs an API key") {
		t.Fatalf("the refusal was not shown:\n%s", body)
	}
	fileUnchanged(t, path, speechToTextAdminFile)
}

func TestAnEnvironmentSpeechToTextKeyTestsButIsNotWritten(t *testing.T) {
	t.Setenv("LISEUR_STT_API_KEY", "env-key")
	peer, calls, _ := sttPeer(t, "env-key")
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, "")
	_, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"enabled": "on", "url": peer.URL + "/v1",
	}))
	if !strings.Contains(body, "saved and tested") || calls.Load() != 1 {
		t.Fatalf("the environment key was not used:\n%s", body)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "env-key") {
		t.Fatalf("the environment key reached the file:\n%s", text)
	}
}

func TestAnEnvironmentManagedSpeechToTextIsReadOnly(t *testing.T) {
	t.Setenv("LISEUR_STT_URL", "https://stt.example/v1")
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=speech-to-text")
	if !strings.Contains(body, "LISEUR_STT_URL") || strings.Contains(body, "Save and test") {
		t.Fatalf("the page offered a form the environment owns:\n%s", body)
	}
	postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"url": "https://other.example/v1",
	}))
	fileUnchanged(t, path, speechToTextAdminFile)
}

func TestOnlyAnAdministratorSavesSpeechToText(t *testing.T) {
	ts, _ := testServerCfg(t, nil, generousReauth)
	cookie := loginCookie(t, ts)
	code, _ := postForm(t, ts, cookie, "/ui/admin/speech-to-text", map[string][]string{
		"admin_password": {"hunter2hunter"},
	})
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("a reader's save got %d", code)
	}
}

func TestAForgedSpeechToTextSaveIsRefused(t *testing.T) {
	ts, cookie, path, _ := speechToTextAdminWithFile(t, speechToTextAdminFile)
	code, _ := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm("forged", map[string]string{
		"url": "https://other.example/v1",
	}))
	if code != http.StatusForbidden {
		t.Fatalf("a forged save answered %d, want 403", code)
	}
	fileUnchanged(t, path, speechToTextAdminFile)
}

func TestSavedSpeechToTextDifferingFromRuntimeShowsRestart(t *testing.T) {
	ts, cookie, _, _ := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=speech-to-text")
	if !strings.Contains(body, "saved settings differ") {
		t.Fatalf("a file differing from the running process was not reported:\n%s", body)
	}
}

// TestATransportErrorNeverEchoesAnEncodedKey: a redirect the transport
// refuses quotes its target, which the service may have built from the
// key. The page and the log say why in their own words instead.
func TestATransportErrorNeverEchoesAnEncodedKey(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://stt.invalid/?key=env%2Fkey", http.StatusFound)
	}))
	t.Cleanup(peer.Close)
	ts, cookie, path, csrf := speechToTextAdminWithFile(t, speechToTextAdminFile)
	_, body := postForm(t, ts, cookie, "/ui/admin/speech-to-text", speechToTextForm(csrf, map[string]string{
		"enabled": "on", "url": peer.URL + "/v1", "api_key": "env/key",
	}))
	if !strings.Contains(body, "could not be reached") {
		t.Fatalf("the refusal was not shown:\n%s", body)
	}
	fileUnchanged(t, path, speechToTextAdminFile)
	for _, leak := range []string{"env%2Fkey", "env/key", "stt.invalid"} {
		if strings.Contains(body, leak) || strings.Contains(logs.String(), leak) {
			t.Fatalf("%q leaked into the page or the log", leak)
		}
	}
}
