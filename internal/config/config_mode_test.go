package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretFreeEntry is a mirror entry a save can write with no credential in
// it: disabled, with neither a key nor a password.
func secretFreeEntry(name string) MirrorConfig {
	m := workingEntry(name)
	m.Enabled = false
	m.RemoteKey = ""
	return m
}

func configMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func readableConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := writeMirrorConfig(t, body)
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

const configPlain = "[content]\ncache_dir = \"covers\"\n"

// saveSecretFreeMirror is a mirror save that writes no credential.
func saveSecretFreeMirror(path string) error {
	return UpdateMirrors(path, setLegacy(secretFreeEntry("orbit")))
}

// saveAudiobookshelf is an audiobookshelf save that writes no credential.
func saveAudiobookshelf(path string) error {
	return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
		a.BaseURL = "https://abs.example.com"
		return nil
	})
}

func noopMirrorSave(path string) error {
	return UpdateMirrors(path, func(*MirrorFile) error { return nil })
}

func noopAudiobookshelfSave(path string) error {
	return UpdateAudiobookshelf(path, func(*AudiobookshelfConfig) error { return nil })
}

type configWriter struct {
	name string
	save func(string) error
	noop func(string) error
}

func configWriters() []configWriter {
	return []configWriter{
		{"mirror", saveSecretFreeMirror, noopMirrorSave},
		{"audiobookshelf", saveAudiobookshelf, noopAudiobookshelfSave},
	}
}

// TestAnAdminSaveLeavesTheConfigFileOwnerOnly is the policy: a save through
// the admin pages leaves the file owner-only, whatever mode an editor gave
// it and whatever it holds.
func TestAnAdminSaveLeavesTheConfigFileOwnerOnly(t *testing.T) {
	const configWithSecrets = "[audiobookshelf]\napi_key = \"file-secret\"\n\n" +
		"[mirror]\nenabled = false\nremote_key = \"0123456789abcdef0123456789abcdef\"\n\n" +
		configPlain

	for _, w := range configWriters() {
		for _, tc := range []struct {
			name string
			body string
			mode os.FileMode
		}{
			{"0644 secret-free", configPlain, 0o644},
			{"0640 secret-free", configPlain, 0o640},
			{"0644 holding a stored credential", configWithSecrets, 0o644},
		} {
			t.Run(w.name+" "+tc.name, func(t *testing.T) {
				path := readableConfig(t, tc.body, tc.mode)
				if err := w.save(path); err != nil {
					t.Fatalf("the save was refused: %v", err)
				}
				if got := configMode(t, path); got != 0o600 {
					t.Fatalf("the file is %04o after a save, want 0600", got)
				}
			})
		}
	}
}

// TestANoopSaveTightensAnExistingConfigFile: a save that writes nothing is
// still an accepted save, and the file it leaves behind is owner-only — a
// file an editor made world-readable included.
func TestANoopSaveTightensAnExistingConfigFile(t *testing.T) {
	for _, w := range configWriters() {
		for _, tc := range []struct {
			name string
			mode os.FileMode
		}{
			{"0644", 0o644},
			{"0640", 0o640},
			{"0400", 0o400},
		} {
			t.Run(w.name+" "+tc.name, func(t *testing.T) {
				path := readableConfig(t, configPlain, tc.mode)
				before := readFile(t, path)
				if err := w.noop(path); err != nil {
					t.Fatalf("the no-op save was refused: %v", err)
				}
				if got := configMode(t, path); got != 0o600 {
					t.Fatalf("the file is %04o after a no-op save, want 0600", got)
				}
				if got := readFile(t, path); got != before {
					t.Fatalf("a no-op save changed the bytes:\n%s", got)
				}
			})
		}
	}
}

// TestANoopSaveOfAnAbsentConfigFileCreatesNothing: nothing to change and no
// file to change means nothing happens. A no-op save does not invent a
// configuration file.
func TestANoopSaveOfAnAbsentConfigFileCreatesNothing(t *testing.T) {
	for _, w := range configWriters() {
		t.Run(w.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "liseur-sync.toml")
			if err := w.noop(path); err != nil {
				t.Fatalf("the no-op save was refused: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("a no-op save created a file: %v", err)
			}
		})
	}
}

// TestASymlinkedConfigEndsOwnerOnlyBehindTheLink: the write follows the
// link, so it is the file the link names that must end up owner-only, and
// the link itself stays a link.
func TestASymlinkedConfigEndsOwnerOnlyBehindTheLink(t *testing.T) {
	for _, w := range configWriters() {
		t.Run(w.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "real.toml")
			if err := os.WriteFile(target, []byte(configPlain), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, "liseur-sync.toml")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := w.save(link); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("the symlink was replaced by a file: %v %v", info, err)
			}
			if got := configMode(t, target); got != 0o600 {
				t.Fatalf("the file behind the link is %04o, want 0600", got)
			}
		})
	}
}

// TestANewConfigFileIsOwnerOnly: a file this server creates is never
// created wider.
func TestANewConfigFileIsOwnerOnly(t *testing.T) {
	for _, w := range configWriters() {
		t.Run(w.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "liseur-sync.toml")
			if err := w.save(path); err != nil {
				t.Fatal(err)
			}
			if got := configMode(t, path); got != 0o600 {
				t.Fatalf("a new config file is %04o, want 0600", got)
			}
		})
	}
}

// TestEnvironmentSecretsNeverReachTheFile: a credential the environment
// supplies is not on disk, so it never appears in the file. Copying it
// there is the one thing the page promises never to do.
func TestEnvironmentSecretsNeverReachTheFile(t *testing.T) {
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "env-mirror-key")
	t.Setenv("LISEUR_ABS_API_KEY", "env-abs-key")
	t.Setenv("LISEUR_ABS_STT_API_KEY", "env-stt-key")

	for _, w := range configWriters() {
		t.Run(w.name, func(t *testing.T) {
			path := readableConfig(t, configPlain, 0o644)
			if err := w.save(path); err != nil {
				t.Fatalf("the save was refused: %v", err)
			}
			if got := configMode(t, path); got != 0o600 {
				t.Fatalf("the file is %04o after a save, want 0600", got)
			}
			text := readFile(t, path)
			for _, secret := range []string{"env-mirror-key", "env-abs-key", "env-stt-key"} {
				if strings.Contains(text, secret) {
					t.Fatalf("the environment secret %q was written to the file:\n%s", secret, text)
				}
			}
		})
	}
}

// TestARefusedSaveLeavesBytesAndModeAlone: a save that does not land is not
// a reason to touch anything, and certainly not a reason to chmod a file the
// admin never successfully edited.
func TestARefusedSaveLeavesBytesAndModeAlone(t *testing.T) {
	const trap = "[audiobookshelf]\napi_key = \"file-secret\"\n\n" +
		"[content]\ncache_dir = \"\"\"covers\n[mirror]\nnot a table\n\"\"\"\n"
	const absSecret = "[audiobookshelf]\napi_key = \"file-secret\"\n"

	for _, tc := range []struct {
		name string
		body string
		save func(string) error
	}{
		{"mirror", trap, saveSecretFreeMirror},
		{"audiobookshelf", absSecret, func(path string) error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				a.BaseURL = "https://user:pass@abs.example.com"
				return nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := readableConfig(t, tc.body, 0o644)
			if err := tc.save(path); err == nil {
				t.Fatal("a save that should have been refused was accepted")
			}
			if got := readFile(t, path); got != tc.body {
				t.Fatalf("the refused save changed the bytes:\n%s", got)
			}
			if got := configMode(t, path); got != 0o644 {
				t.Fatalf("the refused save changed the mode to %04o", got)
			}
		})
	}
}

// TestANoopSaveReadsNothingOutsideTheEditedSection: deciding the mode does
// not mean parsing the whole file, so an unrelated setting with the wrong
// type neither refuses the save nor keeps the file readable.
func TestANoopSaveReadsNothingOutsideTheEditedSection(t *testing.T) {
	const body = "[audiobookshelf]\napi_key = \"file-secret\"\n\n" +
		"[content]\ncache_dir = \"covers\"\nscan_max_depth = \"oops\"\n"
	path := readableConfig(t, body, 0o644)
	if err := noopMirrorSave(path); err != nil {
		t.Fatalf("the no-op save was refused: %v", err)
	}
	if got := configMode(t, path); got != 0o600 {
		t.Fatalf("the file is %04o after a no-op save, want 0600", got)
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("the no-op save changed the bytes:\n%s", got)
	}
}
