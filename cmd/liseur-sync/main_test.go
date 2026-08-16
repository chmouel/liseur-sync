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
