package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// speechToTextEnvKeys are the variables that set a setting the admin page
// edits. LISEUR_STT_API_KEY is absent for the reason ADR-0062 gives for
// LISEUR_ABS_API_KEY: an environment key does not stop an administrator
// editing the settings around it.
var speechToTextEnvKeys = []string{"LISEUR_STT_ENABLED", "LISEUR_STT_URL", "LISEUR_STT_MODEL"}

// SpeechToTextEnvironmentManaged names the variables that own a setting
// the admin page exposes, so the page can refuse a save the next start
// would override.
func SpeechToTextEnvironmentManaged() []string {
	var names []string
	for _, key := range speechToTextEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			names = append(names, key)
		}
	}
	return names
}

// ApplySpeechToTextEnvironment overlays the LISEUR_STT_* variables exactly
// as startup does.
func (c *Config) ApplySpeechToTextEnvironment() {
	setBool(&c.SpeechToText.Enabled, "LISEUR_STT_ENABLED")
	setStr(&c.SpeechToText.URL, "LISEUR_STT_URL")
	setStr(&c.SpeechToText.APIKey, "LISEUR_STT_API_KEY")
	setStr(&c.SpeechToText.Model, "LISEUR_STT_MODEL")
}

// normalizedSpeechToText fills and trims an entry the way validation
// does, so a saved entry and the running one compare equal when a restart
// would change nothing.
func normalizedSpeechToText(s SpeechToTextConfig) SpeechToTextConfig {
	s.URL = canonicalAudiobookshelfURL(s.URL)
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.Model = strings.TrimSpace(s.Model)
	if s.URL == "" {
		s.URL = defaultSTTURL
	}
	if s.Model == "" {
		s.Model = defaultSTTModel
	}
	return s
}

// EffectiveSpeechToText is the entry a start would read from saved.
func EffectiveSpeechToText(saved SpeechToTextConfig) SpeechToTextConfig {
	c := Default()
	c.SpeechToText = saved
	c.ApplySpeechToTextEnvironment()
	return normalizedSpeechToText(c.SpeechToText)
}

// ReadSpeechToText reads the [speech_to_text] entry out of the file at
// path, without the environment. An omitted setting comes back with its
// default. A file still holding the keys this section replaced is refused,
// as startup refuses it.
func ReadSpeechToText(path string) (SpeechToTextConfig, error) {
	c, err := readConfigFile(path)
	return c.SpeechToText, err
}

// readConfigFile decodes the file at path over the defaults, without the
// environment. A missing file is the defaults.
func readConfigFile(path string) (Config, error) {
	c := Default()
	if strings.TrimSpace(path) == "" {
		return c, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("read config: %w", err)
	}
	meta, err := toml.Decode(string(body), &c)
	if err != nil {
		return Default(), safeConfigParseError("the configuration file does not parse", err)
	}
	var undecoded []string
	for _, key := range meta.Undecoded() {
		undecoded = append(undecoded, key.String())
	}
	if hint := movedSTTHint(undecoded); hint != "" {
		return Default(), errors.New("the configuration file still holds old settings" + hint)
	}
	return c, nil
}

// SpeechToTextCandidate is the entry a restart would use: saved with the
// environment overlaid and held to the section's rules.
func SpeechToTextCandidate(saved SpeechToTextConfig) (SpeechToTextConfig, error) {
	c := Default()
	c.SpeechToText = saved
	c.ApplySpeechToTextEnvironment()
	err := c.validateSpeechToText()
	return c.SpeechToText, err
}

// UpdateSpeechToText rewrites the [speech_to_text] table of the file at
// path with whatever apply leaves in the entry, under the lock and proofs
// the other admin writers use (ADR-0061, ADR-0062).
func UpdateSpeechToText(path string, apply func(*SpeechToTextConfig) error) error {
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

	current, err := ReadSpeechToText(target)
	if err != nil {
		return fmt.Errorf("%w; fix it before editing speech-to-text settings", err)
	}
	want := current
	if err := apply(&want); err != nil {
		return err
	}
	if want == current {
		return enforceConfigMode(target)
	}
	rendered := rewriteTable(before, "speech_to_text", false, speechToTextTableBlocks(want))
	if err := verifySpeechToTextRewrite(before, rendered, want); err != nil {
		return err
	}
	return writeConfigAtomically(target, []byte(rendered))
}

func verifySpeechToTextRewrite(before, rendered string, want SpeechToTextConfig) error {
	var updated Config
	meta, err := toml.Decode(rendered, &updated)
	if err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	if len(meta.Undecoded()) > 0 {
		return errors.New("rewritten config carries unknown key(s)")
	}
	// Enabled is a default-true boolean, so the section is compared as a
	// start would read it: over the defaults.
	got := Default()
	if _, err := toml.Decode(rendered, &got); err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	if got.SpeechToText != want {
		return errors.New("rewritten config does not hold the speech-to-text entry it was given")
	}
	got.ApplySpeechToTextEnvironment()
	if err := got.validateSpeechToText(); err != nil {
		return fmt.Errorf("the rewritten config would not start: %w; nothing was saved", err)
	}
	var original Config
	if _, err := toml.Decode(before, &original); err != nil {
		return safeConfigParseError("original config does not parse", err)
	}
	original.SpeechToText, updated.SpeechToText = SpeechToTextConfig{}, SpeechToTextConfig{}
	if !reflect.DeepEqual(original, updated) {
		return errors.New("rewriting speech_to_text would change other settings; nothing was saved")
	}
	return nil
}

func speechToTextTableBlocks(s SpeechToTextConfig) []string {
	return []string{
		"[speech_to_text]\n",
		fmt.Sprintf("enabled = %t\n", s.Enabled),
		fmt.Sprintf("url = %q\n", s.URL),
		fmt.Sprintf("api_key = %q\n", s.APIKey),
		fmt.Sprintf("model = %q\n", s.Model),
		"\n",
	}
}
