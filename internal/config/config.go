package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the server configuration, loaded from one TOML file with
// env overrides (LISEUR_*).
type Config struct {
	ListenAddr string `toml:"listen_addr"` // default 127.0.0.1:8585

	Database struct {
		Driver string `toml:"driver"` // sqlite | postgres
		URL    string `toml:"url"`    // sqlite: file path; postgres: DSN
	} `toml:"database"`

	// InsecureHTTP allows credential-bearing traffic over plain HTTP
	// (LAN-only setups). Default false: login, bearer tokens, kosync and
	// koplugin credentials are rejected over HTTP.
	InsecureHTTP bool `toml:"insecure_http"`

	// TrustedProxies are CIDRs whose X-Forwarded-Proto is honoured when
	// deciding whether a request arrived over HTTPS. Anything else is
	// never trusted.
	TrustedProxies []string `toml:"trusted_proxies"`

	// CORSAllowedOrigins: deny-by-default; origins listed here may call
	// the API from a browser (future web UI).
	CORSAllowedOrigins []string `toml:"cors_allowed_origins"`

	Adapters struct {
		Kosync   bool `toml:"kosync"`
		Koplugin bool `toml:"koplugin"`
	} `toml:"adapters"`

	// OpenRegistration allows kosync users/create without a pairing
	// code. Off by default (design 7.1).
	OpenRegistration bool `toml:"open_registration"`

	Ops struct {
		MaxBatch           int   `toml:"max_batch"`            // default 500
		MaxBodyBytes       int64 `toml:"max_body_bytes"`       // default 1 MiB
		MaxLocatorBytes    int   `toml:"max_locator_bytes"`    // default 16 KiB
		RetentionDays      int   `toml:"retention_days"`       // default 180
		CompactionEnabled  bool  `toml:"compaction_enabled"`   // default true
		InferenceGapMin    int   `toml:"inference_gap_min"`    // default 15
		InferenceLateHours int   `toml:"inference_late_hours"` // default 24
	} `toml:"ops"`

	PairingCodeTTLMin int `toml:"pairing_code_ttl_min"` // default 15
}

// Default returns the configuration with all documented defaults.
func Default() Config {
	var c Config
	c.ListenAddr = "127.0.0.1:8585"
	c.Database.Driver = "sqlite"
	c.Database.URL = "liseur-sync.db"
	c.Adapters.Kosync = true
	c.Adapters.Koplugin = true
	c.Ops.MaxBatch = 500
	c.Ops.MaxBodyBytes = 1 << 20
	c.Ops.MaxLocatorBytes = 16 << 10
	c.Ops.RetentionDays = 180
	c.Ops.CompactionEnabled = true
	c.Ops.InferenceGapMin = 15
	c.Ops.InferenceLateHours = 24
	c.PairingCodeTTLMin = 15
	return c
}

// applyEnv applies LISEUR_* environment overrides. Supported:
// LISEUR_LISTEN_ADDR, LISEUR_DATABASE_DRIVER, LISEUR_DATABASE_URL,
// LISEUR_INSECURE_HTTP, LISEUR_OPEN_REGISTRATION, LISEUR_CORS_ORIGINS
// (comma-separated), LISEUR_TRUSTED_PROXIES (comma-separated).
func (c *Config) applyEnv() {
	setStr := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	setBool := func(dst *bool, key string) {
		if v, ok := os.LookupEnv(key); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}
	setList := func(dst *[]string, key string) {
		if v, ok := os.LookupEnv(key); ok {
			var out []string
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			*dst = out
		}
	}
	setStr(&c.ListenAddr, "LISEUR_LISTEN_ADDR")
	setStr(&c.Database.Driver, "LISEUR_DATABASE_DRIVER")
	setStr(&c.Database.URL, "LISEUR_DATABASE_URL")
	setBool(&c.InsecureHTTP, "LISEUR_INSECURE_HTTP")
	setBool(&c.OpenRegistration, "LISEUR_OPEN_REGISTRATION")
	setList(&c.CORSAllowedOrigins, "LISEUR_CORS_ORIGINS")
	setList(&c.TrustedProxies, "LISEUR_TRUSTED_PROXIES")
}

// Validate checks the config is coherent.
func (c *Config) Validate() error {
	switch c.Database.Driver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("database.driver must be sqlite or postgres, got %q", c.Database.Driver)
	}
	if c.Database.URL == "" {
		return fmt.Errorf("database.url is required")
	}
	if c.Ops.MaxBatch < 1 {
		return fmt.Errorf("ops.max_batch must be >= 1")
	}
	if c.Ops.RetentionDays < 1 {
		return fmt.Errorf("ops.retention_days must be >= 1")
	}
	return nil
}
