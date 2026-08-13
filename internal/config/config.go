package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
)

const maxDurationHours = int64((1<<63 - 1) / time.Hour)

// Config is the server configuration, loaded from one TOML file with
// env overrides (LISEUR_*).
type Config struct {
	ListenAddr string `toml:"listen_addr"` // default 127.0.0.1:8585

	Database struct {
		Driver string `toml:"driver"` // sqlite | postgres
		URL    string `toml:"url"`    // sqlite: file path; postgres: DSN
	} `toml:"database"`

	Content struct {
		Root                  string `toml:"root"`                    // default ./content
		FailureRetentionHours int    `toml:"failure_retention_hours"` // default 24
		OrphanGraceHours      int    `toml:"orphan_grace_hours"`      // default 168
		RecoveryBatchSize     int    `toml:"recovery_batch_size"`     // ingest and blob housekeeping, default 100
		IngestWorkerInterval  int    `toml:"ingest_worker_interval_seconds"`
		// MaxUploadBytes bounds one uploaded file. It is the request-size
		// bound ADR-0005 requires: the EPUB validator's limits only apply
		// once bytes are staged, so without this a single upload could
		// fill the disk before anything inspected it.
		MaxUploadBytes int64 `toml:"max_upload_bytes"`
		// QuotaBytes is the per-principal logical storage limit, or 0 for
		// unlimited. Charged to a library's quota_user_id (ADR-0002).
		QuotaBytes            int64 `toml:"quota_bytes"`
		EPUBMaxEntries        int   `toml:"epub_max_entries"`
		EPUBMaxDirectoryBytes int64 `toml:"epub_max_directory_bytes"`
		EPUBMaxExpandedBytes  int64 `toml:"epub_max_expanded_bytes"`
		EPUBMaxEntryBytes     int64 `toml:"epub_max_entry_bytes"`
		EPUBMaxRatio          int64 `toml:"epub_max_compression_ratio"`
		EPUBMaxMetadataBytes  int64 `toml:"epub_max_metadata_bytes"`
		EPUBMaxXMLDepth       int   `toml:"epub_max_xml_depth"`
	} `toml:"content"`

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
	c.Content.Root = "content"
	c.Content.FailureRetentionHours = 24
	c.Content.OrphanGraceHours = 168
	c.Content.RecoveryBatchSize = 100
	c.Content.IngestWorkerInterval = 5
	c.Content.MaxUploadBytes = 512 << 20
	c.Content.QuotaBytes = 0
	epubLimits := epub.DefaultLimits()
	c.Content.EPUBMaxEntries = epubLimits.MaxEntries
	c.Content.EPUBMaxDirectoryBytes = epubLimits.MaxDirectoryBytes
	c.Content.EPUBMaxExpandedBytes = epubLimits.MaxUncompressedBytes
	c.Content.EPUBMaxEntryBytes = epubLimits.MaxEntryBytes
	c.Content.EPUBMaxRatio = int64(epubLimits.MaxCompressionRatio)
	c.Content.EPUBMaxMetadataBytes = epubLimits.MaxMetadataBytes
	c.Content.EPUBMaxXMLDepth = epubLimits.MaxXMLDepth
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
// LISEUR_CONTENT_ROOT, LISEUR_INSECURE_HTTP, LISEUR_OPEN_REGISTRATION,
// LISEUR_CORS_ORIGINS (comma-separated), LISEUR_TRUSTED_PROXIES
// (comma-separated).
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
	setStr(&c.Content.Root, "LISEUR_CONTENT_ROOT")
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
	if strings.TrimSpace(c.Content.Root) == "" {
		return fmt.Errorf("content.root is required")
	}
	if c.Content.FailureRetentionHours < 1 ||
		int64(c.Content.FailureRetentionHours) > maxDurationHours {
		return fmt.Errorf(
			"content.failure_retention_hours must be between 1 and %d",
			maxDurationHours)
	}
	if c.Content.OrphanGraceHours < 1 ||
		int64(c.Content.OrphanGraceHours) > maxDurationHours {
		return fmt.Errorf(
			"content.orphan_grace_hours must be between 1 and %d",
			maxDurationHours)
	}
	if c.Content.RecoveryBatchSize < 1 || c.Content.RecoveryBatchSize > 500 {
		return fmt.Errorf("content.recovery_batch_size must be between 1 and 500")
	}
	if c.Content.IngestWorkerInterval < 1 ||
		c.Content.IngestWorkerInterval > 3600 {
		return fmt.Errorf(
			"content.ingest_worker_interval_seconds must be between 1 and 3600")
	}
	if c.Content.MaxUploadBytes < 1 {
		return fmt.Errorf("content.max_upload_bytes must be >= 1")
	}
	if c.Content.QuotaBytes < 0 {
		return fmt.Errorf("content.quota_bytes must be >= 0 (0 disables the quota)")
	}
	if c.Content.EPUBMaxRatio < 1 {
		return fmt.Errorf("content.epub_max_compression_ratio must be >= 1")
	}
	if err := c.EPUBLimits().Validate(); err != nil {
		return fmt.Errorf("content EPUB limits are invalid: %w", err)
	}
	if c.Ops.MaxBatch < 1 {
		return fmt.Errorf("ops.max_batch must be >= 1")
	}

	if c.Ops.RetentionDays < 1 {
		return fmt.Errorf("ops.retention_days must be >= 1")
	}
	if c.Ops.InferenceGapMin < 1 {
		return fmt.Errorf("ops.inference_gap_min must be >= 1")
	}
	if c.Ops.InferenceLateHours < 1 {
		return fmt.Errorf("ops.inference_late_hours must be >= 1")
	}
	minLateHours := c.Ops.InferenceGapMin / 60
	if c.Ops.InferenceGapMin%60 != 0 {
		minLateHours++
	}
	if c.Ops.InferenceLateHours < minLateHours {
		return fmt.Errorf("ops.inference_late_hours must cover ops.inference_gap_min")
	}
	return nil
}

// EPUBLimits returns the configured bounded validator limits.
func (c Config) EPUBLimits() epub.Limits {
	ratio := uint64(0)
	if c.Content.EPUBMaxRatio > 0 {
		ratio = uint64(c.Content.EPUBMaxRatio)
	}
	return epub.Limits{
		MaxEntries:           c.Content.EPUBMaxEntries,
		MaxDirectoryBytes:    c.Content.EPUBMaxDirectoryBytes,
		MaxUncompressedBytes: c.Content.EPUBMaxExpandedBytes,
		MaxEntryBytes:        c.Content.EPUBMaxEntryBytes,
		MaxCompressionRatio:  ratio,
		MaxMetadataBytes:     c.Content.EPUBMaxMetadataBytes,
		MaxXMLDepth:          c.Content.EPUBMaxXMLDepth,
	}
}
