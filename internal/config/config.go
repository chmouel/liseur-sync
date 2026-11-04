package config

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
		// MaxAudiobookUploadBytes is the same bound for an audiobook
		// uploaded onto a book (ADR-0058), which runs to gigabytes.
		MaxAudiobookUploadBytes int64 `toml:"max_audiobook_upload_bytes"`

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
		// nothing here reads a value; these only stop one device's
		// key/value store from growing without limit. The wire format
		// has no delete — a client represents "unset" as a value — so
		// nothing ever reclaims a key, and the cap is the only thing
		// standing between a buggy client and unbounded rows.
		// SettingsMaxPerAccount keeps its name because the loader
		// refuses unknown keys, but it caps each device (ADR-0050).
		// Enforced inside the write transaction, as
		// AnnotationMaxPerWork is.
		SettingsMaxPerAccount int `toml:"settings_max_per_account"` // default 256
		SettingsMaxKeyBytes   int `toml:"settings_max_key_bytes"`   // default 128
		SettingsMaxValueBytes int `toml:"settings_max_value_bytes"` // default 4 KiB

		// Translation cache bounds (ADR-0052). The push shares
		// max_batch with ops: it is the same shape, a batched atomic
		// append, and one number for "how many items may one request
		// carry" is easier to keep honest. The others have no ops
		// counterpart: a translation is bounded per value (the client
		// refuses passages over 2,000 characters itself, so 8 KiB is
		// generous in UTF-8), one pull page is bounded, and the whole
		// cache is bounded in bytes per user — enforced inside the push
		// transaction, evicting the oldest-updated rows first.
		TranslationMaxValueBytes int   `toml:"translation_max_value_bytes"` // default 8 KiB
		TranslationMaxPull       int   `toml:"translation_max_pull"`        // default 500
		TranslationBudgetBytes   int64 `toml:"translation_budget_bytes"`    // default 32 MiB
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
	//
	// It is the single entry this setting has always been. Mirrors below
	// adds more of them, and the two are the same kind of thing: an
	// entry's name keys its bookkeeping, so a deployment can keep this
	// table exactly as it is and add peers beside it (ADR-0061).
	Mirror MirrorConfig `toml:"mirror"`

	// Mirrors are further peers, each with its own name, credential and
	// local account. A field left at zero takes the same default the
	// single entry above documents.
	Mirrors []MirrorConfig `toml:"mirrors"`

	// mirrorDefined records whether the configuration actually carries
	// the single [mirror] entry: an operator's file has one, the
	// environment may supply one, and neither is true of a server that
	// never mirrored anything. It is what keeps the entry's default name
	// from appearing as a peer nobody configured.
	mirrorDefined bool

	// Audiobookshelf follows audiobooks on one Audiobookshelf server
	// (ADR-0055). Disabled by default; independent of the mirror.
	Audiobookshelf AudiobookshelfConfig `toml:"audiobookshelf"`

	// SpeechToText is the transcription service both audiobook paths use
	// to pin a listening position to a sentence: native folder audiobooks
	// and Audiobookshelf alike, so it belongs to neither.
	SpeechToText SpeechToTextConfig `toml:"speech_to_text"`
}

// SpeechToTextConfig is an OpenAI-compatible transcription endpoint. A
// configured one receives short slices of a reader's audiobook.
type SpeechToTextConfig struct {
	// Enabled is on by default: Active still needs a usable endpoint.
	Enabled bool `toml:"enabled"`
	// URL is the API root, e.g. https://api.groq.com/openai/v1 or
	// http://127.0.0.1:8000/v1. Left empty it is the Groq default, which
	// stays off until APIKey is set.
	URL string `toml:"url"`
	// APIKey is an optional Bearer token for URL. Prefer LISEUR_STT_API_KEY.
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

// Active reports whether transcription can be attempted. The default
// Groq endpoint refuses anonymous calls, so it needs a key to count.
func (s SpeechToTextConfig) Active() bool {
	if !s.Enabled || s.URL == "" {
		return false
	}
	return !strings.Contains(s.URL, "api.groq.com") || s.APIKey != ""
}

// AudiobookshelfConfig is the one Audiobookshelf server whose listening
// follows one account's reading, and back. Like the mirror's, the
// credential lives here because it must be presented, and it is never
// logged, stored or returned.
type AudiobookshelfConfig struct {
	SessionOverlap Duration `toml:"session_overlap"`
	ActiveDays     int      `toml:"active_days"`
	Enabled        bool     `toml:"enabled"`
	// BaseURL is the server's root, the URL its web app is served at.
	// Must be HTTPS unless insecure_http is set.
	BaseURL string `toml:"base_url"`
	// Account is the liseur-sync username whose reading is followed.
	Account string `toml:"account"`
	// APIKey is an Audiobookshelf API key for the listening account.
	// Prefer LISEUR_ABS_API_KEY.
	APIKey string `toml:"api_key"`
	// PollInterval is how often the server is asked; it has no push.
	PollInterval Duration `toml:"poll_interval"`
	// Timeout bounds one request.
	Timeout Duration `toml:"timeout"`
}

// MirrorConfig is one peer this server mirrors reading to and from.
// The credential lives here rather than in a table because a secret this
// server must *present* cannot be hashed, which makes configuration the
// honest place to keep it (ADR-0047). It is never logged and never
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
	// ResolveRetryInterval is how long a BookOrbit book lookup that
	// found nothing is remembered before it is tried again. BookOrbit
	// exposes no by-fingerprint lookup, so unresolved books are not
	// searched on every poll.
	ResolveRetryInterval Duration `toml:"resolve_retry_interval"`
	// ActiveDays bounds what is polled: only works read within this
	// many days are asked about, so the cost does not grow with the
	// size of the library.
	ActiveDays int `toml:"active_days"`
	// Timeout bounds one request to the peer.
	Timeout Duration `toml:"timeout"`

	// legacyBaseURL is BaseURL as a build before this one folded it:
	// the address as typed with one trailing slash removed, which that
	// build's identity folded with one more removed. Validation records
	// it and nothing else touches it, so it is neither written to a
	// config file nor settable by hand.
	legacyBaseURL string
}

// LegacyPeerBaseURL is the peer address as a build before this one
// folded it into a cursor identity, and empty when no validation has
// seen this entry. A mirror strips one more trailing slash off it to
// reach the exact digest that build stored; current digests come from
// BaseURL.
func (m MirrorConfig) LegacyPeerBaseURL() string { return m.legacyBaseURL }

// The protocols a mirror can speak. They are spelled here rather than
// in internal/mirror because configuration is validated before a
// mirror exists, and a peer that cannot be spoken to is refused at
// startup rather than discovered on the first pass.
const (
	ProtocolKosync    = "kosync"
	ProtocolBookOrbit = "bookorbit"
)

// mirrorListName is what a [[mirrors]] entry may be called. The name
// becomes an environment variable name and a URL query value, so it is
// restricted to what survives both unchanged: lower case, digits and
// dashes. The 32-character bound keeps the variable name short enough to
// be typed.
var mirrorListName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// mirrorDefault is the entry every mirror starts from, so an omitted
// field behaves the same in the single entry and in a list entry.
func mirrorDefault() MirrorConfig {
	c := Default()
	return c.Mirror
}

// applyMirrorDefaults fills a list entry's zero fields with the single
// entry's defaults. A written "0s" therefore also means "the default":
// a duration of zero is not a setting anybody wants, and a peer polled
// flat out is one way to hammer somebody else's server.
func applyMirrorDefaults(m *MirrorConfig) {
	d := mirrorDefault()
	if m.Protocol == "" {
		m.Protocol = d.Protocol
	}
	if m.DeviceID == "" {
		m.DeviceID = d.DeviceID
	}
	if m.PollInterval == 0 {
		m.PollInterval = d.PollInterval
	}
	if m.ResolveRetryInterval == 0 {
		m.ResolveRetryInterval = d.ResolveRetryInterval
	}
	if m.ActiveDays == 0 {
		m.ActiveDays = d.ActiveDays
	}
	if m.Timeout == 0 {
		m.Timeout = d.Timeout
	}
}

// MirrorTableDefined reports whether this configuration carries the
// single [mirror] entry, either from the file or from the environment.
func (c Config) MirrorTableDefined() bool { return c.mirrorDefined }

// SetMirrorTableDefined records that a candidate configuration carries
// the single entry. The admin form builds one from a file it read and
// validates it the way startup would.
func (c *Config) SetMirrorTableDefined(defined bool) { c.mirrorDefined = defined }

// EnabledMirrors is every peer this server should be talking to: the
// single entry first, when there is one, then the list in file order.
// It is the only view of the mirror configuration startup reads.
func (c Config) EnabledMirrors() []MirrorConfig {
	var out []MirrorConfig
	if c.mirrorDefined && c.Mirror.Enabled {
		out = append(out, c.Mirror)
	}
	for _, m := range c.Mirrors {
		if m.Enabled {
			out = append(out, m)
		}
	}
	return out
}

// MirrorRoot is a peer address with every trailing slash removed. It is
// idempotent on purpose: the clients that build routes from the address
// call it, so two spellings of one root cannot produce two different
// routes. A base URL with no path at all stays host-only.
func MirrorRoot(baseURL string) string {
	return strings.TrimRight(baseURL, "/")
}

// mirrorTargetKey folds a peer address down to the account it names, so
// that two spellings of one peer cannot pass as two. The route a protocol
// is spoken at is not part of the identity: BookOrbit serves its native
// API and its KOReader route from one installation, and a port the scheme
// already implies is the same port. The scheme itself is dropped for the
// same reason — both end at one server with one account behind it.
// Discovery over the network is nobody's job here, so a path that is not
// one of the two documented roots stays part of the key.
//
// This is a validation key and nothing more: a mirror's cursors and device
// are keyed by its name and identified by peerIdentity, which keeps the
// protocol and the address as typed, so relaxing this comparison never
// makes a worker adopt another one's bookkeeping.
func mirrorTargetKey(m MirrorConfig) string {
	var host, port, route string
	if u, err := url.Parse(m.BaseURL); err == nil && u.Host != "" {
		host = strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		port = u.Port()
		if (u.Scheme == "https" && port == "443") ||
			(u.Scheme == "http" && port == "80") {
			port = ""
		}
		route = strings.ToLower(path.Clean("/" + strings.TrimPrefix(u.Path, "/")))
		route = strings.TrimSuffix(route, "/")
		for _, suffix := range []string{"/api/v1/koreader", "/api/v1"} {
			if strings.HasSuffix(route, suffix) {
				route = strings.TrimSuffix(route, suffix)
				break
			}
		}
	}
	return host + "\x1f" + port + "\x1f" + route + "\x1f" +
		strings.ToLower(m.RemoteUser)
}

// canonicalAudiobookshelfURL is an address the way startup stores it:
// parsed, with one trailing slash removed. Validation writes it and so
// does the restart check, so a saved address is compared against what
// the next start makes of it. Exactly one slash is removed because the
// peer identity has always been derived from the result of that, and a
// second pass over the same address would move every existing peer.
func canonicalAudiobookshelfURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSuffix(u.String(), "/")
}

// normalizedMirrorProtocol is a protocol name the way validation and the
// credential lookup both spell it: case and surrounding space are the
// operator's, not the protocol's. An entry that names none keeps the
// default.
func normalizedMirrorProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return ProtocolKosync
	}
	return protocol
}

// MirrorCredentialEnv names the environment variables a mirror's
// credential may come from. A list entry's name identifies its own pair,
// so removing one entry can never hand its credential to another — which
// is what a positional scheme like LISEUR_MIRROR_0_* would do.
func MirrorCredentialEnv(name string, legacy bool) (keyVar, passwordVar string) {
	if legacy {
		return "LISEUR_MIRROR_REMOTE_KEY", "LISEUR_MIRROR_REMOTE_PASSWORD"
	}
	upper := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return "LISEUR_MIRRORS_" + upper + "_REMOTE_KEY",
		"LISEUR_MIRRORS_" + upper + "_REMOTE_PASSWORD"
}

// ApplyMirrorCredentialEnv replaces an entry's credential with the one
// the environment holds for its protocol, and reports whether the
// environment owns it. Only the protocol's own variable is read: an MD5
// of a KOReader password is not an account password, and neither works
// as the other. A variable set to the empty string means the entry has
// no credential, so the entry is refused rather than silently started
// with the stale one the file holds.
func ApplyMirrorCredentialEnv(m *MirrorConfig, keyVar, passwordVar string) bool {
	if m.Protocol == ProtocolBookOrbit {
		if v, ok := os.LookupEnv(passwordVar); ok {
			m.RemoteKey, m.RemotePassword = "", v
			return true
		}
		return false
	}
	if v, ok := os.LookupEnv(keyVar); ok {
		m.RemoteKey, m.RemotePassword = v, ""
		return true
	}
	return false
}

// legacyMirrorEnvKeys are the LISEUR_MIRROR_* variables that set the
// single entry's settings. The two credential variables are deliberately
// absent: a credential in the environment is the ordinary way to deploy
// a mirror, and it does not stop an administrator editing the settings
// around it. Any other one does, because the environment would silently
// override what the form saved at the next start.
var legacyMirrorEnvKeys = []string{
	"LISEUR_MIRROR_ENABLED", "LISEUR_MIRROR_NAME", "LISEUR_MIRROR_BASE_URL",
	"LISEUR_MIRROR_ACCOUNT", "LISEUR_MIRROR_PROTOCOL",
	"LISEUR_MIRROR_REMOTE_USER", "LISEUR_MIRROR_DEVICE_ID",
	"LISEUR_MIRROR_PEER_PATH_PREFIX", "LISEUR_MIRROR_LOCAL_PATH_PREFIX",
	"LISEUR_MIRROR_RESOLVE_RETRY_INTERVAL",
}

// LegacyMirrorEnvironmentManaged reports whether the environment owns
// the single entry's non-secret settings, in which case the admin page
// shows them read-only.
func LegacyMirrorEnvironmentManaged() bool {
	for _, key := range legacyMirrorEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			return true
		}
	}
	return false
}

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
	c.Content.MaxAudiobookUploadBytes = 4 << 30
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
	c.Ops.TranslationMaxValueBytes = 8 << 10
	c.Ops.TranslationMaxPull = 500
	c.Ops.TranslationBudgetBytes = 32 << 20
	c.PairingCodeTTLMin = 15
	c.WebSessionTTLDays = 180
	c.Mirror.Name = "peer"
	c.Mirror.Protocol = ProtocolKosync
	c.Mirror.DeviceID = "liseur-sync"
	c.Mirror.PollInterval = Duration(5 * time.Minute)
	c.Mirror.ResolveRetryInterval = Duration(24 * time.Hour)
	c.Mirror.ActiveDays = 30
	c.Mirror.Timeout = Duration(20 * time.Second)
	c.Audiobookshelf.PollInterval = Duration(30 * time.Second)
	c.Audiobookshelf.Timeout = Duration(20 * time.Second)
	c.Audiobookshelf.SessionOverlap = Duration(10 * time.Minute)
	c.Audiobookshelf.ActiveDays = 30
	c.SpeechToText.Enabled = true
	c.SpeechToText.URL = defaultSTTURL
	c.SpeechToText.Model = defaultSTTModel
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
// LISEUR_MIRROR_PEER_PATH_PREFIX, LISEUR_MIRROR_LOCAL_PATH_PREFIX,
// LISEUR_MIRROR_RESOLVE_RETRY_INTERVAL,
// LISEUR_MIRRORS_<NAME>_REMOTE_KEY and
// LISEUR_MIRRORS_<NAME>_REMOTE_PASSWORD for each [[mirrors]] entry,
// LISEUR_ABS_ENABLED, LISEUR_ABS_BASE_URL, LISEUR_ABS_ACCOUNT,
// LISEUR_ABS_API_KEY, LISEUR_ABS_POLL_INTERVAL, LISEUR_ABS_TIMEOUT,
// LISEUR_ABS_SESSION_OVERLAP, LISEUR_ABS_ACTIVE_DAYS,
// LISEUR_STT_ENABLED, LISEUR_STT_URL, LISEUR_STT_API_KEY, LISEUR_STT_MODEL.
//
// The mirror's credential is an environment variable on purpose: it
// belongs beside the database URL in a deployment's .env rather than in
// a file somebody might commit.
func (c *Config) applyEnv() {
	setStr(&c.ListenAddr, "LISEUR_LISTEN_ADDR")
	setStr(&c.Database.Driver, "LISEUR_DATABASE_DRIVER")
	setStr(&c.Database.URL, "LISEUR_DATABASE_URL")
	setStr(&c.Content.CacheDir, "LISEUR_CACHE_DIR")
	setStr(&c.ReaderOrigin, "LISEUR_READER_ORIGIN")
	setBool(&c.InsecureHTTP, "LISEUR_INSECURE_HTTP")
	setList(&c.CORSAllowedOrigins, "LISEUR_CORS_ORIGINS")
	setList(&c.TrustedProxies, "LISEUR_TRUSTED_PROXIES")
	setList(&c.Content.FolderRoots, "LISEUR_FOLDER_ROOTS")

	c.ApplyMirrorEnvironment()

	c.ApplyAudiobookshelfEnvironment()

	c.ApplySpeechToTextEnvironment()
}

// The environment readers applyEnv is built from. The mirror's overlay is
// one function startup and the admin page both call, so it is defined where
// both can reach it.
func setStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func setBool(dst *bool, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setList(dst *[]string, key string) {
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

func setDuration(dst *Duration, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			*dst = Duration(d)
		}
	}
}

// ApplyMirrorEnvironment overlays the LISEUR_MIRROR_* variables on the
// mirror entries a configuration carries, exactly as startup does. The admin
// page builds its candidate from the file and calls this, so a set it is
// about to write is validated against what the next start will actually use.
//
// The protocol and the name are normalised first: the credential variable an
// entry reads depends on both, and validating them after the fact would leave
// "BookOrbit" reading the KOReader key. A variable set to the empty string
// still wins, so an operator who cleared one gets a refused entry rather than
// the stale credential the file holds.
func (c *Config) ApplyMirrorEnvironment() {
	setBool(&c.Mirror.Enabled, "LISEUR_MIRROR_ENABLED")
	setStr(&c.Mirror.Name, "LISEUR_MIRROR_NAME")
	setStr(&c.Mirror.BaseURL, "LISEUR_MIRROR_BASE_URL")
	setStr(&c.Mirror.Account, "LISEUR_MIRROR_ACCOUNT")
	setStr(&c.Mirror.Protocol, "LISEUR_MIRROR_PROTOCOL")
	setStr(&c.Mirror.RemoteUser, "LISEUR_MIRROR_REMOTE_USER")
	setStr(&c.Mirror.DeviceID, "LISEUR_MIRROR_DEVICE_ID")
	setStr(&c.Mirror.PeerPathPrefix, "LISEUR_MIRROR_PEER_PATH_PREFIX")
	setStr(&c.Mirror.LocalPathPrefix, "LISEUR_MIRROR_LOCAL_PATH_PREFIX")
	setDuration(&c.Mirror.ResolveRetryInterval, "LISEUR_MIRROR_RESOLVE_RETRY_INTERVAL")
	c.Mirror.Name = strings.TrimSpace(c.Mirror.Name)
	c.Mirror.Protocol = normalizedMirrorProtocol(c.Mirror.Protocol)
	legacyKey, legacyPassword := MirrorCredentialEnv("", true)
	ApplyMirrorCredentialEnv(&c.Mirror, legacyKey, legacyPassword)
	if LegacyMirrorEnvironmentManaged() {
		// The environment is the operator's answer for this entry, so
		// there is one whether or not the file has a table.
		c.mirrorDefined = true
	}
	for i := range c.Mirrors {
		c.Mirrors[i].Name = strings.TrimSpace(c.Mirrors[i].Name)
		c.Mirrors[i].Protocol = normalizedMirrorProtocol(c.Mirrors[i].Protocol)
		key, password := MirrorCredentialEnv(c.Mirrors[i].Name, false)
		ApplyMirrorCredentialEnv(&c.Mirrors[i], key, password)
	}
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
	if c.Content.MaxAudiobookUploadBytes < 1 {
		return fmt.Errorf("content.max_audiobook_upload_bytes must be >= 1")
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
	if c.Ops.TranslationMaxValueBytes < 1 {
		return fmt.Errorf("ops.translation_max_value_bytes must be >= 1")
	}
	if c.Ops.TranslationMaxPull < 1 {
		return fmt.Errorf("ops.translation_max_pull must be >= 1")
	}
	if c.Ops.TranslationBudgetBytes < 1 {
		return fmt.Errorf("ops.translation_budget_bytes must be >= 1")
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
	if err := c.validateAudiobookshelf(); err != nil {
		return err
	}
	return c.validateSpeechToText()
}

// validateMirror refuses a half-configured mirror rather than starting
// one that will fail on its first request. A mirror missing any of its
// required settings is a deployment mistake, not a degraded mode
// (ADR-0047).
//
// It validates every entry on its own and then the set: a name is what
// keys a mirror's bookkeeping, so two entries may not share one, and there
// is exactly one mirror per canonical remote account, whatever the local
// account or the protocol. Two routes into one account hold one remote
// reading position, so the second would write over the first's work with
// nothing to reconcile them.
func (c *Config) validateMirror() error {
	if err := c.validateMirrorEntry(&c.Mirror, "mirror", true); err != nil {
		return err
	}
	for i := range c.Mirrors {
		applyMirrorDefaults(&c.Mirrors[i])
		m := &c.Mirrors[i]
		path := "mirrors[" + m.Name + "]"
		if err := c.validateMirrorEntry(m, path, false); err != nil {
			return err
		}
	}
	return c.validateMirrorSet()
}

// validateMirrorEntry applies the rules for one entry with the prefix its
// errors are named by. The single [mirror] entry keeps the paths its
// messages have always had; a list entry is named by its own name so an
// operator knows which one to fix.
func (c *Config) validateMirrorEntry(m *MirrorConfig, path string, legacy bool) error {
	m.Name = strings.TrimSpace(m.Name)
	m.Protocol = normalizedMirrorProtocol(m.Protocol)
	m.BaseURL = strings.TrimSpace(m.BaseURL)
	m.Account = strings.TrimSpace(m.Account)
	m.RemoteUser = strings.TrimSpace(m.RemoteUser)
	m.DeviceID = strings.TrimSpace(m.DeviceID)
	m.PeerPathPrefix = strings.TrimSpace(m.PeerPathPrefix)
	m.LocalPathPrefix = strings.TrimSpace(m.LocalPathPrefix)
	if legacy && !m.Enabled {
		// Settings left behind by somebody turning the mirror off must
		// not keep the server from starting.
		return nil
	}
	if !legacy {
		if !mirrorListName.MatchString(m.Name) {
			return fmt.Errorf(
				"%s.name must be lower-case letters, digits and dashes, got %q",
				path, m.Name)
		}
		if m.BaseURL != "" {
			if err := c.cleanMirrorURL(m, path); err != nil {
				return err
			}
		}
		if !m.Enabled {
			return nil
		}
	}
	type setting struct{ name, value string }
	required := []setting{
		{path + ".name", m.Name},
		{path + ".base_url", m.BaseURL},
		{path + ".account", m.Account},
		{path + ".remote_user", m.RemoteUser},
		{path + ".device_id", m.DeviceID},
	}
	switch m.Protocol {
	case ProtocolKosync:
		required = append(required, setting{path + ".remote_key", m.RemoteKey})
	case ProtocolBookOrbit:
		required = append(required, setting{path + ".remote_password", m.RemotePassword})
	default:
		return fmt.Errorf("%s.protocol must be %q or %q, got %q",
			path, ProtocolKosync, ProtocolBookOrbit, m.Protocol)
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
		return fmt.Errorf("%s.peer_path_prefix and %s.local_path_prefix must be set together",
			path, path)
	}
	// The name is joined to a remote device id with a colon to make the
	// device a reader sees, so a name carrying one would make that id
	// ambiguous about where it split. A list entry's name is stricter
	// than this and has already been checked.
	if legacy && strings.ContainsAny(m.Name, ": \t") {
		return fmt.Errorf("%s.name must not contain a colon or whitespace, got %q", path, m.Name)
	}
	if err := c.cleanMirrorURL(m, path); err != nil {
		return err
	}
	if err := c.checkMirrorBounds(m, path); err != nil {
		return err
	}
	return nil
}

// cleanMirrorURL holds a peer address to being an address: a scheme, a
// host and a path, normalised once so every route built on it joins
// without a second thought. A list entry is cleaned even when it is
// disabled, because a credential or a query string in an address is
// wrong whether or not the entry is currently on.
func (c *Config) cleanMirrorURL(m *MirrorConfig, path string) error {
	if m.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s.base_url must be an absolute URL", path)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// The credential is sent on every request, so this is the same
		// boundary the inbound adapters hold.
		if !c.InsecureHTTP {
			return fmt.Errorf("%s.base_url must be https unless insecure_http is set", path)
		}
	}
	// An API root is a scheme, a host and a path. A password in the
	// userinfo would be logged with the URL at every startup, and a
	// query or a fragment would be carried onto every route built from
	// it, so none of the three is accepted rather than quietly dropped.
	if u.User != nil {
		return fmt.Errorf("%s.base_url must not contain a username or password", path)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s.base_url must not contain a query string or fragment", path)
	}
	// Recorded before the address is canonicalised, and only the first
	// time: validation cleans an entry more than once, and the legacy
	// form is the one an operator typed rather than the one that
	// follows from it.
	if m.legacyBaseURL == "" {
		m.legacyBaseURL = strings.TrimSuffix(u.String(), "/")
	}
	m.BaseURL = MirrorRoot(u.String())
	return nil
}

// checkMirrorBounds holds the polling of one enabled entry to what the
// peer can bear. A peer has no push, so the interval is the entire cost
// control.
func (c *Config) checkMirrorBounds(m *MirrorConfig, path string) error {
	if m.PollInterval.Duration() < 30*time.Second {
		return fmt.Errorf("%s.poll_interval must be at least 30s", path)
	}
	if m.ResolveRetryInterval.Duration() < time.Minute {
		return fmt.Errorf("%s.resolve_retry_interval must be at least 1m", path)
	}
	if m.ActiveDays < 1 {
		return fmt.Errorf("%s.active_days must be >= 1", path)
	}
	if m.Timeout.Duration() < time.Second {
		return fmt.Errorf("%s.timeout must be at least 1s", path)
	}
	return nil
}

// validateMirrorSet holds the entries to being distinct from each other.
// A name keys a mirror's cursors and prefixes the device a position from
// it is filed under, so two entries sharing one would share both. And one
// canonical remote account has exactly one mirror, whatever local account
// or protocol is behind it: the second route would share that one remote
// reading position — merging two local accounts' reading into each other's
// through it, or leaving two workers of one account racing over it — with
// nothing to reconcile them.
func (c *Config) validateMirrorSet() error {
	names := map[string]string{}
	addName := func(name, label string) error {
		key := strings.ToLower(name)
		if key == "" {
			return nil
		}
		if other, ok := names[key]; ok {
			return fmt.Errorf(
				"mirror names must be unique: %s and %s are both named %q",
				other, label, name)
		}
		names[key] = label
		return nil
	}
	if c.mirrorDefined {
		if err := addName(c.Mirror.Name, "the [mirror] entry"); err != nil {
			return err
		}
	}
	for _, m := range c.Mirrors {
		if err := addName(m.Name, "mirrors["+m.Name+"]"); err != nil {
			return err
		}
	}

	targets := map[string]string{}
	addTarget := func(m MirrorConfig, label string) error {
		if !m.Enabled {
			return nil
		}
		key := mirrorTargetKey(m)
		if other, ok := targets[key]; ok {
			return fmt.Errorf(
				"%s and %s point at the same account on the same peer (%s as %s); "+
					"two mirrors would share one remote reading position",
				other, label, m.BaseURL, m.RemoteUser)
		}
		targets[key] = label
		return nil
	}
	if c.mirrorDefined {
		if err := addTarget(c.Mirror, "the [mirror] entry"); err != nil {
			return err
		}
	}
	for _, m := range c.Mirrors {
		if err := addTarget(m, "mirrors["+m.Name+"]"); err != nil {
			return err
		}
	}
	return nil
}

// validateAudiobookshelf holds the integration to the mirror's rules:
// nothing half-configured starts, and the key never crosses in the clear
// or rides in a URL (ADR-0055).
func (c *Config) validateAudiobookshelf() error {
	a := &c.Audiobookshelf
	a.BaseURL = strings.TrimSpace(a.BaseURL)
	a.Account = strings.TrimSpace(a.Account)
	a.APIKey = strings.TrimSpace(a.APIKey)
	if a.BaseURL != "" {
		if a.Enabled {
			if err := c.validateAudiobookshelfAddress(a); err != nil {
				return err
			}
		} else {
			// A disabled entry is never spoken to, and may hold an
			// address for later: it is normalised but not refused, so
			// a file that already held one keeps starting.
			a.BaseURL = canonicalAudiobookshelfURL(a.BaseURL)
		}
	}
	if !a.Enabled {
		return nil
	}
	for _, r := range []struct{ name, value string }{
		{"audiobookshelf.base_url", a.BaseURL},
		{"audiobookshelf.account", a.Account},
		{"audiobookshelf.api_key", a.APIKey},
	} {
		if r.value == "" {
			return fmt.Errorf("%s is required when audiobookshelf is enabled", r.name)
		}
	}
	if a.PollInterval.Duration() < 30*time.Second {
		return fmt.Errorf("audiobookshelf.poll_interval must be at least 30s")
	}
	if a.ActiveDays < 1 {
		return fmt.Errorf("audiobookshelf.active_days must be positive")
	}
	if a.SessionOverlap.Duration() < time.Minute {
		return fmt.Errorf("audiobookshelf.session_overlap must be at least 1m")
	}
	if a.Timeout.Duration() < time.Second {
		return fmt.Errorf("audiobookshelf.timeout must be at least 1s")
	}
	return nil
}

const (
	defaultSTTURL   = "https://api.groq.com/openai/v1"
	defaultSTTModel = "whisper-large-v3-turbo"
)

func (c *Config) validateSpeechToText() error {
	s := &c.SpeechToText
	*s = normalizedSpeechToText(*s)
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("speech_to_text.url must be an absolute http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("speech_to_text.url must not contain credentials, query string or fragment")
	}
	return nil
}

// validateAudiobookshelfAddress applies the rules an address a file may
// hold must meet: an absolute address, and never a credential, query or
// fragment that would leak. The scheme rule belongs to an enabled peer,
// the only one ever spoken to. Validation applies it to a running
// integration and a save to the value it is about to write, so a disabled
// entry can hold an address for later that a start would never accept.
func (c *Config) validateAudiobookshelfAddress(a *AudiobookshelfConfig) error {
	u, err := url.Parse(a.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("audiobookshelf.base_url must be an absolute URL")
	}
	if u.User != nil {
		return fmt.Errorf("audiobookshelf.base_url must not contain a username or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("audiobookshelf.base_url must not contain a query string or fragment")
	}
	if a.Enabled {
		switch u.Scheme {
		case "https":
		case "http":
			if !c.InsecureHTTP {
				return fmt.Errorf("audiobookshelf.base_url must be https unless insecure_http is set")
			}
		default:
			return fmt.Errorf("audiobookshelf.base_url must be http or https")
		}
	}
	a.BaseURL = canonicalAudiobookshelfURL(a.BaseURL)
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
