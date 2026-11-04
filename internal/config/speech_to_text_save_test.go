package config

import (
	"strings"
	"testing"
)

func TestUpdateSpeechToTextPreservesEverythingElse(t *testing.T) {
	const body = "# keep me\ninsecure_http = true\n\n[speech_to_text]\nurl = \"http://127.0.0.1:8000/v1\"\n\n" +
		"[audiobookshelf]\napi_key = \"abs-secret\"\n\n[content]\ncache_dir = \"covers\"\n"
	path := writeAudiobookshelfConfig(t, body)
	current, err := ReadSpeechToText(path)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Enabled {
		t.Fatal("a section without enabled did not read as enabled")
	}
	if err := UpdateSpeechToText(path, func(s *SpeechToTextConfig) error {
		s.Model = "whisper-small"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	for _, kept := range []string{"# keep me", `api_key = "abs-secret"`, `cache_dir = "covers"`, `model = "whisper-small"`} {
		if !strings.Contains(text, kept) {
			t.Fatalf("a save lost %q:\n%s", kept, text)
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := SpeechToTextConfig{Enabled: true, URL: "http://127.0.0.1:8000/v1", Model: "whisper-small"}
	if loaded.SpeechToText != want {
		t.Fatalf("got %+v", loaded.SpeechToText)
	}
}

func TestUpdateSpeechToTextRefusesAnInvalidURL(t *testing.T) {
	const body = "[content]\ncache_dir = \"covers\"\n"
	path := writeAudiobookshelfConfig(t, body)
	err := UpdateSpeechToText(path, func(s *SpeechToTextConfig) error {
		s.URL = "https://user:pass@stt.example/v1"
		return nil
	})
	if err == nil {
		t.Fatal("a URL with credentials was written")
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("the file changed:\n%s", got)
	}
}
