package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// audiobookshelfEnvKeys are the LISEUR_ABS_* variables that set a
// setting the admin page edits. A credential variable is deliberately
// absent: the environment is the ordinary place to keep an API key, and
// it does not stop an administrator editing the settings around it.
var audiobookshelfEnvKeys = []string{
	"LISEUR_ABS_ENABLED", "LISEUR_ABS_BASE_URL", "LISEUR_ABS_ACCOUNT",
}

// AudiobookshelfEnvironmentManaged names the environment variables that
// own a setting the admin page exposes, so the page can say so and
// refuse to save values the next start would override.
func AudiobookshelfEnvironmentManaged() []string {
	var names []string
	for _, key := range audiobookshelfEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			names = append(names, key)
		}
	}
	return names
}

// ReadAudiobookshelf reads the [audiobookshelf] entry out of the file at
// path, without applying environment overrides. An omitted setting comes
// back with the default a start would give it, so what the page shows is
// what the server would do and what it writes back leaves nothing
// implied.
func ReadAudiobookshelf(path string) (AudiobookshelfConfig, error) {
	out := Default().Audiobookshelf
	if strings.TrimSpace(path) == "" {
		return out, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read config: %w", err)
	}
	c := Default()
	if _, err := toml.Decode(string(body), &c); err != nil {
		return out, safeConfigParseError("the configuration file does not parse", err)
	}
	return c.Audiobookshelf, nil
}

// safeConfigParseError says where a file stopped parsing without repeating
// the decoder's message: TOML diagnostics quote the source literal, and that
// literal can be a value the page must never render or audit.
func safeConfigParseError(summary string, err error) error {
	var parseErr toml.ParseError
	if errors.As(err, &parseErr) && parseErr.Position.Line > 0 {
		return fmt.Errorf("%s at line %d, column %d",
			summary, parseErr.Position.Line, parseErr.Position.Col)
	}
	return errors.New(summary)
}

// ApplyAudiobookshelfEnvironment overlays the LISEUR_ABS_* variables on
// the entry exactly as startup does. The admin page builds its candidate
// from the file and calls this, so what it is about to write is held
// against what the next start will actually use.
func (c *Config) ApplyAudiobookshelfEnvironment() {
	setBool(&c.Audiobookshelf.Enabled, "LISEUR_ABS_ENABLED")
	setStr(&c.Audiobookshelf.BaseURL, "LISEUR_ABS_BASE_URL")
	setStr(&c.Audiobookshelf.Account, "LISEUR_ABS_ACCOUNT")
	setStr(&c.Audiobookshelf.APIKey, "LISEUR_ABS_API_KEY")
	setDuration(&c.Audiobookshelf.PollInterval, "LISEUR_ABS_POLL_INTERVAL")
	setDuration(&c.Audiobookshelf.Timeout, "LISEUR_ABS_TIMEOUT")
	setDuration(&c.Audiobookshelf.SessionOverlap, "LISEUR_ABS_SESSION_OVERLAP")
	setStr(&c.Audiobookshelf.STTURL, "LISEUR_ABS_STT_URL")
	setStr(&c.Audiobookshelf.STTAPIKey, "LISEUR_ABS_STT_API_KEY")
	setStr(&c.Audiobookshelf.STTModel, "LISEUR_ABS_STT_MODEL")
	setStr(&c.Audiobookshelf.STTURL, "LISEUR_STT_URL")
	setStr(&c.Audiobookshelf.STTAPIKey, "LISEUR_STT_API_KEY")
	setStr(&c.Audiobookshelf.STTModel, "LISEUR_STT_MODEL")
	if v, ok := os.LookupEnv("LISEUR_ABS_ACTIVE_DAYS"); ok {
		if days, err := strconv.Atoi(v); err == nil {
			c.Audiobookshelf.ActiveDays = days
		}
	}
}

// EffectiveAudiobookshelf is the entry a start would read: the saved one
// with the environment overlaid and normalised the way validation leaves
// it. Comparing a saved entry to the running one only means something
// after this, because a restart that changes nothing must not look like
// one that does.
func EffectiveAudiobookshelf(saved AudiobookshelfConfig) AudiobookshelfConfig {
	c := Default()
	c.Audiobookshelf = saved
	c.ApplyAudiobookshelfEnvironment()
	return normalizedAudiobookshelf(c.Audiobookshelf)
}

// normalizedAudiobookshelf fills and trims an entry the way validation
// does, without applying the environment. The addresses go through the
// same canonicalisation startup applies, case and all, so comparing a
// saved entry to the running one cannot report a restart over a spelling
// difference the next start would erase.
func normalizedAudiobookshelf(a AudiobookshelfConfig) AudiobookshelfConfig {
	defaults := Default().Audiobookshelf
	a.BaseURL = canonicalAudiobookshelfURL(a.BaseURL)
	a.Account = strings.TrimSpace(a.Account)
	a.APIKey = strings.TrimSpace(a.APIKey)
	a.STTURL = canonicalAudiobookshelfURL(a.STTURL)
	a.STTAPIKey = strings.TrimSpace(a.STTAPIKey)
	a.STTModel = strings.TrimSpace(a.STTModel)
	if a.STTURL == "" {
		a.STTURL = defaults.STTURL
	}
	if a.STTModel == "" {
		a.STTModel = defaults.STTModel
	}
	return a
}

// AudiobookshelfCandidate is the entry a restart would use: saved with
// the environment overlaid, held to the audiobookshelf rules. Only those
// rules run — the rest of the configuration is read for the one thing
// they consult, the insecure_http scope a plain-http peer needs — so a
// file that is otherwise broken can still save this section.
func AudiobookshelfCandidate(path string, saved AudiobookshelfConfig) (AudiobookshelfConfig, error) {
	c := Default()
	if strings.TrimSpace(path) != "" {
		body, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return saved, fmt.Errorf("read config: %w", err)
		}
		if err == nil {
			if _, err := toml.Decode(string(body), &c); err != nil {
				return saved, safeConfigParseError("the configuration file does not parse", err)
			}
		}
	}
	c.Audiobookshelf = saved
	c.applyEnv()
	if err := c.validateSavedAudiobookshelf(); err != nil {
		return c.Audiobookshelf, err
	}
	return c.Audiobookshelf, nil
}

// validateSavedAudiobookshelf holds an entry a save is about to write to
// the rules a start applies, and to the address rules a start skips for a
// disabled entry: a save never writes an address carrying a credential,
// because the page would have to strip it to show what it wrote.
func (c *Config) validateSavedAudiobookshelf() error {
	if err := c.validateAudiobookshelf(); err != nil {
		return err
	}
	if a := &c.Audiobookshelf; !a.Enabled && a.BaseURL != "" {
		return c.validateAudiobookshelfAddress(a)
	}
	return nil
}

// UpdateAudiobookshelf rewrites the [audiobookshelf] table of the file at
// path with whatever apply leaves in the entry it is handed, and nothing
// else. The file is read again under the shared write lock, so apply sees
// what is on disk now rather than what a caller read a moment ago, and a
// mirror save running at the same time cannot lose this one.
//
// The rewrite is proved before it replaces anything: the result parses,
// it carries no unknown key, it decodes to the entry that was meant, it
// would start, and everything outside the section is exactly what it was.
// A save that writes no bytes still makes a file that already holds a
// stored credential owner-only. See writeConfigAtomically for how the mode
// of a written file is chosen.
func UpdateAudiobookshelf(path string, apply func(*AudiobookshelfConfig) error) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("the server was not started with a writable config file")
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	target, err := resolveConfigPath(path)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}
	before := string(body)

	current, decodeErr := ReadAudiobookshelf(target)
	if decodeErr != nil {
		return fmt.Errorf("%w; fix it before editing audiobookshelf settings", decodeErr)
	}
	want := current
	if err := apply(&want); err != nil {
		return err
	}
	if want == current {
		return tightenConfigCredentialMode(target, before)
	}
	rendered := rewriteTable(before, "audiobookshelf", false, audiobookshelfTableBlocks(want))
	if err := verifyAudiobookshelfRewrite(before, rendered, want); err != nil {
		return err
	}
	return writeConfigAtomically(target, []byte(rendered))
}

// verifyAudiobookshelfRewrite proves a rebuilt file before it replaces an
// operator's own.
func verifyAudiobookshelfRewrite(before, rendered string, want AudiobookshelfConfig) error {
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	if got.Audiobookshelf != want {
		return errors.New("rewritten config does not hold the audiobookshelf entry it was given")
	}

	var updated Config
	meta, err := toml.Decode(rendered, &updated)
	if err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return errors.New("rewritten config carries unknown key(s)")
	}
	if err := validateRenderedAudiobookshelf(rendered); err != nil {
		return err
	}
	var original Config
	if _, err := toml.Decode(before, &original); err != nil {
		return safeConfigParseError("original config does not parse", err)
	}
	original.Audiobookshelf, updated.Audiobookshelf = AudiobookshelfConfig{}, AudiobookshelfConfig{}
	if !reflect.DeepEqual(original, updated) {
		return errors.New("rewriting audiobookshelf would change other settings; nothing was saved")
	}
	return nil
}

// validateRenderedAudiobookshelf holds the result to the rules the next
// start applies, with the full environment a start reads — insecure_http
// is what a plain-http peer is held against — and only those rules.
func validateRenderedAudiobookshelf(rendered string) error {
	c := Default()
	if _, err := toml.Decode(rendered, &c); err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	c.applyEnv()
	if err := c.validateSavedAudiobookshelf(); err != nil {
		return fmt.Errorf("the rewritten config would not start: %w; nothing was saved", err)
	}
	return nil
}

// audiobookshelfTableBlocks is the whole section as it is written. The
// trailing empty line separates it from whatever follows.
func audiobookshelfTableBlocks(a AudiobookshelfConfig) []string {
	return []string{
		"[audiobookshelf]\n",
		fmt.Sprintf("enabled = %t\n", a.Enabled),
		fmt.Sprintf("base_url = %q\n", a.BaseURL),
		fmt.Sprintf("account = %q\n", a.Account),
		fmt.Sprintf("api_key = %q\n", a.APIKey),
		fmt.Sprintf("poll_interval = %q\n", writtenDuration(a.PollInterval)),
		fmt.Sprintf("timeout = %q\n", writtenDuration(a.Timeout)),
		fmt.Sprintf("session_overlap = %q\n", writtenDuration(a.SessionOverlap)),
		fmt.Sprintf("active_days = %d\n", a.ActiveDays),
		fmt.Sprintf("stt_url = %q\n", a.STTURL),
		fmt.Sprintf("stt_api_key = %q\n", a.STTAPIKey),
		fmt.Sprintf("stt_model = %q\n", a.STTModel),
		"\n",
	}
}
