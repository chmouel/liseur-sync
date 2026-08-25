package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// Config is the server configuration, loaded from one TOML file with
// env overrides (LISEUR_*).
type Config struct {
	ListenAddr string `toml:"listen_addr"` // default 127.0.0.1:8585

	Database struct {
		Driver string `toml:"driver"` // sqlite | postgres
		URL    string `toml:"url"`    // sqlite: file path; postgres: DSN
	} `toml:"database"`

	Content struct {
		// CacheDir is the only directory this server writes to. It holds
		// rendered covers and nothing else: everything in it can be
		// produced again from the books it was made from, so it is safe
		// to delete while the server is running (ADR-0017).
		CacheDir string `toml:"cache_dir"`
		// FolderRoots bounds where the admin panel may point a folder.
		// Adding one names a path on this machine, which is a privilege
		// beyond "can administer this application" (ADR-0013): an
		// operator who lists the directories their books live under
		// turns the panel's form from a filesystem-wide oracle into a
		// choice among trees they already meant to serve.
		//
		// Empty means "anywhere the server can read", which is what the
		// add-folder subcommand allows. A root must be absolute; a
		// folder root is accepted when it is one of these or below one.
		FolderRoots []string `toml:"folder_roots"`
		// ScanMaxFiles and ScanMaxDepth bound one traversal. A pass that
		// meets either is incomplete, and an incomplete pass never marks
		// anything missing — so raising them is how an operator with a
		// very large folder keeps absence detection working, rather than
		// a tuning knob.
		ScanMaxFiles int `toml:"scan_max_files"`
		ScanMaxDepth int `toml:"scan_max_depth"`

		// MaxUploadBytes bounds one uploaded publication (ADR-0023).
		// It is not a quota: there is no quota, and nothing counts what
		// a folder holds. It is the size above which this server stops
		// reading a request body, so a client cannot fill a disk with
		// one POST.
		MaxUploadBytes int64 `toml:"max_upload_bytes"`

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
	// the API from a browser. ReaderOrigin is allowed implicitly, since
	// a reader that cannot reach the API is not a deployment mode.
	CORSAllowedOrigins []string `toml:"cors_allowed_origins"`

	// ReaderOrigin serves the browser reader from a second hostname
	// (ADR-0007 phase 3). Empty is the default and means the reader is
	// served from the same origin as the rest of the UI.
	//
	// It buys one thing: a sandbox escape out of publication content
	// lands on an origin that holds no session cookie and serves no
	// authenticated route, so what it reaches is a short-lived
	// library-read token rather than the account. It costs an operator a
	// second hostname and certificate, which is why it is optional.
	ReaderOrigin string `toml:"reader_origin"`

	Adapters struct {
		Kosync   bool `toml:"kosync"`
		Koplugin bool `toml:"koplugin"`
	} `toml:"adapters"`

	Ops struct {
		MaxBatch           int   `toml:"max_batch"`            // default 500
		MaxBodyBytes       int64 `toml:"max_body_bytes"`       // default 1 MiB
		MaxLocatorBytes    int   `toml:"max_locator_bytes"`    // default 16 KiB
		RetentionDays      int   `toml:"retention_days"`       // default 180
		CompactionEnabled  bool  `toml:"compaction_enabled"`   // default true
		InferenceGapMin    int   `toml:"inference_gap_min"`    // default 15
		InferenceLateHours int   `toml:"inference_late_hours"` // default 24

		// Annotation bounds (ADR-0028). Each refusal is a precise 4xx,
		// nothing truncated silently. AnnotationMaxPerWork caps live
		// records per user per work, enforced inside the store's write
		// transaction. AnnotationRetentionDays is how long a deleted
		// annotation's tombstone survives so every device learns of it.
		AnnotationMaxBatch        int `toml:"annotation_max_batch"`         // default 100
		AnnotationMaxExcerptBytes int `toml:"annotation_max_excerpt_bytes"` // default 1 KiB
		AnnotationMaxBodyBytes    int `toml:"annotation_max_body_bytes"`    // default 16 KiB
		AnnotationMaxPerWork      int `toml:"annotation_max_per_work"`      // default 2000
		AnnotationRetentionDays   int `toml:"annotation_retention_days"`    // default 180
	} `toml:"ops"`

	PairingCodeTTLMin int `toml:"pairing_code_ttl_min"` // default 15
}

// Default returns the configuration with all documented defaults.
func Default() Config {
	var c Config
	c.ListenAddr = "127.0.0.1:8585"
	c.Database.Driver = "sqlite"
	c.Database.URL = "liseur-sync.db"
	c.Content.CacheDir = "cache"
	c.Content.MaxUploadBytes = 200 << 20
	epubLimits := epub.DefaultLimits()
	c.Content.EPUBMaxEntries = epubLimits.MaxEntries
	c.Content.EPUBMaxDirectoryBytes = epubLimits.MaxDirectoryBytes
	c.Content.EPUBMaxExpandedBytes = epubLimits.MaxUncompressedBytes
	c.Content.EPUBMaxEntryBytes = epubLimits.MaxEntryBytes
	c.Content.EPUBMaxRatio = int64(epubLimits.MaxCompressionRatio)
	c.Content.EPUBMaxMetadataBytes = epubLimits.MaxMetadataBytes
	c.Content.EPUBMaxXMLDepth = epubLimits.MaxXMLDepth
	c.Content.ScanMaxFiles = 200_000
	c.Content.ScanMaxDepth = 32
	c.Adapters.Kosync = true
	c.Adapters.Koplugin = true
	c.Ops.MaxBatch = 500
	c.Ops.MaxBodyBytes = 1 << 20
	c.Ops.MaxLocatorBytes = 16 << 10
	c.Ops.RetentionDays = 180
	c.Ops.CompactionEnabled = true
	c.Ops.InferenceGapMin = 15
	c.Ops.InferenceLateHours = 24
	c.Ops.AnnotationMaxBatch = 100
	c.Ops.AnnotationMaxExcerptBytes = 1 << 10
	c.Ops.AnnotationMaxBodyBytes = 16 << 10
	c.Ops.AnnotationMaxPerWork = 2000
	c.Ops.AnnotationRetentionDays = 180
	c.PairingCodeTTLMin = 15
	return c
}

// applyEnv applies LISEUR_* environment overrides. Supported:
// LISEUR_LISTEN_ADDR, LISEUR_DATABASE_DRIVER, LISEUR_DATABASE_URL,
// LISEUR_CACHE_DIR, LISEUR_INSECURE_HTTP,
// LISEUR_CORS_ORIGINS (comma-separated), LISEUR_TRUSTED_PROXIES
// (comma-separated), LISEUR_READER_ORIGIN,
// LISEUR_FOLDER_ROOTS (comma-separated).
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
	setStr(&c.Content.CacheDir, "LISEUR_CACHE_DIR")
	setStr(&c.ReaderOrigin, "LISEUR_READER_ORIGIN")
	setBool(&c.InsecureHTTP, "LISEUR_INSECURE_HTTP")
	setList(&c.CORSAllowedOrigins, "LISEUR_CORS_ORIGINS")
	setList(&c.TrustedProxies, "LISEUR_TRUSTED_PROXIES")
	setList(&c.Content.FolderRoots, "LISEUR_FOLDER_ROOTS")
}

// Validate checks the config is coherent.
func (c *Config) Validate() error {
	if err := c.validateReaderOrigin(); err != nil {
		return err
	}
	switch c.Database.Driver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("database.driver must be sqlite or postgres, got %q", c.Database.Driver)
	}
	if c.Database.URL == "" {
		return fmt.Errorf("database.url is required")
	}
	if strings.TrimSpace(c.Content.CacheDir) == "" {
		return fmt.Errorf("content.cache_dir is required")
	}
	if c.Content.ScanMaxFiles < 1 {
		return fmt.Errorf("content.scan_max_files must be >= 1")
	}
	if c.Content.MaxUploadBytes < 1 {
		return fmt.Errorf("content.max_upload_bytes must be >= 1")
	}
	if c.Content.ScanMaxDepth < 1 || c.Content.ScanMaxDepth > 256 {
		return fmt.Errorf("content.scan_max_depth must be between 1 and 256")
	}
	for i, root := range c.Content.FolderRoots {
		trimmed := strings.TrimSpace(root)
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf(
				"content.folder_roots[%d] (%q) must be an absolute path", i, root)
		}
		c.Content.FolderRoots[i] = filepath.Clean(trimmed)
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
	if c.Ops.AnnotationMaxBatch < 1 {
		return fmt.Errorf("ops.annotation_max_batch must be >= 1")
	}
	if c.Ops.AnnotationMaxExcerptBytes < 1 {
		return fmt.Errorf("ops.annotation_max_excerpt_bytes must be >= 1")
	}
	if c.Ops.AnnotationMaxBodyBytes < 1 {
		return fmt.Errorf("ops.annotation_max_body_bytes must be >= 1")
	}
	if c.Ops.AnnotationMaxPerWork < 1 {
		return fmt.Errorf("ops.annotation_max_per_work must be >= 1")
	}
	if c.Ops.AnnotationRetentionDays < 1 {
		return fmt.Errorf("ops.annotation_retention_days must be >= 1")
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

// validateReaderOrigin refuses a reader origin that would not work, at
// startup rather than at the first reader page.
//
// The strictness is the point rather than fussiness. This value decides
// which host is allowed to hold reader credentials and which requests
// the API answers cross-origin, so it has to be an origin — a scheme and
// a host — and nothing else. A trailing path would silently never match
// the Origin header a browser sends, and the operator would be left
// looking at a reader that cannot reach the API for no visible reason.
func (c *Config) validateReaderOrigin() error {
	raw := strings.TrimSpace(c.ReaderOrigin)
	c.ReaderOrigin = strings.TrimSuffix(raw, "/")
	if c.ReaderOrigin == "" {
		return nil
	}
	u, err := url.Parse(c.ReaderOrigin)
	if err != nil || u.Host == "" {
		return fmt.Errorf(
			"reader_origin must be an absolute origin like https://read.example.com, got %q",
			c.ReaderOrigin)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// The reader origin holds a live API credential. Over plain HTTP
		// anybody on the path can take it out of the page, which is a
		// worse posture than not splitting the origins at all.
		if !c.InsecureHTTP {
			return fmt.Errorf(
				"reader_origin %q is http; set insecure_http = true if that is deliberate",
				c.ReaderOrigin)
		}
	default:
		return fmt.Errorf("reader_origin must be http or https, got %q", u.Scheme)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf(
			"reader_origin must be a bare origin with no path, got %q", c.ReaderOrigin)
	}
	// A reader origin equal to nothing in particular is fine; a reader
	// origin the operator also listed by hand is not a conflict, it is
	// the same permission stated twice.
	return nil
}

// ReaderOriginHost is the host:port a request must arrive at to be
// treated as the reader origin. Empty when the mode is off.
func (c Config) ReaderOriginHost() string {
	if c.ReaderOrigin == "" {
		return ""
	}
	u, err := url.Parse(c.ReaderOrigin)
	if err != nil {
		return ""
	}
	return u.Host
}

// BrowserOrigins is every origin the API answers cross-origin for. The
// reader origin is included whether or not the operator also listed it,
// because a reader that cannot call the API is not a deployment mode.
func (c Config) BrowserOrigins() []string {
	out := make([]string, 0, len(c.CORSAllowedOrigins)+1)
	seen := map[string]bool{}
	for _, origin := range append([]string{c.ReaderOrigin}, c.CORSAllowedOrigins...) {
		origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
		if origin == "" || seen[origin] {
			continue
		}
		seen[origin] = true
		out = append(out, origin)
	}
	return out
}
