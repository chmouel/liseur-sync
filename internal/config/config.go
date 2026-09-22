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

		// Settings bounds. A settings key is opaque to the server, so
		// nothing here reads a value; these only stop one account's
		// key/value store from growing without limit. The wire format
		// has no delete — a client represents "unset" as a value — so
		// nothing ever reclaims a key, and the per-account cap is the
		// only thing standing between a buggy client and unbounded
		// rows. Enforced inside the write transaction, as
		// AnnotationMaxPerWork is.
		SettingsMaxPerAccount int `toml:"settings_max_per_account"` // default 256
		SettingsMaxKeyBytes   int `toml:"settings_max_key_bytes"`   // default 128
		SettingsMaxValueBytes int `toml:"settings_max_value_bytes"` // default 4 KiB
	} `toml:"ops"`

	PairingCodeTTLMin int `toml:"pairing_code_ttl_min"` // default 15

	// WebSessionTTLDays is how long a browser session stays valid
	// without being used. It slides: every authenticated request pushes
	// the expiry back to now + this, so an account somebody reads with
	// never gets signed out, and one left alone lapses. Default 180.
	WebSessionTTLDays int `toml:"web_session_ttl_days"`

	// Mirror configures outbound synchronisation with a peer server
	// that speaks KOReader's sync protocol (ADR-0047). Disabled by
	// default, and the only part of this server that dials out.
	Mirror MirrorConfig `toml:"mirror"`
}

// MirrorConfig is one peer this server mirrors reading to and from.
// There is exactly one, for exactly one account: the credential lives
// here rather than in a table because a secret this server must
// *present* cannot be hashed, and a single account makes configuration
// the honest place to keep it (ADR-0047). It is never logged and never
// appears in a response.
type MirrorConfig struct {
	Enabled bool `toml:"enabled"`
	// Protocol is how the peer is spoken to. "kosync" is KOReader's
	// sync protocol, which any peer worth pointing this at implements
	// and which does not move; it is the default for both reasons.
	// "bookorbit" is BookOrbit's own REST API, which carries a CFI
	// rather than a percentage and costs a full account password to
	// reach (ADR-0048).
	Protocol string `toml:"protocol"`
	// Name labels this peer. It is not cosmetic: it keys the mirror's
	// bookkeeping and prefixes the device id every position taken from
	// the peer is filed under, so a reader sees where a position came
	// from. Changing it makes the mirror forget what it has already
	// exchanged and start filing under a new device, so it is picked
	// once and left alone.
	Name string `toml:"name"`
	// BaseURL is the peer's root. For kosync it is the same URL a
	// KOReader device would be pointed at; for BookOrbit's native
	// protocol it is the API root, ending in /api/v1. Must be HTTPS
	// unless insecure_http is set, because the credential travels on
	// every request.
	BaseURL string `toml:"base_url"`
	// Account is the liseur-sync username whose reading is mirrored.
	// The mirror reads and writes nothing outside it.
	Account string `toml:"account"`
	// RemoteUser is the account name on the peer. Both protocols need
	// it; only the credential beside it differs.
	RemoteUser string `toml:"remote_user"`
	// RemoteKey is kosync's credential: the MD5-derived key KOReader
	// sends as x-auth-key. It unlocks progress-by-fingerprint on the
	// peer and nothing else.
	RemoteKey string `toml:"remote_key"`
	// RemotePassword is the native protocol's credential, and it is
	// the peer account's real password, because BookOrbit offers a
	// native client nothing narrower. It opens that whole account, so
	// it is the reason kosync remains the default (ADR-0048).
	RemotePassword string `toml:"remote_password"`
	// DeviceID is the device name this server writes under on the peer.
	// It is also how it recognises its own echo coming back, so it must
	// be stable across restarts. The native protocol has no device
	// field on a position, so there it is only the label the peer
	// shows for this server's session.
	DeviceID string `toml:"device_id"`
	// PeerPathPrefix and LocalPathPrefix reconcile two views of one
	// disk. A peer in a container reports the path it sees, which is
	// not the path this server sees; naming both roots turns the
	// peer's path into a check rather than a curiosity. Optional: with
	// them unset the match is made on the file's size and name alone.
	PeerPathPrefix  string `toml:"peer_path_prefix"`
	LocalPathPrefix string `toml:"local_path_prefix"`
	// PollInterval is how often the peer is asked. It has no push.
	PollInterval Duration `toml:"poll_interval"`
	// ActiveDays bounds what is polled: only works read within this
	// many days are asked about, so the cost does not grow with the
	// size of the library.
	ActiveDays int `toml:"active_days"`
	// Timeout bounds one request to the peer.
	Timeout Duration `toml:"timeout"`
}

// The protocols a mirror can speak. They are spelled here rather than
// in internal/mirror because configuration is validated before a
// mirror exists, and a peer that cannot be spoken to is refused at
// startup rather than discovered on the first pass.
const (
	ProtocolKosync    = "kosync"
	ProtocolBookOrbit = "bookorbit"
)

// Duration is a time.Duration that reads from TOML as a string like
// "5m". The standard decoder has no duration type and the alternative
// is a second unit-bearing field name per knob.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// WebSessionTTL is the browser session window as a duration.
func (c Config) WebSessionTTL() time.Duration {
	return time.Duration(c.WebSessionTTLDays) * 24 * time.Hour
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
	c.Ops.SettingsMaxPerAccount = 256
	c.Ops.SettingsMaxKeyBytes = 128
	c.Ops.SettingsMaxValueBytes = 4 << 10
	c.PairingCodeTTLMin = 15
	c.WebSessionTTLDays = 180
	c.Mirror.Name = "peer"
	c.Mirror.Protocol = ProtocolKosync
	c.Mirror.DeviceID = "liseur-sync"
	c.Mirror.PollInterval = Duration(5 * time.Minute)
	c.Mirror.ActiveDays = 30
	c.Mirror.Timeout = Duration(20 * time.Second)
	return c
}

// applyEnv applies LISEUR_* environment overrides. Supported:
// LISEUR_LISTEN_ADDR, LISEUR_DATABASE_DRIVER, LISEUR_DATABASE_URL,
// LISEUR_CACHE_DIR, LISEUR_INSECURE_HTTP,
// LISEUR_CORS_ORIGINS (comma-separated), LISEUR_TRUSTED_PROXIES
// (comma-separated), LISEUR_READER_ORIGIN,
// LISEUR_FOLDER_ROOTS (comma-separated),
// LISEUR_MIRROR_ENABLED, LISEUR_MIRROR_NAME, LISEUR_MIRROR_BASE_URL,
// LISEUR_MIRROR_ACCOUNT, LISEUR_MIRROR_PROTOCOL,
// LISEUR_MIRROR_REMOTE_USER, LISEUR_MIRROR_REMOTE_KEY,
// LISEUR_MIRROR_REMOTE_PASSWORD, LISEUR_MIRROR_DEVICE_ID,
// LISEUR_MIRROR_PEER_PATH_PREFIX, LISEUR_MIRROR_LOCAL_PATH_PREFIX.
//
// The mirror's credential is an environment variable on purpose: it
// belongs beside the database URL in a deployment's .env rather than in
// a file somebody might commit.
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

	setBool(&c.Mirror.Enabled, "LISEUR_MIRROR_ENABLED")
	setStr(&c.Mirror.Name, "LISEUR_MIRROR_NAME")
	setStr(&c.Mirror.BaseURL, "LISEUR_MIRROR_BASE_URL")
	setStr(&c.Mirror.Account, "LISEUR_MIRROR_ACCOUNT")
	setStr(&c.Mirror.Protocol, "LISEUR_MIRROR_PROTOCOL")
	setStr(&c.Mirror.RemoteUser, "LISEUR_MIRROR_REMOTE_USER")
	setStr(&c.Mirror.RemoteKey, "LISEUR_MIRROR_REMOTE_KEY")
	setStr(&c.Mirror.RemotePassword, "LISEUR_MIRROR_REMOTE_PASSWORD")
	setStr(&c.Mirror.DeviceID, "LISEUR_MIRROR_DEVICE_ID")
	setStr(&c.Mirror.PeerPathPrefix, "LISEUR_MIRROR_PEER_PATH_PREFIX")
	setStr(&c.Mirror.LocalPathPrefix, "LISEUR_MIRROR_LOCAL_PATH_PREFIX")
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
	if c.Ops.SettingsMaxPerAccount < 1 {
		return fmt.Errorf("ops.settings_max_per_account must be >= 1")
	}
	if c.Ops.SettingsMaxKeyBytes < 1 {
		return fmt.Errorf("ops.settings_max_key_bytes must be >= 1")
	}
	if c.Ops.SettingsMaxValueBytes < 1 {
		return fmt.Errorf("ops.settings_max_value_bytes must be >= 1")
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
	if c.WebSessionTTLDays < 1 || c.WebSessionTTLDays > 3650 {
		return fmt.Errorf("web_session_ttl_days must be between 1 and 3650")
	}
	if err := c.validateMirror(); err != nil {
		return err
	}
	return nil
}

// validateMirror refuses a half-configured mirror rather than starting
// one that will fail on its first request. A mirror missing any of its
// four required settings is a deployment mistake, not a degraded mode
// (ADR-0047).
func (c *Config) validateMirror() error {
	m := &c.Mirror
	m.Name = strings.TrimSpace(m.Name)
	m.Protocol = strings.ToLower(strings.TrimSpace(m.Protocol))
	m.BaseURL = strings.TrimSpace(m.BaseURL)
	m.Account = strings.TrimSpace(m.Account)
	m.RemoteUser = strings.TrimSpace(m.RemoteUser)
	m.DeviceID = strings.TrimSpace(m.DeviceID)
	m.PeerPathPrefix = strings.TrimSpace(m.PeerPathPrefix)
	m.LocalPathPrefix = strings.TrimSpace(m.LocalPathPrefix)
	if m.Protocol == "" {
		// A file written before there was a choice means the only
		// protocol there was.
		m.Protocol = ProtocolKosync
	}
	if !m.Enabled {
		return nil
	}
	type setting struct{ name, value string }
	required := []setting{
		{"mirror.name", m.Name},
		{"mirror.base_url", m.BaseURL},
		{"mirror.account", m.Account},
		{"mirror.remote_user", m.RemoteUser},
		{"mirror.device_id", m.DeviceID},
	}
	switch m.Protocol {
	case ProtocolKosync:
		required = append(required, setting{"mirror.remote_key", m.RemoteKey})
	case ProtocolBookOrbit:
		required = append(required, setting{"mirror.remote_password", m.RemotePassword})
	default:
		return fmt.Errorf("mirror.protocol must be %q or %q, got %q",
			ProtocolKosync, ProtocolBookOrbit, m.Protocol)
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("%s is required when the mirror is enabled", r.name)
		}
	}
	// Naming one end of the path mapping and not the other would
	// rewrite a peer's path into nothing and silently stop confirming
	// anything, which is worse than not mapping at all.
	if (m.PeerPathPrefix == "") != (m.LocalPathPrefix == "") {
		return fmt.Errorf("mirror.peer_path_prefix and mirror.local_path_prefix must be set together")
	}
	// The name is joined to a remote device id with a colon to make the
	// device a reader sees, so a name carrying one would make that id
	// ambiguous about where it split.
	if strings.ContainsAny(m.Name, ": \t") {
		return fmt.Errorf("mirror.name must not contain a colon or whitespace, got %q", m.Name)
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("mirror.base_url must be an absolute URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		// The credential is sent on every request, so this is the same
		// boundary the inbound adapters hold.
		if !c.InsecureHTTP {
			return fmt.Errorf("mirror.base_url must be https unless insecure_http is set")
		}
	default:
		return fmt.Errorf("mirror.base_url must be http or https, got %q", u.Scheme)
	}
	// An API root is a scheme, a host and a path. A password in the
	// userinfo would be logged with the URL at every startup, and a
	// query or a fragment would be carried onto every route built from
	// it, so none of the three is accepted rather than quietly dropped.
	if u.User != nil {
		return fmt.Errorf("mirror.base_url must not contain a username or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("mirror.base_url must not contain a query string or fragment")
	}
	m.BaseURL = strings.TrimSuffix(u.String(), "/")
	if m.PollInterval.Duration() < time.Minute {
		return fmt.Errorf("mirror.poll_interval must be at least 1m")
	}
	if m.ActiveDays < 1 {
		return fmt.Errorf("mirror.active_days must be >= 1")
	}
	if m.Timeout.Duration() < time.Second {
		return fmt.Errorf("mirror.timeout must be at least 1s")
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
