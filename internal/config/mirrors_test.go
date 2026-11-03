package config

import (
	"strings"
	"testing"
	"time"
)

// mirrorsFixture is the list form of workingMirror: two entries, each
// complete, so each test can break exactly one thing.
func mirrorsFixture(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	cfg.Mirrors = []MirrorConfig{
		{
			Enabled: true, Protocol: ProtocolKosync, Name: "kobo",
			BaseURL: "https://kobo.example.com/sync", Account: "alice",
			RemoteUser: "reader",
			RemoteKey:  "0123456789abcdef0123456789abcdef",
		},
		{
			Enabled: true, Protocol: ProtocolBookOrbit, Name: "orbit",
			BaseURL: "https://orbit.example.com/api/v1", Account: "bob",
			RemoteUser: "reader2", RemotePassword: "hunter2",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the baseline list is not valid: %v", err)
	}
	return cfg
}

// TestASingleEntryUpgradesWithoutMoving. A deployment that already
// mirrors keeps the entry it has: the same name, the same settings, and
// the same bookkeeping in the database.
func TestASingleEntryUpgradesWithoutMoving(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[mirror]
enabled = true
name = "orbit"
base_url = "https://orbit.example.com/api/v1/koreader"
account = "you"
remote_user = "you"
remote_key = "0123456789abcdef0123456789abcdef"
device_id = "liseur-sync"
`))
	if err != nil {
		t.Fatal(err)
	}
	entries := cfg.EnabledMirrors()
	if len(entries) != 1 {
		t.Fatalf("enabled entries: %+v", entries)
	}
	if entries[0].Name != "orbit" || entries[0].BaseURL != "https://orbit.example.com/api/v1/koreader" {
		t.Fatalf("the single entry changed on upgrade: %+v", entries[0])
	}
	if !cfg.MirrorTableDefined() {
		t.Fatal("a file with a [mirror] table reported none")
	}
}

// TestTheDefaultEntryIsNotAPeerNobodyConfigured. The identity of an
// entry is its table, not the struct field it decodes into: without that,
// every deployment has a peer called "peer" and no list entry may use that
// name.
func TestTheDefaultEntryIsNotAPeerNobodyConfigured(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = "peer"
base_url = "https://orbit.example.com/sync"
account = "you"
remote_user = "you"
remote_key = "0123456789abcdef0123456789abcdef"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MirrorTableDefined() {
		t.Fatal("a file with no [mirror] table reported one")
	}
	if got := len(cfg.EnabledMirrors()); got != 1 {
		t.Fatalf("enabled entries: %d", got)
	}
}

// TestAListEntryTakesTheDefaultsTheTableDocuments. A field left out of a
// [[mirrors]] entry means what it means in the single table, so an
// operator writing one entry by hand does not have to spell out five
// settings.
func TestAListEntryTakesTheDefaultsTheTableDocuments(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "you"
remote_user = "you"
remote_key = "0123456789abcdef0123456789abcdef"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Mirrors) != 1 {
		t.Fatalf("entries: %+v", cfg.Mirrors)
	}
	m := cfg.Mirrors[0]
	if m.Protocol != ProtocolKosync || m.DeviceID != "liseur-sync" {
		t.Fatalf("defaults: %+v", m)
	}
	if m.PollInterval.Duration() != 5*time.Minute ||
		m.ResolveRetryInterval.Duration() != 24*time.Hour ||
		m.ActiveDays != 30 || m.Timeout.Duration() != 20*time.Second {
		t.Fatalf("defaults: %+v", m)
	}
}

// TestAMirrorNameIsWhatItCanBeAnEnvironmentVariable is the name rule: a
// name becomes an environment variable name and a URL query value, so it
// is restricted to what survives both unchanged.
func TestAMirrorNameIsWhatItCanBeAnEnvironmentVariable(t *testing.T) {
	for _, name := range []string{"Kobo", "ko bo", "kobo:", "-kobo", "kobo_extra",
		"a-name-that-is-far-too-long-to-be-typed-out-in-full"} {
		cfg := mirrorsFixture(t)
		cfg.Mirrors[0].Name = name
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted the mirror name %q", name)
		}
	}
}

// TestTwoMirrorsCannotShareAName. A name keys a mirror's cursors and
// prefixes the device a position from it is filed under, so two entries
// sharing one would share both.
func TestTwoMirrorsCannotShareAName(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirrors[1].Name = cfg.Mirrors[0].Name
	err := cfg.Validate()
	if err == nil {
		t.Fatal("two list entries shared a name")
	}
	if !strings.Contains(err.Error(), "unique") {
		t.Fatalf("the error does not say what is wrong: %v", err)
	}

	// And the single entry counts too, even switched off: turning it back
	// on must not silently take a name that is in use.
	cfg = mirrorsFixture(t)
	cfg.Mirror = workingMirror(t).Mirror
	cfg.Mirror.Enabled = false
	cfg.Mirror.Name = "kobo"
	cfg.SetMirrorTableDefined(true)
	if err := cfg.Validate(); err == nil {
		t.Fatal("a list entry took the name of the single entry")
	}
}

// TestTwoMirrorsCannotReachOnePeerAccount. Two local accounts mirroring
// through one remote account would merge their reading into each other's,
// which is a cross-user leak between two readers of this server.
func TestTwoMirrorsCannotReachOnePeerAccount(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"different accounts", func(c *Config) { c.Mirrors[1].Account = "carol" }},
		{"different protocols", func(c *Config) {
			c.Mirrors[1].Protocol = ProtocolKosync
			c.Mirrors[1].RemoteKey, c.Mirrors[1].RemotePassword = "k", ""
		}},
		{"a different port is a different peer", func(c *Config) {
			c.Mirrors[1].BaseURL = "https://kobo.example.com:8443/sync"
			c.Mirrors[1].RemoteUser = "reader"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mirrorsFixture(t)
			cfg.Mirrors[1].BaseURL = cfg.Mirrors[0].BaseURL
			cfg.Mirrors[1].RemoteUser = cfg.Mirrors[0].RemoteUser
			tc.edit(&cfg)
			if strings.Contains(tc.name, "different port") {
				// The same peer reached under another port is a
				// different library, so it is allowed.
				if err := cfg.Validate(); err != nil {
					t.Fatalf("a distinct peer was refused: %v", err)
				}
				return
			}
			err := cfg.Validate()
			if err == nil {
				t.Fatal("two enabled mirrors reached one peer account")
			}
			if !strings.Contains(err.Error(), "same account on the same peer") {
				t.Fatalf("the error does not say what is wrong: %v", err)
			}
		})
	}
}

// TestTheDuplicateTargetRuleCoversTheSingleEntry carries the same rule to
// the table an upgrade brought along.
func TestTheDuplicateTargetRuleCoversTheSingleEntry(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirror = cfg.Mirrors[0]
	cfg.Mirror.Name = "peer"
	cfg.SetMirrorTableDefined(true)
	cfg.Mirrors[0].Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a disabled list entry was counted: %v", err)
	}
	cfg.Mirrors[0].Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("the single entry and a list entry reached one peer account")
	}
}

// TestADisabledListEntryIsStillCleaned. An address carrying a credential
// or a query string is wrong whether or not the entry is switched on: it
// is printed in the log and in the page, and a query would be carried onto
// every route built from it.
func TestADisabledListEntryIsStillCleaned(t *testing.T) {
	for _, bad := range []string{
		"https://sam:hunter2@kobo.example.com/sync",
		"https://sam@kobo.example.com/sync",
		"https://kobo.example.com/sync?token=abc",
		"https://kobo.example.com/sync#top",
	} {
		cfg := mirrorsFixture(t)
		cfg.Mirrors[0].Enabled = false
		cfg.Mirrors[0].BaseURL = bad
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("a disabled entry kept %q", bad)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("the error quoted the password back: %v", err)
		}
	}
}

// TestEachListEntryReadsItsOwnCredential. The variable is named after the
// entry, so removing one entry can never hand its credential to another.
func TestEachListEntryReadsItsOwnCredential(t *testing.T) {
	t.Setenv("LISEUR_MIRRORS_KOBO_REMOTE_KEY", "kobo-key")
	t.Setenv("LISEUR_MIRRORS_ORBIT_REMOTE_PASSWORD", "orbit-password")
	cfg, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader"

[[mirrors]]
enabled = true
protocol = "bookorbit"
name = "orbit"
base_url = "https://orbit.example.com/api/v1"
account = "bob"
remote_user = "reader2"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mirrors[0].RemoteKey != "kobo-key" || cfg.Mirrors[0].RemotePassword != "" {
		t.Fatalf("kobo credential: %+v", cfg.Mirrors[0])
	}
	if cfg.Mirrors[1].RemotePassword != "orbit-password" || cfg.Mirrors[1].RemoteKey != "" {
		t.Fatalf("orbit credential: %+v", cfg.Mirrors[1])
	}
}

// TestAnEmptyEnvironmentCredentialIsRefused. A variable set to nothing is
// somebody having unset it badly; carrying on with the stale credential in
// the file would hide that.
func TestAnEmptyEnvironmentCredentialIsRefused(t *testing.T) {
	t.Setenv("LISEUR_MIRRORS_KOBO_REMOTE_KEY", "")
	_, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader"
remote_key = "stale-file-key"
`))
	if err == nil {
		t.Fatal("an empty environment credential was accepted")
	}
	if !strings.Contains(err.Error(), "mirrors[kobo].remote_key") {
		t.Fatalf("the error does not name the entry: %v", err)
	}
}

// TestANonSecretMirrorVariableMakesTheSingleEntryReadOnly. A form that
// saves values the environment overrides at the next start is a form that
// lies, so the page has to know.
func TestANonSecretMirrorVariableMakesTheSingleEntryReadOnly(t *testing.T) {
	if LegacyMirrorEnvironmentManaged() {
		t.Fatal("the mirror is environment-managed before any variable is set")
	}
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "only-a-credential")
	if LegacyMirrorEnvironmentManaged() {
		t.Fatal("a credential alone made the mirror read-only")
	}
	t.Setenv("LISEUR_MIRROR_BASE_URL", "https://orbit.example.com/sync")
	if !LegacyMirrorEnvironmentManaged() {
		t.Fatal("a non-secret variable did not make the mirror read-only")
	}
}

// TestTheEnvironmentSuppliedEntryCounts. A deployment that configures the
// single entry entirely from the environment has one, and its name is as
// taken as a file's.
func TestTheEnvironmentSuppliedEntryCounts(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_ENABLED", "true")
	t.Setenv("LISEUR_MIRROR_BASE_URL", "https://orbit.example.com/sync")
	t.Setenv("LISEUR_MIRROR_ACCOUNT", "alice")
	t.Setenv("LISEUR_MIRROR_REMOTE_USER", "reader")
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("LISEUR_MIRROR_NAME", "peer")
	cfg := Default()
	cfg.applyEnv()
	if !cfg.MirrorTableDefined() {
		t.Fatal("the environment did not supply an entry")
	}
	entry := MirrorConfig{
		Enabled: true, Protocol: ProtocolKosync, Name: "peer",
		BaseURL: "https://kobo.example.com/sync", Account: "bob",
		RemoteUser: "reader2", RemoteKey: "0123456789abcdef0123456789abcdef",
	}
	cfg.Mirrors = []MirrorConfig{entry}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a list entry took the name of the environment's entry")
	}
}

// TestATypoInsideTheMirrorsListIsAStartupError. The loader refuses keys it
// does not know because a misplaced setting silently does nothing; a typo
// inside a list entry is the same mistake in a new place.
func TestATypoInsideTheMirrorsListIsAStartupError(t *testing.T) {
	_, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = "kobo"
base_urll = "https://kobo.example.com/sync"
`))
	if err == nil {
		t.Fatal("a misspelt key inside [[mirrors]] was accepted")
	}
	if !strings.Contains(err.Error(), "mirrors.base_urll") {
		t.Fatalf("the error does not name the key: %v", err)
	}
}

// TestOnlyEnabledMirrorsStart. A disabled entry is a record of a peer
// somebody switched off, not a worker.
func TestOnlyEnabledMirrorsStart(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirrors[0].Enabled = false
	entries := cfg.EnabledMirrors()
	if len(entries) != 1 || entries[0].Name != "orbit" {
		t.Fatalf("enabled entries: %+v", entries)
	}
}

// TestTheSingleEntryStartsFirst keeps the order stable, so the log of a
// deployment that added a list beside its table reads the way its file
// does.
func TestTheSingleEntryStartsFirst(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirror = workingMirror(t).Mirror
	cfg.Mirror.Name = "peer"
	cfg.SetMirrorTableDefined(true)
	entries := cfg.EnabledMirrors()
	if len(entries) != 3 || entries[0].Name != "peer" ||
		entries[1].Name != "kobo" || entries[2].Name != "orbit" {
		t.Fatalf("enabled entries: %+v", entries)
	}
}

// TestAListEntryErrorNamesItsOwnEntry. One broken entry among several has
// to say which one, or an operator is left guessing.
func TestAListEntryErrorNamesItsOwnEntry(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirrors[1].RemotePassword = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("a bookorbit entry with no password was accepted")
	}
	if !strings.Contains(err.Error(), "mirrors[orbit].remote_password") {
		t.Fatalf("the error does not name the entry and the setting: %v", err)
	}
	// The single entry's messages are unchanged.
	cfg = workingMirror(t)
	cfg.Mirror.RemoteKey = ""
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "mirror.remote_key is required") {
		t.Fatalf("the single entry's message changed: %v", err)
	}
}

// TestTwoSpellingsOfOneRootAreOnePeerAccount. The address a client builds
// its routes from and the address the duplicate check compares have to be
// one canonical root: "/sync//" trimmed once is "/sync/", which still looks
// like a different peer from "/sync" while both clients end up at "/sync".
// Two local accounts assigned to those two entries would then share one
// remote account's reading.
func TestTwoSpellingsOfOneRootAreOnePeerAccount(t *testing.T) {
	cfg := mirrorsFixture(t)
	cfg.Mirror = cfg.Mirrors[0]
	cfg.Mirror.Name = "peer"
	cfg.Mirror.BaseURL = "https://kobo.example.com/sync//"
	cfg.SetMirrorTableDefined(true)
	cfg.Mirrors[0].BaseURL = "https://kobo.example.com/sync"
	cfg.Mirrors[0].RemoteUser = cfg.Mirror.RemoteUser

	err := cfg.Validate()
	if err == nil {
		t.Fatal("two spellings of one root passed as two peers")
	}
	if !strings.Contains(err.Error(), "same account on the same peer") {
		t.Fatalf("the error does not say what is wrong: %v", err)
	}
	if cfg.Mirror.BaseURL != "https://kobo.example.com/sync" ||
		cfg.Mirrors[0].BaseURL != cfg.Mirror.BaseURL {
		t.Fatalf("the two spellings were not held to one root: %q and %q",
			cfg.Mirror.BaseURL, cfg.Mirrors[0].BaseURL)
	}
	if root := MirrorRoot(cfg.Mirror.BaseURL); root != MirrorRoot(MirrorRoot(cfg.Mirror.BaseURL)) {
		t.Fatalf("normalisation is not idempotent: %q", root)
	}
}

// TestACredentialVariableIsChosenAfterNormalisingTheEntry. The variable an
// entry reads depends on its protocol and its name, and both arrive from a
// file an operator wrote: "BookOrbit" is the protocol, " kobo " is the name.
// Looking the variable up before normalising reads the wrong one and leaves
// the stale credential the file holds.
func TestACredentialVariableIsChosenAfterNormalisingTheEntry(t *testing.T) {
	t.Run("protocol case", func(t *testing.T) {
		t.Setenv("LISEUR_MIRROR_REMOTE_PASSWORD", "env-password")
		cfg, err := Load(writeConfig(t, `
[mirror]
enabled = true
protocol = "BookOrbit"
name = "peer"
base_url = "https://orbit.example.com/api/v1"
account = "you"
remote_user = "you"
remote_key = "stale-file-key"
`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Mirror.RemotePassword != "env-password" || cfg.Mirror.RemoteKey != "" {
			t.Fatalf("the credential variable was read before the protocol was normalised: %+v",
				cfg.Mirror)
		}
	})

	t.Run("list name whitespace", func(t *testing.T) {
		t.Setenv("LISEUR_MIRRORS_KOBO_REMOTE_KEY", "kobo-key")
		cfg, err := Load(writeConfig(t, `
[[mirrors]]
enabled = true
name = " kobo "
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader"
`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Mirrors[0].Name != "kobo" || cfg.Mirrors[0].RemoteKey != "kobo-key" {
			t.Fatalf("the entry's variable was looked up under its untrimmed name: %+v",
				cfg.Mirrors[0])
		}
	})

	t.Run("empty invalidates the stale file value", func(t *testing.T) {
		t.Setenv("LISEUR_MIRROR_REMOTE_PASSWORD", "")
		_, err := Load(writeConfig(t, `
[mirror]
enabled = true
protocol = "BookOrbit"
name = "peer"
base_url = "https://orbit.example.com/api/v1"
account = "you"
remote_user = "you"
remote_password = "stale-file-password"
`))
		if err == nil {
			t.Fatal("the stale file password was kept after the variable was cleared")
		}
		if !strings.Contains(err.Error(), "mirror.remote_password") {
			t.Fatalf("the error does not name the setting: %v", err)
		}
	})
}
