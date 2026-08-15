package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata/provider"
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
		TrashRetentionHours   int    `toml:"trash_retention_hours"`   // default 720
		RecoveryBatchSize     int    `toml:"recovery_batch_size"`     // ingest and blob housekeeping, default 100
		IngestWorkerInterval  int    `toml:"ingest_worker_interval_seconds"`
		// MaxUploadBytes bounds one uploaded file. It is the request-size
		// bound ADR-0005 requires: the EPUB validator's limits only apply
		// once bytes are staged, so without this a single upload could
		// fill the disk before anything inspected it.
		MaxUploadBytes int64 `toml:"max_upload_bytes"`
		// MaxStagingBytes bounds every in-flight upload together, or 0
		// for unlimited. max_upload_bytes and quota_bytes both pass
		// uploads that are individually fine and collectively fill the
		// disk, and staged bytes are charged to nobody until they are
		// promoted.
		MaxStagingBytes int64 `toml:"max_staging_bytes"`
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
		// RefreshTick is how often, in seconds, the server looks for a
		// library whose refresh has come due, or 0 to run no refreshes at
		// all. It is not how often a root is swept: each library carries
		// its own interval, and this is the granularity at which those
		// are noticed. Setting it to a minute costs one query a minute
		// and makes an hourly library an hourly library rather than an
		// hourly-give-or-take-five-minutes one.
		//
		// Notifications are not used: a sweep is the only thing allowed
		// to conclude that a book is gone (ADR-0002), so the interval is
		// the real latency budget rather than a fallback for a missed
		// event.
		RefreshTick int `toml:"refresh_tick_seconds"`
		// WatchedMaxFiles and WatchedMaxDepth bound one traversal. A
		// sweep that meets either is incomplete, and an incomplete sweep
		// never marks anything missing, so raising them is how an
		// operator with a very large library keeps absence detection
		// working rather than a tuning knob.
		WatchedMaxFiles int `toml:"watched_max_files"`
		WatchedMaxDepth int `toml:"watched_max_depth"`
		// LibraryRoots bounds where the admin panel may point a
		// directory or Calibre library. Creating one names a path on
		// this machine, which is a privilege beyond "can administer
		// this application" (ADR-0013): an operator who lists the
		// directories their libraries live under turns the panel's form
		// from a filesystem-wide oracle into a choice among trees they
		// already meant to serve.
		//
		// Empty is the default and means "anywhere the server can
		// read", which is what the `add-library` subcommand has always
		// allowed. A root must be an absolute path; a library root is
		// accepted when it is one of these or below one.
		LibraryRoots []string `toml:"library_roots"`
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

	// Metadata configures optional lookups against external services.
	// Everything here is off by default: a self-hosted server that talks
	// to nobody is the posture an operator gets without asking, and
	// ADR-0004 keeps it that way. Nothing in the ingest path uses these
	// — a scan that phoned home would make a library's contents visible
	// to a third party as a side effect of having files on disk.
	Metadata struct {
		// Providers names the services that may be queried. Empty
		// disables external lookup entirely. Known values are
		// "openlibrary" and "googlebooks"; an unknown one is refused at
		// startup rather than ignored, because silence looks exactly
		// like a service being down.
		Providers []string `toml:"providers"`
		// LookupTimeoutSeconds bounds one whole lookup, redirects
		// included.
		LookupTimeoutSeconds int `toml:"lookup_timeout_seconds"`
		// LookupMaxBytes bounds one provider response. A response over
		// it is refused rather than truncated: half a JSON document is
		// a book with fields silently missing.
		LookupMaxBytes int64 `toml:"lookup_max_bytes"`
		// LookupMaxRedirects bounds the redirect chain. The allowlist is
		// re-checked on every hop regardless.
		LookupMaxRedirects int `toml:"lookup_max_redirects"`
	} `toml:"metadata"`

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
	// A month is long enough that someone notices a mistaken deletion
	// before the bytes are gone, and short enough that the trash is not
	// a second copy of the library.
	c.Content.TrashRetentionHours = 720
	c.Content.RecoveryBatchSize = 100
	c.Content.IngestWorkerInterval = 5
	c.Content.MaxUploadBytes = 512 << 20
	// Sixteen uploads of the default maximum size. Enough that a small
	// instance never meets it, low enough that a runaway client cannot
	// spend a disk before anything notices.
	c.Content.MaxStagingBytes = 8 << 30
	c.Content.QuotaBytes = 0
	epubLimits := epub.DefaultLimits()
	c.Content.EPUBMaxEntries = epubLimits.MaxEntries
	c.Content.EPUBMaxDirectoryBytes = epubLimits.MaxDirectoryBytes
	c.Content.EPUBMaxExpandedBytes = epubLimits.MaxUncompressedBytes
	c.Content.EPUBMaxEntryBytes = epubLimits.MaxEntryBytes
	c.Content.EPUBMaxRatio = int64(epubLimits.MaxCompressionRatio)
	c.Content.EPUBMaxMetadataBytes = epubLimits.MaxMetadataBytes
	c.Content.EPUBMaxXMLDepth = epubLimits.MaxXMLDepth
	// Five minutes is short enough that a book dropped into a folder is
	// there before anybody goes looking for it, and long enough that a
	// spinning disk is not read continuously.
	c.Content.RefreshTick = 60
	c.Content.WatchedMaxFiles = 200_000
	c.Content.WatchedMaxDepth = 32
	c.Adapters.Kosync = true
	c.Adapters.Koplugin = true
	c.Ops.MaxBatch = 500
	c.Ops.MaxBodyBytes = 1 << 20
	c.Ops.MaxLocatorBytes = 16 << 10
	c.Ops.RetentionDays = 180
	c.Ops.CompactionEnabled = true
	c.Ops.InferenceGapMin = 15
	c.Ops.InferenceLateHours = 24
	// Metadata.Providers stays empty: external lookup is opt-in. The
	// bounds have defaults anyway, so turning it on is one line rather
	// than four.
	c.Metadata.LookupTimeoutSeconds = int(provider.DefaultTimeout / time.Second)
	c.Metadata.LookupMaxBytes = provider.DefaultMaxBytes
	c.Metadata.LookupMaxRedirects = provider.DefaultMaxRedirects
	c.PairingCodeTTLMin = 15
	return c
}

// applyEnv applies LISEUR_* environment overrides. Supported:
// LISEUR_LISTEN_ADDR, LISEUR_DATABASE_DRIVER, LISEUR_DATABASE_URL,
// LISEUR_CONTENT_ROOT, LISEUR_INSECURE_HTTP, LISEUR_OPEN_REGISTRATION,
// LISEUR_CORS_ORIGINS (comma-separated), LISEUR_TRUSTED_PROXIES
// (comma-separated), LISEUR_METADATA_PROVIDERS (comma-separated),
// LISEUR_READER_ORIGIN, LISEUR_LIBRARY_ROOTS (comma-separated).
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
	setStr(&c.ReaderOrigin, "LISEUR_READER_ORIGIN")
	setBool(&c.InsecureHTTP, "LISEUR_INSECURE_HTTP")
	setBool(&c.OpenRegistration, "LISEUR_OPEN_REGISTRATION")
	setList(&c.CORSAllowedOrigins, "LISEUR_CORS_ORIGINS")
	setList(&c.TrustedProxies, "LISEUR_TRUSTED_PROXIES")
	setList(&c.Metadata.Providers, "LISEUR_METADATA_PROVIDERS")
	setList(&c.Content.LibraryRoots, "LISEUR_LIBRARY_ROOTS")
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
	if c.Content.TrashRetentionHours < 1 ||
		int64(c.Content.TrashRetentionHours) > maxDurationHours {
		return fmt.Errorf(
			"content.trash_retention_hours must be between 1 and %d",
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
	if c.Content.MaxStagingBytes < 0 {
		return fmt.Errorf("content.max_staging_bytes must be >= 0 (0 disables the cap)")
	}
	// A cap below one upload refuses every upload, including the first,
	// and would look like a broken server rather than a misconfigured one.
	if c.Content.MaxStagingBytes > 0 && c.Content.MaxStagingBytes < c.Content.MaxUploadBytes {
		return fmt.Errorf(
			"content.max_staging_bytes (%d) must be >= content.max_upload_bytes (%d), or no upload can ever be accepted",
			c.Content.MaxStagingBytes, c.Content.MaxUploadBytes)
	}
	if c.Content.QuotaBytes < 0 {
		return fmt.Errorf("content.quota_bytes must be >= 0 (0 disables the quota)")
	}
	if c.Content.RefreshTick < 0 || c.Content.RefreshTick > 86400 {
		return fmt.Errorf(
			"content.refresh_tick_seconds must be between 0 and 86400 (0 disables refreshing)")
	}
	if c.Content.WatchedMaxFiles < 1 {
		return fmt.Errorf("content.watched_max_files must be >= 1")
	}
	if c.Content.WatchedMaxDepth < 1 || c.Content.WatchedMaxDepth > 256 {
		return fmt.Errorf("content.watched_max_depth must be between 1 and 256")
	}
	for i, root := range c.Content.LibraryRoots {
		trimmed := strings.TrimSpace(root)
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf(
				"content.library_roots[%d] (%q) must be an absolute path", i, root)
		}
		c.Content.LibraryRoots[i] = filepath.Clean(trimmed)
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
	// A provider this build does not have is refused here rather than
	// dropped, because an operator who wrote "openlibary" and got
	// nothing back would conclude the service was down and go looking
	// in the wrong place.
	if _, err := provider.New(c.Metadata.Providers, c.MetadataLimits()); err != nil {
		return fmt.Errorf("metadata.providers: %w", err)
	}
	if c.Metadata.LookupTimeoutSeconds < 0 {
		return fmt.Errorf("metadata.lookup_timeout_seconds must be >= 0")
	}
	if c.Metadata.LookupMaxBytes < 0 {
		return fmt.Errorf("metadata.lookup_max_bytes must be >= 0")
	}
	if c.Metadata.LookupMaxRedirects < 0 {
		return fmt.Errorf("metadata.lookup_max_redirects must be >= 0")
	}
	return nil
}

// MetadataLimits is the bound on one external lookup. A zero in any
// field takes the package default rather than meaning "no limit", since
// an unbounded external call is the one thing this must never be.
func (c *Config) MetadataLimits() provider.Limits {
	return provider.Limits{
		Timeout:      time.Duration(c.Metadata.LookupTimeoutSeconds) * time.Second,
		MaxBytes:     c.Metadata.LookupMaxBytes,
		MaxRedirects: c.Metadata.LookupMaxRedirects,
	}
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
