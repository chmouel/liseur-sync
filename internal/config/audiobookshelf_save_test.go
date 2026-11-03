package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeAudiobookshelfConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// audiobookshelfFile is a deployment with a mirror, a list entry, STT
// settings and an unrelated section, all of which a save must leave
// alone.
const audiobookshelfFile = `listen_addr = "127.0.0.1:8585"

[mirror]
enabled = false
name = "peer"
base_url = "https://orbit.example.com/sync"
account = "alice"
remote_user = "reader"
remote_key = "0123456789abcdef0123456789abcdef"

[[mirrors]]
enabled = false
name = "kobo"
base_url = "https://kobo.example.com/sync"
account = "alice"
remote_user = "reader2"

[audiobookshelf]
enabled = false
base_url = "https://abs.example.com"
account = "alice"
api_key = "file-secret"
poll_interval = "45s"
timeout = "15s"
session_overlap = "20m"
active_days = 7
stt_url = "https://stt.example/v1"
stt_api_key = "stt-secret"
stt_model = "whisper-small"

[content]
cache_dir = "covers"
`

func workingAudiobookshelfEntry() AudiobookshelfConfig {
	a := Default().Audiobookshelf
	a.Enabled = true
	a.BaseURL = "https://abs.example.com"
	a.Account = "alice"
	a.APIKey = "new-secret"
	return a
}

func TestUpdateAudiobookshelfPreservesEverythingElse(t *testing.T) {
	path := writeAudiobookshelfConfig(t, audiobookshelfFile)
	want := workingAudiobookshelfEntry()
	if err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.Enabled = want.Enabled
		a.BaseURL = want.BaseURL
		a.Account = want.Account
		a.APIKey = want.APIKey
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	for _, kept := range []string{
		`listen_addr = "127.0.0.1:8585"`,
		"[mirror]", "[[mirrors]]", `name = "kobo"`,
		`poll_interval = "45s"`, `timeout = "15s"`,
		`session_overlap = "20m"`, `active_days = 7`,
		`stt_url = "https://stt.example/v1"`, `stt_api_key = "stt-secret"`,
		`stt_model = "whisper-small"`, `cache_dir = "covers"`,
	} {
		if !strings.Contains(text, kept) {
			t.Fatalf("a save lost %q:\n%s", kept, text)
		}
	}
	if strings.Count(text, "[audiobookshelf]") != 1 {
		t.Fatalf("the section was duplicated:\n%s", text)
	}
	if strings.Contains(text, "file-secret") {
		t.Fatalf("the old key survived the replacement:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Audiobookshelf.APIKey != "new-secret" || !loaded.Audiobookshelf.Enabled {
		t.Fatalf("the saved entry did not load: %+v", loaded.Audiobookshelf)
	}
	if loaded.Audiobookshelf.STTModel != "whisper-small" {
		t.Fatalf("STT settings moved: %+v", loaded.Audiobookshelf)
	}
}

func TestUpdateAudiobookshelfAddsTheSection(t *testing.T) {
	path := writeAudiobookshelfConfig(t, "listen_addr = \"127.0.0.1:8585\"\n\n[content]\ncache_dir = \"covers\"\n")
	a := Default().Audiobookshelf
	a.Enabled = false
	a.BaseURL = "https://abs.example.com"
	if err := UpdateAudiobookshelf(path, func(got *AudiobookshelfConfig) error {
		*got = a
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "[audiobookshelf]") ||
		!strings.Contains(text, `base_url = "https://abs.example.com"`) {
		t.Fatalf("the section was not written:\n%s", text)
	}
	if !strings.Contains(text, "cache_dir = \"covers\"") {
		t.Fatalf("the rest of the file moved:\n%s", text)
	}
}

func TestUpdateAudiobookshelfKeepsPermissionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.toml")
	if err := os.WriteFile(target, []byte("[content]\ncache_dir = \"covers\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "liseur-sync.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAudiobookshelf(link, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://abs.example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v %v", info, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("permissions became %04o, want 0640", got)
	}
}

func TestUpdateAudiobookshelfRefusesABrokenFile(t *testing.T) {
	const broken = "[audiobookshelf]\napi_key = \"unterminated\n"
	path := writeAudiobookshelfConfig(t, broken)
	before := readFile(t, path)
	err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://abs.example.com"
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("a broken file was not refused: %v", err)
	}
	if readFile(t, path) != before {
		t.Fatal("a refused save changed the file")
	}
}

func TestUpdateAudiobookshelfNeedsAConfigFile(t *testing.T) {
	err := UpdateAudiobookshelf("", func(*AudiobookshelfConfig) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "writable config file") {
		t.Fatalf("no config path was refused with %v", err)
	}
}

func TestUpdateAudiobookshelfValidatesOnlyAudiobookshelf(t *testing.T) {
	// Another section is missing what a start requires; saving this one
	// must still work, because what it writes is held only to the
	// audiobookshelf rules.
	path := writeAudiobookshelfConfig(t, "[database]\ndriver = \"sqlite\"\n")
	if err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://abs.example.com"
		return nil
	}); err != nil {
		t.Fatalf("an unrelated broken setting blocked the save: %v", err)
	}
}

// TestUpdateAudiobookshelfRefusesADisabledURLCarryingASecret: a URL that
// is not in use yet can still be saved, but never with a credential, a
// query or a fragment in it that the page would show.
func TestUpdateAudiobookshelfRefusesADisabledURLCarryingASecret(t *testing.T) {
	for _, raw := range []string{
		"https://user:pass@abs.example.com",
		"https://abs.example.com/?token=x",
		"https://abs.example.com/#frag",
	} {
		path := writeAudiobookshelfConfig(t, "[content]\ncache_dir = \"covers\"\n")
		err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
			a.Enabled = false
			a.BaseURL = raw
			return nil
		})
		if err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
}

func TestAudiobookshelfEnvironmentManagedNamesOnlyExposedSettings(t *testing.T) {
	t.Setenv("LISEUR_ABS_BASE_URL", "https://from-env.example")
	t.Setenv("LISEUR_ABS_API_KEY", "env-secret")
	got := AudiobookshelfEnvironmentManaged()
	if len(got) != 1 || got[0] != "LISEUR_ABS_BASE_URL" {
		t.Fatalf("managed variables: %v", got)
	}
}

func TestAudiobookshelfCandidateUsesTheEnvironmentKey(t *testing.T) {
	t.Setenv("LISEUR_ABS_API_KEY", "env-secret")
	path := writeAudiobookshelfConfig(t, "[content]\ncache_dir = \"covers\"\n")
	entry := workingAudiobookshelfEntry()
	entry.APIKey = ""
	candidate, err := AudiobookshelfCandidate(path, entry)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.APIKey != "env-secret" {
		t.Fatalf("the environment key was not used: %q", candidate.APIKey)
	}
}

// TestAudiobookshelfCandidateRefusesAnEmptyEnvironmentOverride: an
// operator who cleared LISEUR_ABS_API_KEY gets a refusal, not a silent
// start with the stale key the file holds.
func TestAudiobookshelfCandidateRefusesAnEmptyEnvironmentOverride(t *testing.T) {
	t.Setenv("LISEUR_ABS_API_KEY", "")
	path := writeAudiobookshelfConfig(t, "[content]\ncache_dir = \"covers\"\n")
	if _, err := AudiobookshelfCandidate(path, workingAudiobookshelfEntry()); err == nil {
		t.Fatal("an enabled integration with an empty environment key was accepted")
	}
}

// TestAudiobookshelfCandidateReadsInsecureHTTPFromTheFile: the same
// startup environment a save is held against, including the scope a
// plain-http peer needs.
func TestAudiobookshelfCandidateReadsInsecureHTTPFromTheFile(t *testing.T) {
	path := writeAudiobookshelfConfig(t, "insecure_http = true\n")
	entry := workingAudiobookshelfEntry()
	entry.BaseURL = "http://abs.lan:13378"
	if _, err := AudiobookshelfCandidate(path, entry); err != nil {
		t.Fatalf("insecure_http in the file was ignored: %v", err)
	}
	if _, err := AudiobookshelfCandidate("", entry); err == nil {
		t.Fatal("plain http without insecure_http was accepted")
	}
}

// TestAudiobookshelfAndMirrorSavesShareOneLock: two forms saving at once
// must not each rewrite a body read before the other wrote, or one of the
// two changes vanishes.
func TestAudiobookshelfAndMirrorSavesShareOneLock(t *testing.T) {
	path := writeAudiobookshelfConfig(t, "listen_addr = \"127.0.0.1:8585\"\n\n[content]\ncache_dir = \"covers\"\n")
	entry := workingAudiobookshelfEntry()
	mirror := workingEntry("orbit")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, save := range []func() error{
		func() error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				*a = entry
				return nil
			})
		},
		func() error {
			return UpdateMirrors(path, setLegacy(mirror))
		},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- save()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	text := readFile(t, path)
	if !strings.Contains(text, "[audiobookshelf]") || !strings.Contains(text, "[mirror]") {
		t.Fatalf("a concurrent save lost a section:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Audiobookshelf.APIKey != "new-secret" || loaded.Mirror.Name != "orbit" {
		t.Fatalf("both sections did not survive: %+v %+v", loaded.Audiobookshelf, loaded.Mirror)
	}
}

func TestReadAudiobookshelfFillsDefaults(t *testing.T) {
	got, err := ReadAudiobookshelf("")
	if err != nil {
		t.Fatal(err)
	}
	want := Default().Audiobookshelf
	if got != want {
		t.Fatalf("defaults: %+v, want %+v", got, want)
	}
}

func TestUpdateAudiobookshelfWritesNothingWhenNothingChanged(t *testing.T) {
	path := writeAudiobookshelfConfig(t, audiobookshelfFile)
	before := readFile(t, path)
	if err := UpdateAudiobookshelf(path, func(*AudiobookshelfConfig) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if readFile(t, path) != before {
		t.Fatal("an unchanged save rewrote the file")
	}
}

func TestUpdateAudiobookshelfRefusesToCorruptAMultiLineString(t *testing.T) {
	body := "note = \"\"\"\n[audiobookshelf]\n\"\"\"\n\n[content]\ncache_dir = \"covers\"\n"
	path := writeAudiobookshelfConfig(t, body)
	err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://abs.example.com"
		return nil
	})
	if err == nil {
		t.Fatal("a table header inside a string was treated as one")
	}
	if readFile(t, path) != body {
		t.Fatal("a refused rewrite changed the file")
	}
}

// TestAudiobookshelfValidationDropsOneTrailingSlash: startup removes
// one slash and PeerIdentity removes one more; both are the pre-existing
// spelling of the peer, so a config an operator wrote with stray slashes
// keeps the identity its history was stored under.
func TestAudiobookshelfValidationDropsOneTrailingSlash(t *testing.T) {
	cfg := workingAudiobookshelf(t)
	cfg.Audiobookshelf.BaseURL = "https://abs.example.com///"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Audiobookshelf.BaseURL; got != "https://abs.example.com//" {
		t.Fatalf("base_url normalised to %q", got)
	}
}

// TestCanonicalAudiobookshelfURLMatchesStartup: the restart check and
// startup must agree on what an address is, or a restart that changes
// nothing would report one.
func TestCanonicalAudiobookshelfURLMatchesStartup(t *testing.T) {
	for _, raw := range []string{
		"https://abs.example.com",
		"https://abs.example.com/",
		"https://abs.example.com//",
		"https://abs.example.com/abs//",
		"HTTPS://Abs.Example.COM",
	} {
		cfg := workingAudiobookshelf(t)
		cfg.Audiobookshelf.BaseURL = raw
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if got, want := canonicalAudiobookshelfURL(raw), cfg.Audiobookshelf.BaseURL; got != want {
			t.Fatalf("%q canonicalised to %q, startup stores %q", raw, got, want)
		}
	}
}

// TestReadAudiobookshelfDoesNotEchoAParseErrorLiteral: a TOML diagnostic
// quotes the source it choked on, which can be the value of the key beside
// it. The page, a redirect and the audit line must none of them carry it.
func TestReadAudiobookshelfDoesNotEchoAParseErrorLiteral(t *testing.T) {
	const secret = "sup3r/secret+key"
	path := writeAudiobookshelfConfig(t,
		"[audiobookshelf]\napi_key = \""+secret+`\xZ"`+"\n")

	for name, call := range map[string]func() error{
		"read": func() error {
			_, err := ReadAudiobookshelf(path)
			return err
		},
		"candidate": func() error {
			_, err := AudiobookshelfCandidate(path, Default().Audiobookshelf)
			return err
		},
		"update": func() error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				a.BaseURL = "https://abs.example.com"
				return nil
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("a malformed file was accepted")
			}
			msg := err.Error()
			if strings.Contains(msg, secret) || strings.Contains(msg, "secret") {
				t.Fatalf("the error quoted the source literal: %q", msg)
			}
			if !strings.Contains(msg, "line 2") {
				t.Fatalf("the error lost its location: %q", msg)
			}
		})
	}
}

func TestEffectiveAudiobookshelfAppliesTheEnvironment(t *testing.T) {
	t.Setenv("LISEUR_ABS_ENABLED", "true")
	t.Setenv("LISEUR_ABS_POLL_INTERVAL", "7m")
	a := Default().Audiobookshelf
	a.Enabled = false
	a.BaseURL = "https://abs.example.com///"
	got := EffectiveAudiobookshelf(a)
	if !got.Enabled || got.PollInterval.Duration() != 7*time.Minute ||
		got.BaseURL != "https://abs.example.com//" {
		t.Fatalf("the effective entry did not take the environment: %+v", got)
	}
}

// TestADisabledEntryStillStartsWithAnAddressAStartWouldRefuse: startup
// never parsed a disabled address, and an upgrade must not turn a file
// that started yesterday into one that does not. A save still refuses to
// write one that carries a credential.
func TestADisabledEntryStillStartsWithAnAddressAStartWouldRefuse(t *testing.T) {
	path := writeAudiobookshelfConfig(t,
		"insecure_http = true\n\n[audiobookshelf]\nenabled = false\nbase_url = \"abs.example.com\"\n")
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("a disabled entry stopped the server from starting: %v", err)
	}
	if got := loaded.Audiobookshelf.BaseURL; got != "abs.example.com" {
		t.Fatalf("a disabled address was rewritten: %q", got)
	}
	if err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://user:pass@abs.example.com"
		return nil
	}); err == nil {
		t.Fatal("a save wrote an address carrying a credential")
	}
}

// TestUpdateAudiobookshelfRewritesAMixedCaseHeaderInPlace. The decoder
// folds case when it binds the table, so the section scan has to as well:
// appending [audiobookshelf] beside an [Audiobookshelf] the operator wrote
// leaves two tables whose decode order decides which one wins.
func TestUpdateAudiobookshelfRewritesAMixedCaseHeaderInPlace(t *testing.T) {
	path := writeAudiobookshelfConfig(t, "listen_addr = \"127.0.0.1:8585\"\n\n"+
		"[Audiobookshelf]\nenabled = false\n"+
		"base_url = \"https://abs.example.com\"\naccount = \"alice\"\n"+
		"api_key = \"file-secret\"\n")
	want := workingAudiobookshelfEntry()
	if err := UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		*a = want
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if got := strings.Count(strings.ToLower(text), "[audiobookshelf]"); got != 1 {
		t.Fatalf("the file holds %d audiobookshelf headers:\n%s", got, text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Audiobookshelf.APIKey != "new-secret" || !loaded.Audiobookshelf.Enabled {
		t.Fatalf("the rewritten section did not load: %+v", loaded.Audiobookshelf)
	}
}
