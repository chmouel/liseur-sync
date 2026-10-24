package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func workingAudiobookshelf(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	cfg.Audiobookshelf.Enabled = true
	cfg.Audiobookshelf.BaseURL = "https://abs.example.com/"
	cfg.Audiobookshelf.Account = "reader"
	cfg.Audiobookshelf.APIKey = "key"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the baseline audiobookshelf config is not valid: %v", err)
	}
	return cfg
}

func TestAudiobookshelfIsOffByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Audiobookshelf.Enabled || cfg.Audiobookshelf.APIKey != "" {
		t.Fatalf("audiobookshelf is configured by default: %+v", cfg.Audiobookshelf)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Audiobookshelf.PollInterval.Duration(); got != 30*time.Second {
		t.Fatalf("default poll interval: %s", got)
	}
	if got := workingAudiobookshelf(t).Audiobookshelf.BaseURL; got != "https://abs.example.com" {
		t.Fatalf("base_url not normalised: %q", got)
	}
}

func TestAudiobookshelfRefusesABrokenConfig(t *testing.T) {
	for _, tc := range []struct {
		want   string
		break_ func(*Config)
	}{
		{"audiobookshelf.base_url is required", func(c *Config) { c.Audiobookshelf.BaseURL = " " }},
		{"audiobookshelf.account", func(c *Config) { c.Audiobookshelf.Account = "" }},
		{"audiobookshelf.api_key", func(c *Config) { c.Audiobookshelf.APIKey = "" }},
		{"must be https", func(c *Config) { c.Audiobookshelf.BaseURL = "http://abs.example.com" }},
		{"username or password", func(c *Config) { c.Audiobookshelf.BaseURL = "https://u:p@abs.example.com" }},
		{"query string", func(c *Config) { c.Audiobookshelf.BaseURL = "https://abs.example.com/?token=x" }},
		{"poll_interval", func(c *Config) { c.Audiobookshelf.PollInterval = Duration(time.Second) }},
		{"timeout", func(c *Config) { c.Audiobookshelf.Timeout = 0 }},
	} {
		t.Run(tc.want, func(t *testing.T) {
			cfg := workingAudiobookshelf(t)
			tc.break_(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %q, got %v", tc.want, err)
			}
		})
	}
	cfg := workingAudiobookshelf(t)
	cfg.InsecureHTTP = true
	cfg.Audiobookshelf.BaseURL = "http://abs.lan"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("insecure_http should allow plain http: %v", err)
	}
}

func TestAudiobookshelfFromTheEnvironment(t *testing.T) {
	t.Setenv("LISEUR_ABS_ENABLED", "true")
	t.Setenv("LISEUR_ABS_BASE_URL", "https://abs.example.com")
	t.Setenv("LISEUR_ABS_ACCOUNT", "reader")
	t.Setenv("LISEUR_ABS_API_KEY", "secret")
	t.Setenv("LISEUR_ABS_POLL_INTERVAL", "7m")
	t.Setenv("LISEUR_ABS_TIMEOUT", "5s")
	t.Setenv("LISEUR_ABS_SESSION_OVERLAP", "20m")
	t.Setenv("LISEUR_ABS_ACTIVE_DAYS", "7")
	cfg := Default()
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a := cfg.Audiobookshelf
	if !a.Enabled || a.BaseURL != "https://abs.example.com" || a.Account != "reader" ||
		a.APIKey != "secret" || a.PollInterval.Duration() != 7*time.Minute || a.Timeout.Duration() != 5*time.Second || a.SessionOverlap.Duration() != 20*time.Minute || a.ActiveDays != 7 {
		t.Fatalf("environment not applied: %+v", a)
	}
}

func TestTheShippedExampleDocumentsAudiobookshelf(t *testing.T) {
	body, err := os.ReadFile("../../liseur-sync.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(body), `poll_interval = "30s"`, `poll_interval = "9m"`, 1)
	cfg, err := Load(writeConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Audiobookshelf.PollInterval.Duration(); got != 9*time.Minute {
		t.Fatalf("poll_interval did not reach audiobookshelf: %s", got)
	}
	if cfg.Audiobookshelf.Enabled || cfg.Audiobookshelf.APIKey != "" {
		t.Fatal("the shipped example turns audiobookshelf on or carries a key")
	}
}
