package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveMirrorPreservesOtherConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	original := "listen_addr = \"127.0.0.1:8585\"\n\n[database]\ndriver = \"sqlite\"\n\n[mirror]\nenabled = false\nremote_key = \"old\"\n\n[content]\ncache_dir = \"covers\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := Default().Mirror
	m.Enabled = true
	m.Name = "orbit"
	m.BaseURL = "https://orbit.example/sync"
	m.Account = "alice"
	m.RemoteUser = "alice"
	m.RemoteKey = "new-key"
	m.DeviceID = "liseur-sync"
	if err := SaveMirror(path, m); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "listen_addr = \"127.0.0.1:8585\"") ||
		!strings.Contains(text, "cache_dir = \"covers\"") {
		t.Fatalf("unrelated configuration was lost:\n%s", text)
	}
	if strings.Count(text, "[mirror]") != 1 || !strings.Contains(text, "remote_key = \"new-key\"") ||
		strings.Contains(text, "remote_key = \"old\"") {
		t.Fatalf("mirror section was not replaced:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.Account != "alice" || loaded.Mirror.BaseURL != m.BaseURL {
		t.Fatalf("saved mirror did not load: %+v", loaded.Mirror)
	}
}

// TestSaveMirrorHandlesCommentedHeaders covers a header with a trailing
// inline comment, on both sides of the section: the opening
// "[mirror] # ..." must still be recognized (or the section is
// duplicated and the file no longer loads), and a following
// "[content] # ..." must still end the removal (or its settings are
// deleted along with the old mirror section).
func TestSaveMirrorHandlesCommentedHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	original := "listen_addr = \"127.0.0.1:8585\"\n\n" +
		"[mirror] # peer settings\n" +
		"enabled = false\n" +
		"remote_key = \"old\"\n\n" +
		"[content] # library\n" +
		"cache_dir = \"covers\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := Default().Mirror
	m.Enabled = true
	m.Name = "orbit"
	m.BaseURL = "https://orbit.example/sync"
	m.Account = "alice"
	m.RemoteUser = "alice"
	m.RemoteKey = "new-key"
	m.DeviceID = "liseur-sync"
	if err := SaveMirror(path, m); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, "[mirror]") != 1 {
		t.Fatalf("commented [mirror] header was not recognized, section duplicated:\n%s", text)
	}
	if !strings.Contains(text, "cache_dir = \"covers\"") {
		t.Fatalf("commented [content] header was swallowed, settings lost:\n%s", text)
	}
	if strings.Contains(text, "remote_key = \"old\"") {
		t.Fatalf("old mirror section was not replaced:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("rewritten config does not load: %v\n%s", err, text)
	}
	if loaded.Mirror.Account != "alice" || loaded.Content.CacheDir != "covers" {
		t.Fatalf("saved config did not round-trip: %+v / %+v", loaded.Mirror, loaded.Content)
	}
}

// TestSaveMirrorWritesAFileAnOperatorCanRead covers the case where
// there is nothing to preserve: the server was started with a config
// path that does not exist yet. The result has to be a file that loads,
// starts at its first setting and spells its durations the way the
// documentation does.
func TestSaveMirrorWritesAFileAnOperatorCanRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	m := Default().Mirror
	m.Name = "orbit"
	m.BaseURL = "https://orbit.example/sync"
	m.Account = "alice"
	m.RemoteUser = "alice"
	m.RemoteKey = "key"
	m.DeviceID = "liseur-sync"
	if err := SaveMirror(path, m); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.HasPrefix(text, "[mirror]\n") {
		t.Fatalf("a fresh config file does not start at its first setting:\n%q", text)
	}
	if !strings.Contains(text, "poll_interval = \"5m\"") ||
		!strings.Contains(text, "resolve_retry_interval = \"24h\"") ||
		!strings.Contains(text, "timeout = \"20s\"") {
		t.Fatalf("durations were not written the way people write them:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.PollInterval != m.PollInterval || loaded.Mirror.Timeout != m.Timeout {
		t.Fatalf("durations did not survive the round trip: %+v", loaded.Mirror)
	}
}

// TestSaveMirrorKeepsABookOrbitPeer. The admin page writes the whole
// mirror section, and a setting it drops is a mirror that comes back
// after a restart unable to sign in or unable to confirm a book.
func TestSaveMirrorKeepsABookOrbitPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte("listen_addr = \"127.0.0.1:8585\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := MirrorConfig{
		Enabled:              true,
		Protocol:             ProtocolBookOrbit,
		Name:                 "orbit",
		BaseURL:              "https://orbit.example.com/api/v1",
		Account:              "you",
		RemoteUser:           "you",
		RemotePassword:       "hunter2",
		DeviceID:             "liseur-sync",
		PeerPathPrefix:       "/books",
		LocalPathPrefix:      "/srv/library",
		PollInterval:         Duration(5 * time.Minute),
		ResolveRetryInterval: Duration(10 * time.Minute),
		ActiveDays:           30,
		Timeout:              Duration(20 * time.Second),
	}
	if err := SaveMirror(path, m); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Mirror
	if got.Protocol != ProtocolBookOrbit || got.RemotePassword != "hunter2" {
		t.Fatalf("the credential did not survive a save: %+v", got)
	}
	if got.PeerPathPrefix != "/books" || got.LocalPathPrefix != "/srv/library" {
		t.Fatalf("the path mapping did not survive a save: %+v", got)
	}
	if got.ResolveRetryInterval.Duration() != 10*time.Minute {
		t.Fatalf("the resolve retry interval did not survive a save: %+v", got)
	}
	if cfg.ListenAddr != "127.0.0.1:8585" {
		t.Fatalf("saving the mirror disturbed the rest of the file: %q", cfg.ListenAddr)
	}
}
