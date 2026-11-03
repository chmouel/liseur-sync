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

// TestACredentialBearingConfigSaveIsOwnerOnly is the policy: a save that
// leaves a stored credential on disk makes the file owner-only, whatever
// mode an editor gave it, because the secret travels in the file itself.
func TestACredentialBearingConfigSaveIsOwnerOnly(t *testing.T) {
	const plain = "[content]\ncache_dir = \"covers\"\n"

	legacyPassword := workingEntry("orbit")
	legacyPassword.Protocol = ProtocolBookOrbit
	legacyPassword.BaseURL = "https://orbit.example.com/api/v1"
	legacyPassword.RemoteKey = ""
	legacyPassword.RemotePassword = "hunter2"
	legacyDisabled := legacyPassword
	legacyDisabled.Enabled = false

	listKey := workingEntry("kobo")
	listKey.BaseURL = "https://kobo.example.com/sync"
	listPassword := legacyPassword
	listPassword.Name = "kobo"
	listDisabled := listKey
	listDisabled.Enabled = false

	saveLegacy := func(m MirrorConfig) func(string) error {
		return func(path string) error { return UpdateMirrors(path, setLegacy(m)) }
	}
	saveList := func(m MirrorConfig) func(string) error {
		return func(path string) error {
			return UpdateMirrors(path, func(f *MirrorFile) error {
				f.List = append(f.List, m)
				return nil
			})
		}
	}
	saveAudiobookshelf := func(change func(*AudiobookshelfConfig)) func(string) error {
		return func(path string) error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				change(a)
				return nil
			})
		}
	}

	for _, tc := range []struct {
		name string
		body string
		save func(string) error
	}{
		{"legacy remote password", plain, saveLegacy(legacyPassword)},
		{"legacy remote key", plain, saveLegacy(workingEntry("orbit"))},
		{"disabled legacy remote password", plain, saveLegacy(legacyDisabled)},
		{"list remote password", plain, saveList(listPassword)},
		{"list remote key", plain, saveList(listKey)},
		{"disabled list remote key", plain, saveList(listDisabled)},
		{"audiobookshelf api key", plain, saveAudiobookshelf(func(a *AudiobookshelfConfig) {
			a.APIKey = "abs-key"
		})},
		{"audiobookshelf stt api key", plain, saveAudiobookshelf(func(a *AudiobookshelfConfig) {
			a.STTAPIKey = "stt-key"
		})},
		// A save that writes no credential of its own still lands in a
		// file whose database URL carries one, so the file is tightened.
		{"existing database url password",
			"[database]\ndriver = \"postgres\"\nurl = \"postgres://liseur:hunter2@db:5432/liseur\"\n\n" + plain,
			saveLegacy(secretFreeEntry("orbit"))},
		{"existing database keyword password",
			"[database]\ndriver = \"postgres\"\nurl = \"host=db password=hunter2 dbname=liseur\"\n\n" + plain,
			saveLegacy(secretFreeEntry("orbit"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := readableConfig(t, tc.body, 0o644)
			if err := tc.save(path); err != nil {
				t.Fatalf("the save was refused: %v", err)
			}
			if got := configMode(t, path); got != 0o600 {
				t.Fatalf("the file holding a credential is %04o, want 0600", got)
			}
		})
	}
}

// TestASecretFreeConfigSaveKeepsTheMode: the permission promise still
// holds where it can. A file with nothing to hide keeps what it had.
func TestASecretFreeConfigSaveKeepsTheMode(t *testing.T) {
	const plain = "[content]\ncache_dir = \"covers\"\n"
	for _, tc := range []struct {
		name string
		mode os.FileMode
		save func(string) error
	}{
		{"mirror 0640", 0o640, func(path string) error {
			return UpdateMirrors(path, setLegacy(secretFreeEntry("orbit")))
		}},
		{"mirror 0644", 0o644, func(path string) error {
			return UpdateMirrors(path, setLegacy(secretFreeEntry("orbit")))
		}},
		{"audiobookshelf 0640", 0o640, func(path string) error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				a.BaseURL = "https://abs.example.com"
				return nil
			})
		}},
		{"audiobookshelf 0644", 0o644, func(path string) error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				a.BaseURL = "https://abs.example.com"
				return nil
			})
		}},
		{"sqlite path is not a credential", 0o644, func(path string) error {
			return UpdateMirrors(path, setLegacy(secretFreeEntry("orbit")))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := plain
			if tc.name == "sqlite path is not a credential" {
				body = "[database]\ndriver = \"sqlite\"\nurl = \"/var/lib/liseur-sync.db\"\n\n" + plain
			}
			path := readableConfig(t, body, tc.mode)
			if err := tc.save(path); err != nil {
				t.Fatalf("the save was refused: %v", err)
			}
			if got := configMode(t, path); got != tc.mode {
				t.Fatalf("the secret-free file is %04o, want %04o", got, tc.mode)
			}
		})
	}
}

// TestANoopSaveStillTightensACredentialBearingFile: a save that writes
// nothing is still a save somebody asked for, and a file it leaves
// world-readable is the same plaintext secret as one a rewrite would have
// left. Only a file that already holds a credential is touched.
func TestANoopSaveStillTightensACredentialBearingFile(t *testing.T) {
	const mirrorSecret = "[mirror]\nenabled = false\nremote_key = \"0123456789abcdef0123456789abcdef\"\n"
	const absSecret = "[audiobookshelf]\napi_key = \"file-secret\"\n"
	const noSecret = "[content]\ncache_dir = \"covers\"\n"

	for _, tc := range []struct {
		name     string
		body     string
		save     func(string) error
		wantMode os.FileMode
	}{
		{"mirror", mirrorSecret, func(path string) error {
			return UpdateMirrors(path, func(*MirrorFile) error { return nil })
		}, 0o600},
		{"audiobookshelf", absSecret, func(path string) error {
			return UpdateAudiobookshelf(path, func(*AudiobookshelfConfig) error { return nil })
		}, 0o600},
		{"mirror without a secret", noSecret, func(path string) error {
			return UpdateMirrors(path, func(*MirrorFile) error { return nil })
		}, 0o644},
		{"audiobookshelf without a secret", noSecret, func(path string) error {
			return UpdateAudiobookshelf(path, func(*AudiobookshelfConfig) error { return nil })
		}, 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := readableConfig(t, tc.body, 0o644)
			before := readFile(t, path)
			if err := tc.save(path); err != nil {
				t.Fatalf("the no-op save was refused: %v", err)
			}
			if got := configMode(t, path); got != tc.wantMode {
				t.Fatalf("the file is %04o after a no-op save, want %04o", got, tc.wantMode)
			}
			if got := readFile(t, path); got != before {
				t.Fatalf("a no-op save changed the bytes:\n%s", got)
			}
		})
	}
}

// TestASymlinkedConfigEndsOwnerOnlyBehindTheLink: the write follows the
// link, so it is the file the link names that must end up owner-only, and
// the link itself stays a link.
func TestASymlinkedConfigEndsOwnerOnlyBehindTheLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.toml")
	if err := os.WriteFile(target, []byte("[content]\ncache_dir = \"covers\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "liseur-sync.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMirrors(link, setLegacy(workingEntry("orbit"))); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced by a file: %v %v", info, err)
	}
	if got := configMode(t, target); got != 0o600 {
		t.Fatalf("the file behind the link is %04o, want 0600", got)
	}
}

// TestANewConfigFileIsOwnerOnly: a file this server creates may hold a
// credential as soon as it holds anything, so it is never created wider.
func TestANewConfigFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := UpdateMirrors(path, setLegacy(secretFreeEntry("orbit"))); err != nil {
		t.Fatal(err)
	}
	if got := configMode(t, path); got != 0o600 {
		t.Fatalf("a new config file is %04o, want 0600", got)
	}
}

// TestARefusedSaveLeavesBytesAndModeAlone: a save that does not land is
// not a reason to touch anything, and certainly not a reason to chmod a
// file the admin never successfully edited.
func TestARefusedSaveLeavesBytesAndModeAlone(t *testing.T) {
	const trap = "[audiobookshelf]\napi_key = \"file-secret\"\n\n" +
		"[content]\ncache_dir = \"\"\"covers\n[mirror]\nnot a table\n\"\"\"\n"
	const absSecret = "[audiobookshelf]\napi_key = \"file-secret\"\n"

	for _, tc := range []struct {
		name string
		body string
		save func(string) error
	}{
		{"mirror", trap, func(path string) error {
			return UpdateMirrors(path, setLegacy(workingEntry("orbit")))
		}},
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

// TestEnvironmentSecretsNeverMakeAFileOwnerOnly: a credential the
// environment supplies is not on disk, so it neither decides the mode nor
// appears in the file. Copying it there is the one thing the page promises
// never to do.
func TestEnvironmentSecretsNeverMakeAFileOwnerOnly(t *testing.T) {
	const plain = "[content]\ncache_dir = \"covers\"\n"
	t.Setenv("LISEUR_MIRROR_REMOTE_KEY", "env-mirror-key")
	t.Setenv("LISEUR_ABS_API_KEY", "env-abs-key")
	t.Setenv("LISEUR_ABS_STT_API_KEY", "env-stt-key")

	for _, tc := range []struct {
		name string
		save func(string) error
	}{
		{"mirror", func(path string) error {
			return UpdateMirrors(path, setLegacy(secretFreeEntry("orbit")))
		}},
		{"audiobookshelf", func(path string) error {
			return UpdateAudiobookshelf(path, func(a *AudiobookshelfConfig) error {
				a.BaseURL = "https://abs.example.com"
				return nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := readableConfig(t, plain, 0o644)
			if err := tc.save(path); err != nil {
				t.Fatalf("the save was refused: %v", err)
			}
			if got := configMode(t, path); got != 0o644 {
				t.Fatalf("an environment secret made the file %04o", got)
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
