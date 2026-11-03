package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeMirrorConfig puts body at a fresh path and hands it back.
func writeMirrorConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// setLegacy is the mutation the page performs: replace the single entry.
func setLegacy(m MirrorConfig) func(*MirrorFile) error {
	return func(f *MirrorFile) error {
		f.Legacy, f.HasLegacy = m, true
		return nil
	}
}

func workingEntry(name string) MirrorConfig {
	m := Default().Mirror
	m.Enabled = true
	m.Name = name
	m.BaseURL = "https://orbit.example.com/api/v1/koreader"
	m.Account = "alice"
	m.RemoteUser = "reader"
	m.RemoteKey = "0123456789abcdef0123456789abcdef"
	return m
}

func TestUpdateMirrorsWritesASingleEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	m := workingEntry("orbit")
	if err := UpdateMirrors(path, setLegacy(m)); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.HasPrefix(text, "[mirror]\n") {
		t.Fatalf("a fresh config file does not start at its first setting:\n%q", text)
	}
	if !strings.Contains(text, "poll_interval = \"5m\"") ||
		!strings.Contains(text, "resolve_retry_interval = \"24h\"") ||
		!strings.Contains(text, "timeout = \"20s\"") {
		t.Fatalf("durations were not written the way people write them:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Mirror.Enabled || !loaded.MirrorTableDefined() {
		t.Fatalf("the single entry did not load as one: %+v", loaded.Mirror)
	}
	if loaded.Mirror.PollInterval != m.PollInterval || loaded.Mirror.Timeout != m.Timeout {
		t.Fatalf("durations did not survive the round trip: %+v", loaded.Mirror)
	}
}

func TestUpdateMirrorsPreservesUnrelatedConfig(t *testing.T) {
	path := writeMirrorConfig(t, "listen_addr = \"127.0.0.1:8585\"\n\n"+
		"[database]\ndriver = \"sqlite\"\n\n"+
		"[mirror]\nenabled = false\nremote_key = \"old\"\n\n"+
		"[content]\ncache_dir = \"covers\"\n")
	m := workingEntry("orbit")
	m.RemoteKey = "new-key"
	if err := UpdateMirrors(path, setLegacy(m)); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "listen_addr = \"127.0.0.1:8585\"") ||
		!strings.Contains(text, "cache_dir = \"covers\"") {
		t.Fatalf("unrelated configuration was lost:\n%s", text)
	}
	if strings.Count(text, "[mirror]") != 1 ||
		!strings.Contains(text, "remote_key = \"new-key\"") ||
		strings.Contains(text, "remote_key = \"old\"") {
		t.Fatalf("mirror section was not replaced:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.Account != "alice" || loaded.Mirror.BaseURL != m.BaseURL {
		t.Fatalf("saved mirror did not load: %+v", loaded.Mirror)
	}
}

// TestUpdateMirrorsLeavesTheSingleEntryAloneWhenTheListChanges is the
// promise that makes a migration optional: a deployment with a [mirror]
// table gains [[mirrors]] entries beside it, and not one byte of the
// table moves.
func TestUpdateMirrorsLeavesTheSingleEntryAloneWhenTheListChanges(t *testing.T) {
	head := "listen_addr = \"127.0.0.1:8585\"\n\n" +
		"[mirror] # the old entry\n" +
		"enabled = true\nname = \"orbit\"\nbase_url = \"https://orbit.example/sync\"\n" +
		"account = \"alice\"\nremote_user = \"reader\"\n" +
		"remote_key = \"0123456789abcdef0123456789abcdef\"\n" +
		"device_id = \"liseur-sync\"\n\n" +
		"[content]\ncache_dir = \"covers\"\n"
	path := writeMirrorConfig(t, head)
	file, err := ReadMirrors(path)
	if err != nil {
		t.Fatal(err)
	}
	second := workingEntry("kobo")
	second.BaseURL = "https://kobo.example.com/sync"
	if err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.List = append(f.List, second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.HasPrefix(text, head) {
		t.Fatalf("the single entry or what came before it moved:\n%s", text)
	}
	if strings.Count(text, "[mirror]") != 1 || strings.Count(text, "[[mirrors]]") != 1 {
		t.Fatalf("the file does not hold one table and one list entry:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.MirrorTableDefined() || loaded.Mirror.Name != file.Legacy.Name {
		t.Fatalf("the single entry changed: %+v", loaded.Mirror)
	}
	entries := loaded.EnabledMirrors()
	if len(entries) != 2 || entries[0].Name != "orbit" || entries[1].Name != "kobo" {
		t.Fatalf("enabled entries after the save: %+v", entries)
	}
}

func TestUpdateMirrorsKeepsTheFilesPermissions(t *testing.T) {
	path := writeMirrorConfig(t, "[content]\ncache_dir = \"covers\"\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMirrors(path, setLegacy(workingEntry("orbit"))); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("permissions became %04o, want 0640", got)
	}
}

// TestUpdateMirrorsFollowsASymlink. A deployment whose configuration is
// mounted or linked into place has the settings file somewhere else; an
// atomic rename over the link would leave the real file untouched and the
// next start reading the old settings.
func TestUpdateMirrorsFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.toml")
	if err := os.WriteFile(target, []byte("[content]\ncache_dir = \"covers\"\n"), 0o600); err != nil {
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
	loaded, err := Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.Name != "orbit" {
		t.Fatalf("the file the link names was not written: %+v", loaded.Mirror)
	}
}

// TestUpdateMirrorsRefusesToCorruptAMultiLineString. The section scan is
// line-based, so a "[mirror]" that is only text inside a multi-line string
// looks exactly like a table header to it. The proof run before the write
// is what turns that into a refusal instead of a mangled file.
func TestUpdateMirrorsRefusesToCorruptAMultiLineString(t *testing.T) {
	original := "[content]\ncache_dir = \"\"\"covers\n" +
		"[mirror]\nnot a table\n\"\"\"\n"
	path := writeMirrorConfig(t, original)
	err := UpdateMirrors(path, setLegacy(workingEntry("orbit")))
	if err == nil {
		t.Fatalf("a [mirror] inside a multi-line string was rewritten:\n%s", readFile(t, path))
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("the file was changed anyway:\n%s", got)
	}
}

// TestUpdateMirrorsRepairsABrokenTable. A file somebody hand-edited into
// not parsing is the one case where the single entry can still be written:
// it is replaced whole, and the rest of the file is not proven because
// there is nothing to prove it against. Without this an operator is left
// with a file the server refuses to start on and no way to fix it from the
// page.
func TestUpdateMirrorsRepairsABrokenTable(t *testing.T) {
	path := writeMirrorConfig(t, "listen_addr = \"127.0.0.1:8585\"\ninsecure_http = true\n\n"+
		"[mirror]\nenabled = true\nprotocol = \"kosync\"\nremote_key = \"unterminated\n")
	m := workingEntry("orbit")
	m.BaseURL = "https://orbit.example/sync"
	if err := UpdateMirrors(path, setLegacy(m)); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirror.Name != "orbit" || loaded.Mirror.RemoteKey == "" {
		t.Fatalf("the table was not repaired: %+v", loaded.Mirror)
	}
	if loaded.ListenAddr != "127.0.0.1:8585" || !loaded.InsecureHTTP {
		t.Fatalf("repairing the table disturbed the rest: %+v", loaded)
	}
}

func TestUpdateMirrorsRepairsABrokenTableBesideAValidList(t *testing.T) {
	path := writeMirrorConfig(t, "listen_addr = \"127.0.0.1:8585\"\ninsecure_http = true\n\n"+
		"[[mirrors]]\nenabled = false\nname = \"kobo\"\nbase_url = \"https://kobo.example.com/sync\"\n\n"+
		"[mirror]\nenabled = true\nprotocol = \"kosync\"\nremote_key = \"unterminated\n")
	m := workingEntry("orbit")
	m.BaseURL = "https://orbit.example/sync"
	if err := UpdateMirrors(path, setLegacy(m)); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("the repaired file does not load: %v", err)
	}
	if loaded.Mirror.Name != "orbit" || loaded.Mirror.RemoteKey == "" {
		t.Fatalf("the table was not repaired: %+v", loaded.Mirror)
	}
	if len(loaded.Mirrors) != 1 || loaded.Mirrors[0].Name != "kobo" {
		t.Fatalf("repairing the table lost the list: %+v", loaded.Mirrors)
	}
}

// TestUpdateMirrorsRefusesARepairThatDuplicatesAPreservedEntry. The
// [mirror] table is repaired while the list's bytes are left alone, so the
// entry being written is proved against a set the broken file could not
// hand back: the rendered file has to be held to the rules startup applies,
// or a repair reports success and leaves a file the next Load refuses.
func TestUpdateMirrorsRefusesARepairThatDuplicatesAPreservedEntry(t *testing.T) {
	original := "insecure_http = true\n\n" +
		"[[mirrors]]\nenabled = true\nname = \"bob\"\n" +
		"base_url = \"https://orbit.example/sync\"\naccount = \"bob\"\n" +
		"remote_user = \"reader\"\nremote_key = \"fedcba9876543210fedcba9876543210\"\n" +
		"device_id = \"liseur-sync\"\n\n" +
		"[mirror]\nenabled = true\nprotocol = \"kosync\"\nremote_key = \"unterminated\n"
	path := writeMirrorConfig(t, original)
	alice := workingEntry("alice")
	alice.BaseURL = "https://orbit.example/sync"
	err := UpdateMirrors(path, setLegacy(alice))
	if err == nil {
		t.Fatalf("a repair wrote a file the next start refuses:\n%s", readFile(t, path))
	}
	if !strings.Contains(err.Error(), "same account on the same peer") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("the refused repair changed the file:\n%s", got)
	}
}

// TestUpdateMirrorsAcceptsWhatTheEnvironmentAllows. LISEUR_INSECURE_HTTP
// is part of what an HTTP peer address is held against, so a save is proved
// under the whole environment startup reads, not the mirror variables alone:
// the file says nothing here, and an unrelated edit to its HTTP entry must
// still succeed and load back as written.
func TestUpdateMirrorsAcceptsWhatTheEnvironmentAllows(t *testing.T) {
	t.Setenv("LISEUR_INSECURE_HTTP", "true")
	path := writeMirrorConfig(t, "[[mirrors]]\nenabled = true\nname = \"lan\"\n"+
		"protocol = \"kosync\"\nbase_url = \"http://192.0.2.10:8080/sync\"\n"+
		"account = \"alice\"\nremote_user = \"reader\"\n"+
		"remote_key = \"0123456789abcdef0123456789abcdef\"\n"+
		"device_id = \"liseur-sync\"\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("the file does not load with the environment set: %v", err)
	}
	err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.List[0].PollInterval = Duration(10 * time.Minute)
		return nil
	})
	if err != nil {
		t.Fatalf("an unrelated edit to an HTTP entry was refused: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("the saved file does not load: %v", err)
	}
	if len(loaded.Mirrors) != 1 ||
		loaded.Mirrors[0].BaseURL != "http://192.0.2.10:8080/sync" ||
		loaded.Mirrors[0].PollInterval != Duration(10*time.Minute) {
		t.Fatalf("the save does not agree with the next load: %+v", loaded.Mirrors)
	}
}

// TestUpdateMirrorsRefusesWhatTheEnvironmentRefuses is the other direction:
// the file allows HTTP and the environment overrides that, so a repair that
// keeps an HTTP list entry would report success on a file the next start
// rejects.
func TestUpdateMirrorsRefusesWhatTheEnvironmentRefuses(t *testing.T) {
	t.Setenv("LISEUR_INSECURE_HTTP", "false")
	original := "insecure_http = true\n\n" +
		"[[mirrors]]\nenabled = true\nname = \"lan\"\n" +
		"protocol = \"kosync\"\nbase_url = \"http://192.0.2.10:8080/sync\"\n" +
		"account = \"alice\"\nremote_user = \"reader\"\n" +
		"remote_key = \"0123456789abcdef0123456789abcdef\"\n" +
		"device_id = \"liseur-sync\"\n\n" +
		"[mirror]\nenabled = true\nprotocol = \"kosync\"\nremote_key = \"unterminated\n"
	path := writeMirrorConfig(t, original)
	err := UpdateMirrors(path, setLegacy(workingEntry("orbit")))
	if err == nil {
		t.Fatalf("a repair wrote a file the next start refuses:\n%s", readFile(t, path))
	}
	if !strings.Contains(err.Error(), "must be https unless insecure_http is set") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("the refused repair changed the file:\n%s", got)
	}
}

func TestUpdateMirrorsRefusesAListChangeOnABrokenFile(t *testing.T) {
	original := "[mirror]\nremote_key = \"unterminated\n"
	path := writeMirrorConfig(t, original)
	err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.List = append(f.List, workingEntry("kobo"))
		return nil
	})
	if err == nil {
		t.Fatal("a list was rewritten into a file that does not parse")
	}
	if !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("the broken file was changed anyway:\n%s", got)
	}
}

func TestUpdateMirrorsWritesAListIntoANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	kobo := workingEntry("kobo")
	kobo.BaseURL = "https://kobo.example.com/sync"
	if err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.List = []MirrorConfig{workingEntry("orbit"), kobo}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.HasPrefix(text, "[[mirrors]]\n") {
		t.Fatalf("a fresh list file does not start at its first entry:\n%q", text)
	}
	if strings.Count(text, "[[mirrors]]") != 2 {
		t.Fatalf("list entries were not written one section each:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MirrorTableDefined() {
		t.Fatal("a file with no [mirror] table reported one")
	}
	if len(loaded.Mirrors) != 2 || loaded.Mirrors[1].Name != "kobo" {
		t.Fatalf("list entries did not load: %+v", loaded.Mirrors)
	}
}

func TestUpdateMirrorsHandlesCommentedHeaders(t *testing.T) {
	path := writeMirrorConfig(t, "listen_addr = \"127.0.0.1:8585\"\n\n"+
		"[mirror] # peer settings\nenabled = false\nremote_key = \"old\"\n\n"+
		"[content] # library\ncache_dir = \"covers\"\n")
	if err := UpdateMirrors(path, setLegacy(workingEntry("orbit"))); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if strings.Count(text, "[mirror]") != 1 {
		t.Fatalf("commented [mirror] header was not recognized, section duplicated:\n%s", text)
	}
	if !strings.Contains(text, "cache_dir = \"covers\"") {
		t.Fatalf("commented [content] header was swallowed, settings lost:\n%s", text)
	}
	if strings.Contains(text, "remote_key = \"old\"") {
		t.Fatalf("old mirror section was not replaced:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("rewritten config does not load: %v\n%s", err, text)
	}
	if loaded.Mirror.Account != "alice" || loaded.Content.CacheDir != "covers" {
		t.Fatalf("saved config did not round-trip: %+v / %+v", loaded.Mirror, loaded.Content)
	}
}

func TestUpdateMirrorsKeepsABookOrbitPeer(t *testing.T) {
	path := writeMirrorConfig(t, "listen_addr = \"127.0.0.1:8585\"\n")
	m := MirrorConfig{
		Enabled:              true,
		Protocol:             ProtocolBookOrbit,
		Name:                 "orbit",
		BaseURL:              "https://orbit.example.com/api/v1",
		Account:              "you",
		RemoteUser:           "you",
		RemotePassword:       "hunter2",
		DeviceID:             "liseur-sync",
		PeerPathPrefix:       "/books",
		LocalPathPrefix:      "/srv/library",
		PollInterval:         Duration(5 * time.Minute),
		ResolveRetryInterval: Duration(10 * time.Minute),
		ActiveDays:           30,
		Timeout:              Duration(20 * time.Second),
	}
	if err := UpdateMirrors(path, setLegacy(m)); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Mirror
	if got.Protocol != ProtocolBookOrbit || got.RemotePassword != "hunter2" {
		t.Fatalf("the credential did not survive a save: %+v", got)
	}
	if got.PeerPathPrefix != "/books" || got.LocalPathPrefix != "/srv/library" {
		t.Fatalf("the path mapping did not survive a save: %+v", got)
	}
	if got.ResolveRetryInterval.Duration() != 10*time.Minute {
		t.Fatalf("the resolve retry interval did not survive a save: %+v", got)
	}
	if cfg.ListenAddr != "127.0.0.1:8585" {
		t.Fatalf("saving the mirror disturbed the rest of the file: %q", cfg.ListenAddr)
	}
}

// TestReadMirrorsAppliesTheDefaultsAWorkerWould. The page shows what the
// server would do, and what it writes back leaves nothing implied, so an
// entry that never named a poll interval still comes back with one.
func TestReadMirrorsAppliesTheDefaultsAWorkerWould(t *testing.T) {
	path := writeMirrorConfig(t, "[mirror]\nenabled = true\nremote_key = \"k\"\n\n"+
		"[[mirrors]]\nname = \"kobo\"\nbase_url = \"https://kobo.example/sync\"\n")
	file, err := ReadMirrors(path)
	if err != nil {
		t.Fatal(err)
	}
	if !file.HasLegacy {
		t.Fatal("the [mirror] table was not seen")
	}
	if file.Legacy.Name != "peer" || file.Legacy.Protocol != ProtocolKosync {
		t.Fatalf("single entry defaults: %+v", file.Legacy)
	}
	if len(file.List) != 1 {
		t.Fatalf("list: %+v", file.List)
	}
	entry := file.List[0]
	if entry.PollInterval.Duration() != 5*time.Minute ||
		entry.ResolveRetryInterval.Duration() != 24*time.Hour ||
		entry.ActiveDays != 30 || entry.Timeout.Duration() != 20*time.Second ||
		entry.DeviceID != "liseur-sync" || entry.Protocol != ProtocolKosync {
		t.Fatalf("list entry defaults: %+v", entry)
	}
}

// TestUpdateMirrorsSeesWhatIsOnDiskNow. The page reads the file, and a
// save lands later; another administrator's entry written in between must
// not be thrown away by the second save.
func TestUpdateMirrorsSeesWhatIsOnDiskNow(t *testing.T) {
	path := writeMirrorConfig(t, "[content]\ncache_dir = \"covers\"\n")
	other := workingEntry("kobo")
	other.BaseURL = "https://kobo.example/sync"
	if _, err := ReadMirrors(path); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.List = append(f.List, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := workingEntry("orbit")
	if err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.Legacy, f.HasLegacy = m, true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Mirrors) != 1 || loaded.Mirrors[0].Name != "kobo" {
		t.Fatalf("the other entry was lost: %+v", loaded.Mirrors)
	}
	if loaded.Mirror.Name != "orbit" {
		t.Fatalf("the second save did not land: %+v", loaded.Mirror)
	}
}

// TestUpdateMirrorsRemovesTheSingleEntry. Turning mirroring off is an
// empty entry, not a disabled one: what is left is a file that says
// nothing about peers.
func TestUpdateMirrorsRemovesTheSingleEntry(t *testing.T) {
	path := writeMirrorConfig(t, "[content]\ncache_dir = \"covers\"\n\n"+
		"[mirror]\nenabled = true\nname = \"orbit\"\nremote_key = \"k\"\n")
	if err := UpdateMirrors(path, func(f *MirrorFile) error {
		f.Legacy, f.HasLegacy = MirrorConfig{}, false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if strings.Contains(text, "[mirror]") {
		t.Fatalf("the table was not removed:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MirrorTableDefined() || loaded.Mirror.Name != "peer" {
		t.Fatalf("a removed entry is still there: %+v", loaded.Mirror)
	}
	if len(loaded.EnabledMirrors()) != 0 {
		t.Fatalf("a removed entry still starts workers: %+v", loaded.EnabledMirrors())
	}
}

func TestUpdateMirrorsAtAnEmptyPathIsRefused(t *testing.T) {
	err := UpdateMirrors("", func(*MirrorFile) error { return nil })
	if err == nil {
		t.Fatal("a server started without a config file wrote one")
	}
	if !strings.Contains(err.Error(), "writable config file") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}
