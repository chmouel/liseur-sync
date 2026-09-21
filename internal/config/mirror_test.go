package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// workingMirror is a mirror configured the way a deployment would
// configure one, so each test below can break exactly one thing.
func workingMirror(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	cfg.Mirror.Enabled = true
	cfg.Mirror.BaseURL = "https://orbit.example.com/api/v1/koreader"
	cfg.Mirror.Account = "reader"
	cfg.Mirror.RemoteUser = "reader"
	cfg.Mirror.RemoteKey = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the baseline mirror config is not valid: %v", err)
	}
	return cfg
}

// TestMirrorIsOffAndHarmlessByDefault. Dialling out is not something a
// server should start doing because it was upgraded.
func TestMirrorIsOffAndHarmlessByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Mirror.Enabled {
		t.Fatal("the mirror is enabled by default")
	}
	if cfg.Mirror.BaseURL != "" || cfg.Mirror.RemoteUser != "" || cfg.Mirror.RemoteKey != "" {
		t.Fatalf("the mirror has a peer by default: %+v", cfg.Mirror)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a disabled, unconfigured mirror was rejected: %v", err)
	}
	if got := cfg.Mirror.PollInterval.Duration(); got != 5*time.Minute {
		t.Fatalf("default poll interval: %s", got)
	}
	if cfg.Mirror.ActiveDays != 30 {
		t.Fatalf("default active days: %d", cfg.Mirror.ActiveDays)
	}
	if got := cfg.Mirror.Timeout.Duration(); got != 20*time.Second {
		t.Fatalf("default timeout: %s", got)
	}
	if cfg.Mirror.DeviceID != "liseur-sync" {
		t.Fatalf("default device id: %q", cfg.Mirror.DeviceID)
	}
}

// TestADisabledMirrorIgnoresItsOwnNonsense. Settings left behind by
// somebody turning the mirror off must not keep the server from
// starting; only an enabled mirror is held to its requirements.
func TestADisabledMirrorIgnoresItsOwnNonsense(t *testing.T) {
	cfg := Default()
	cfg.Mirror.Enabled = false
	cfg.Mirror.BaseURL = "not a url at all"
	cfg.Mirror.PollInterval = Duration(time.Millisecond)
	cfg.Mirror.ActiveDays = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a disabled mirror was validated anyway: %v", err)
	}
}

// TestAHalfConfiguredMirrorRefusesToStart is the point of validation
// here: a mirror missing a setting would fail on its first request, in
// a background goroutine, where nobody is looking. It fails at startup
// instead, naming the setting.
func TestAHalfConfiguredMirrorRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		missing string
		break_  func(*MirrorConfig)
	}{
		{"mirror.base_url", func(m *MirrorConfig) { m.BaseURL = "" }},
		{"mirror.account", func(m *MirrorConfig) { m.Account = "" }},
		{"mirror.remote_user", func(m *MirrorConfig) { m.RemoteUser = "" }},
		{"mirror.remote_key", func(m *MirrorConfig) { m.RemoteKey = "" }},
		{"mirror.device_id", func(m *MirrorConfig) { m.DeviceID = "" }},
	} {
		t.Run(tc.missing, func(t *testing.T) {
			cfg := workingMirror(t)
			tc.break_(&cfg.Mirror)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("a mirror with no %s was accepted", tc.missing)
			}
			if !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("error does not name the missing setting: %v", err)
			}
		})
	}
}

// TestASettingOfOnlyWhitespaceIsNotASetting. A value a deployment left
// as an empty quoted string with a stray space is missing, not present.
func TestASettingOfOnlyWhitespaceIsNotASetting(t *testing.T) {
	cfg := workingMirror(t)
	cfg.Mirror.RemoteUser = "   "
	if err := cfg.Validate(); err == nil {
		t.Fatal("a whitespace-only remote_user was accepted")
	}
}

// TestTheCredentialWillNotTravelInTheClear holds the same boundary the
// inbound adapters hold: the peer credential is sent on every request,
// so the peer is HTTPS unless the operator has said this deployment is
// on a network where that is not required.
func TestTheCredentialWillNotTravelInTheClear(t *testing.T) {
	cfg := workingMirror(t)
	cfg.Mirror.BaseURL = "http://orbit.example.com/api/v1/koreader"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("a plaintext peer was accepted with insecure_http unset")
	}
	if !strings.Contains(err.Error(), "insecure_http") {
		t.Fatalf("the error does not say how to allow it: %v", err)
	}

	cfg.InsecureHTTP = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a plaintext peer was refused with insecure_http set: %v", err)
	}
}

func TestAPeerURLMustBeAbsoluteAndHTTP(t *testing.T) {
	for _, bad := range []string{
		"orbit.example.com/api/v1/koreader",
		"/api/v1/koreader",
		"ftp://orbit.example.com/koreader",
		"file:///etc/passwd",
	} {
		cfg := workingMirror(t)
		cfg.Mirror.BaseURL = bad
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted %q as a peer URL", bad)
		}
	}
}

// TestThePeerURLIsNormalisedOnce so the client can join paths onto it
// without each call worrying about a trailing slash.
func TestThePeerURLIsNormalisedOnce(t *testing.T) {
	cfg := workingMirror(t)
	cfg.Mirror.BaseURL = "  https://orbit.example.com/api/v1/koreader/  "
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Mirror.BaseURL; got != "https://orbit.example.com/api/v1/koreader" {
		t.Fatalf("peer URL after validation: %q", got)
	}
}

// TestPollingIsBounded. The peer has no push, so the interval is the
// entire cost control; a mirror asking every second is a mirror
// hammering somebody else's server.
func TestPollingIsBounded(t *testing.T) {
	cfg := workingMirror(t)
	cfg.Mirror.PollInterval = Duration(10 * time.Second)
	if err := cfg.Validate(); err == nil {
		t.Fatal("a ten-second poll interval was accepted")
	}

	cfg = workingMirror(t)
	cfg.Mirror.ActiveDays = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("an empty active window was accepted")
	}

	cfg = workingMirror(t)
	cfg.Mirror.Timeout = Duration(0)
	if err := cfg.Validate(); err == nil {
		t.Fatal("a mirror with no request timeout was accepted")
	}
}

// TestDurationsAreWrittenTheWayPeopleWriteThem. The TOML decoder has no
// duration type; this is the whole reason Duration exists.
func TestDurationsAreWrittenTheWayPeopleWriteThem(t *testing.T) {
	body := `
[mirror]
enabled = true
base_url = "https://orbit.example.com/api/v1/koreader"
account = "reader"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"
poll_interval = "90m"
timeout = "3s"
active_days = 7
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Mirror.PollInterval.Duration(); got != 90*time.Minute {
		t.Fatalf("poll_interval: %s", got)
	}
	if got := cfg.Mirror.Timeout.Duration(); got != 3*time.Second {
		t.Fatalf("timeout: %s", got)
	}
	if cfg.Mirror.ActiveDays != 7 {
		t.Fatalf("active_days: %d", cfg.Mirror.ActiveDays)
	}
}

func TestAnUnreadableDurationIsAStartupError(t *testing.T) {
	body := `
[mirror]
poll_interval = "every five minutes"
`
	if _, err := Load(writeConfig(t, body)); err == nil {
		t.Fatal("a duration nobody can parse was accepted")
	}
}

// TestTheCredentialComesFromTheEnvironment. It belongs beside the
// database URL in a deployment's .env, not in a file somebody might
// commit, so the environment has to be able to supply all of it.
func TestTheCredentialComesFromTheEnvironment(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_ENABLED", "true")
	t.Setenv("LISEUR_MIRROR_BASE_URL", "https://peer.example.com/api/v1/koreader")
	t.Setenv("LISEUR_MIRROR_ACCOUNT", "reader")
	t.Setenv("LISEUR_MIRROR_REMOTE_USER", "remote-reader")
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "fedcba9876543210fedcba9876543210")
	t.Setenv("LISEUR_MIRROR_DEVICE_ID", "books-chmouel-com")
	t.Setenv("LISEUR_MIRROR_NAME", "orbit")

	cfg := Default()
	cfg.applyEnv()
	want := MirrorConfig{
		Enabled:    true,
		Name:       "orbit",
		BaseURL:    "https://peer.example.com/api/v1/koreader",
		Account:    "reader",
		RemoteUser: "remote-reader",
		RemoteKey:  "fedcba9876543210fedcba9876543210",
		DeviceID:   "books-chmouel-com",
	}
	got := cfg.Mirror
	got.PollInterval, got.ActiveDays, got.Timeout = 0, 0, 0
	if got != want {
		t.Fatalf("mirror from environment:\n got %+v\nwant %+v", got, want)
	}
}

// TestTheEnvironmentCanTurnTheMirrorOffAgain. An operator who set the
// peer in a file must be able to stop it from the environment without
// editing that file.
func TestTheEnvironmentCanTurnTheMirrorOffAgain(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_ENABLED", "false")
	cfg := Default()
	cfg.Mirror.Enabled = true
	cfg.applyEnv()
	if cfg.Mirror.Enabled {
		t.Fatal("LISEUR_MIRROR_ENABLED=false did not disable the mirror")
	}
}

// TestTheShippedExampleDocumentsTheMirror. The example is where an
// operator finds out this exists at all, and the settings it names have
// to be the settings the server reads.
func TestTheShippedExampleDocumentsTheMirror(t *testing.T) {
	body, err := os.ReadFile("../../liseur-sync.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "[mirror]") {
		t.Fatal("the example config does not document the mirror")
	}
	text = strings.Replace(text, `poll_interval = "5m"`, `poll_interval = "11m"`, 1)
	text = strings.Replace(text, "active_days = 30", "active_days = 3", 1)

	cfg, err := Load(writeConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Mirror.PollInterval.Duration(); got != 11*time.Minute {
		t.Fatalf("poll_interval did not reach the mirror: %s", got)
	}
	if cfg.Mirror.ActiveDays != 3 {
		t.Fatalf("active_days did not reach the mirror: %d", cfg.Mirror.ActiveDays)
	}
	if cfg.Mirror.Enabled {
		t.Fatal("the shipped example turns the mirror on")
	}
	if cfg.Mirror.RemoteKey != "" {
		t.Fatal("the shipped example carries a credential")
	}
}
