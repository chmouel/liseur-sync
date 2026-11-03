package main

import (
	"bytes"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/config"
)

func TestAdminUsageNoSubcommandPlainStderr(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"admin", "-config", "unused.toml"}, &stderr)
	if code == 0 {
		t.Fatal("runMain exited 0, want non-zero")
	}
	got := stderr.String()
	if !strings.Contains(got, "usage: liseur-sync admin [-config <file>] <subcommand>\n\n") {
		t.Fatalf("stderr did not contain plain multi-line admin usage:\n%s", got)
	}
	if strings.Contains(got, `\n`) {
		t.Fatalf("stderr contains escaped newline sequence: %q", got)
	}
	if strings.Contains(got, "ERROR") || strings.Contains(got, "err=") {
		t.Fatalf("stderr contains slog output: %q", got)
	}
}

func TestAdminUsageHelpFlagExitsZero(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"admin", "-h"}, &stderr)
	if code != 0 {
		t.Fatalf("runMain exited %d, want 0", code)
	}
	if !strings.Contains(stderr.String(),
		"usage: liseur-sync admin [-config <file>] <subcommand>\n\n") {
		t.Fatalf("stderr did not contain admin usage:\n%s", stderr.String())
	}
}

func TestDefaultConfigPathFallsBackWithoutEnv(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "")
	if got := defaultConfigPath(); got != "liseur-sync.toml" {
		t.Fatalf("defaultConfigPath() = %q, want %q", got, "liseur-sync.toml")
	}
}

func TestDefaultConfigPathUsesEnvOverride(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "/etc/liseur-sync/prod.toml")
	if got := defaultConfigPath(); got != "/etc/liseur-sync/prod.toml" {
		t.Fatalf("defaultConfigPath() = %q, want %q", got, "/etc/liseur-sync/prod.toml")
	}
}

func TestConfigFlagOverridesLISEUR_CONFIGEnvVar(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "/etc/liseur-sync/prod.toml")
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to TOML config file")
	if err := fs.Parse([]string{"-config", "explicit.toml"}); err != nil {
		t.Fatal(err)
	}
	if *cfgPath != "explicit.toml" {
		t.Fatalf("cfgPath = %q, want %q (explicit -config must win over LISEUR_CONFIG)", *cfgPath, "explicit.toml")
	}
}

func TestRelativeCacheDirFollowsAbsoluteSQLiteDatabase(t *testing.T) {
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Database.URL = filepath.Join(dataDir, "liseur-sync.db")
	cfg.Content.CacheDir = "cache"
	if got := cacheDirFor(cfg); got != filepath.Join(dataDir, "cache") {
		t.Fatalf("cacheDirFor = %q, want %q", got, filepath.Join(dataDir, "cache"))
	}
}

func TestAbsoluteCacheDirIsLeftAlone(t *testing.T) {
	cfg := config.Default()
	cfg.Database.URL = "/var/lib/liseur-sync/liseur-sync.db"
	cfg.Content.CacheDir = "/srv/cache"
	if got := cacheDirFor(cfg); got != "/srv/cache" {
		t.Fatalf("cacheDirFor = %q, want /srv/cache", got)
	}
}

func TestAdminUsageContainsScan(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"admin", "-h"}, &stderr)
	if code != 0 {
		t.Fatalf("runMain exited %d, want 0", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "scan [name|folder-id]") {
		t.Fatalf("admin usage does not document scan:\n%s", out)
	}
}

func TestTopUsageContainsScan(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"-h"}, &stderr)
	if code != 0 {
		t.Fatalf("runMain exited %d, want 0", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "scan [-config <file>]") {
		t.Fatalf("top usage does not document scan:\n%s", out)
	}
}

func TestTopScanHelpFlagExitsZero(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"scan", "-h"}, &stderr)
	if code != 0 {
		t.Fatalf("runMain exited %d, want 0", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "Usage of scan:") && !strings.Contains(out, "-config") {
		t.Fatalf("scan -h output unexpected:\n%s", out)
	}
}

// TestOneWorkerPerEnabledMirror. Each entry is a peer of its own, so
// startup builds a worker per entry and none for the disabled ones. The
// single [mirror] table counts as an entry like any other: a deployment
// that upgrades keeps mirroring without editing its file.
func TestOneWorkerPerEnabledMirror(t *testing.T) {
	cfg := config.Default()
	cfg.Mirror.Enabled = true
	cfg.Mirror.Name = "peer"
	cfg.Mirror.BaseURL = "https://orbit.example.com/sync"
	cfg.Mirror.Account = "alice"
	cfg.Mirror.RemoteUser = "reader"
	cfg.Mirror.RemoteKey = "0123456789abcdef0123456789abcdef"
	cfg.SetMirrorTableDefined(true)
	cfg.Mirrors = []config.MirrorConfig{
		{Enabled: true, Name: "kobo", BaseURL: "https://kobo.example.com/sync",
			Account: "bob", RemoteUser: "reader2", RemoteKey: "0123456789abcdef0123456789abcdef"},
		{Enabled: false, Name: "switched-off", BaseURL: "https://off.example.com/sync"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	runners := mirrorRunners(nil, cfg)
	if len(runners) != 2 {
		t.Fatalf("%d workers for two enabled entries", len(runners))
	}
	if runners[0].Cfg.Name != "peer" || runners[1].Cfg.Name != "kobo" {
		t.Fatalf("workers: %q, %q", runners[0].Cfg.Name, runners[1].Cfg.Name)
	}

	// A server that mirrors nothing builds nothing.
	if got := mirrorRunners(nil, config.Default()); len(got) != 0 {
		t.Fatalf("an unconfigured server started %d workers", len(got))
	}
}
