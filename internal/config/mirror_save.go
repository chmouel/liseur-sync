package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// MirrorFile is the mirror configuration one config file holds: the
// single [mirror] table an older deployment has, the [[mirrors]] list a
// newer one adds, and whether the table is there at all.
//
// The distinction matters. A file with no [mirror] table has no entry —
// nothing to show and no name to keep unique — while the struct beside
// it is pre-filled with the defaults an entry would start from.
type MirrorFile struct {
	Legacy    MirrorConfig   `toml:"mirror"`
	HasLegacy bool           `toml:"-"`
	List      []MirrorConfig `toml:"mirrors"`
}

// Entry returns the list entry called name.
func (f MirrorFile) Entry(name string) (MirrorConfig, bool) {
	for _, m := range f.List {
		if m.Name == name {
			return m, true
		}
	}
	return MirrorConfig{}, false
}

// LegacyEntry returns the single [mirror] entry when the file has one.
func (f MirrorFile) LegacyEntry() (MirrorConfig, bool) {
	if !f.HasLegacy {
		return MirrorConfig{}, false
	}
	return f.Legacy, true
}

// WithEnvironment applies the LISEUR_MIRROR_* overlay to these entries the
// way startup does, and returns the set a start would actually run. The file
// values are left alone: this is the view and the candidate to validate, not
// what a save writes. An entry the environment supplies with no [mirror]
// table of its own appears here, which is what makes it visible — and
// read-only — on the page.
func (f MirrorFile) WithEnvironment() MirrorFile {
	var c Config
	c.Mirror = f.Legacy
	c.Mirrors = slices.Clone(f.List)
	c.mirrorDefined = f.HasLegacy
	c.ApplyMirrorEnvironment()
	return MirrorFile{Legacy: c.Mirror, HasLegacy: c.mirrorDefined, List: c.Mirrors}
}

// configWriteMu serialises the read-modify-write of the config file. Two
// administrator tabs saving different sections would otherwise each
// rewrite a body they read before the other wrote, and one of the two
// changes would vanish.
var configWriteMu sync.Mutex

// ReadMirrors reads the mirror configuration out of the file at path,
// without applying environment overrides. The admin page needs that
// distinction: a credential supplied by the environment may be used to
// test a connection without being copied into the file on the next save.
//
// Every entry comes back with the defaults an omitted setting would take,
// so what the page shows is what the server would do, and what the page
// writes back leaves nothing implied.
func ReadMirrors(path string) (MirrorFile, error) {
	out := MirrorFile{Legacy: Default().Mirror}
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
	meta, err := toml.Decode(string(body), &out)
	if err != nil {
		return out, safeConfigParseError("the configuration file does not parse", err)
	}
	out.HasLegacy = tableDefined(meta, "mirror")
	for i := range out.List {
		applyMirrorDefaults(&out.List[i])
	}
	return out, nil
}

// UpdateMirrors rewrites the mirror parts of the file at path with
// whatever apply leaves in the MirrorFile it is handed, and nothing else.
//
// The file is read again under the write lock, so apply sees what is on
// disk now rather than what a caller read a moment ago. A section that did
// not change is left byte-for-byte, which is what keeps an operator's
// comments, ordering and unrelated settings intact.
//
// The rewrite is proved before it replaces anything: the original and the
// result are both decoded, their mirror fields cleared and the rest
// required to be identical. A table header inside a multi-line string is
// the case that makes this necessary — the line-based scan that finds
// sections cannot tell it from a real one, and the proof is what turns a
// corrupted file into a refusal.
func UpdateMirrors(path string, apply func(*MirrorFile) error) error {
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

	current, decodeErr := ReadMirrors(target)
	// The caller is handed a copy of the list, not the list itself: what
	// it edits is what gets written, and the value it started from is
	// what says whether anything changed.
	want := current
	want.List = slices.Clone(current.List)
	if decodeErr != nil {
		// A file that does not parse can still be repaired, but only by
		// replacing the single table: the list is unknown, so a change to
		// it would have to guess at what it held. The list's own bytes
		// are left untouched by the repair and proved by the result
		// parsing whole.
		current.HasLegacy = hasTable(before, "mirror", false)
		current.List = nil
		want = current
		want.List = slices.Clone(current.List)
	}
	if err := apply(&want); err != nil {
		return err
	}
	if decodeErr != nil {
		if len(want.List) != 0 {
			return fmt.Errorf(
				"the configuration file does not parse (%w); only the [mirror] entry can be rewritten until it does", decodeErr)
		}
	}

	rendered := before
	// A file that does not parse is rewritten whether or not the entry the
	// caller handed back differs from what the file decoded to: that decode
	// failed part-way, so the two can look alike while the file is still
	// broken, and a save that changed nothing would report success over it.
	if decodeErr != nil || !sameLegacy(want, current) {
		var blocks []string
		if want.HasLegacy {
			blocks = mirrorTableBlocks(want.Legacy)
		}
		rendered = rewriteTable(rendered, "mirror", false, blocks)
	}
	if !sameEntries(want.List, current.List) {
		rendered = rewriteTable(rendered, "mirrors", true, mirrorListBlocks(want.List))
	}
	if rendered == before {
		if decodeErr != nil {
			// Rewriting the table left the bytes alone, so the file is
			// broken somewhere the repair cannot reach and nothing can
			// prove it would start.
			return fmt.Errorf(
				"the configuration file does not parse (%w); only the [mirror] entry can be rewritten until it does", decodeErr)
		}
		return nil
	}
	if err := verifyMirrorRewrite(before, rendered, want, decodeErr == nil); err != nil {
		return err
	}
	if err := writeConfigAtomically(target, []byte(rendered)); err != nil {
		return err
	}
	return nil
}

// sameLegacy compares the single entry, treating a removed table as one
// thing whatever the struct beside it holds: what a caller leaves there
// after removing the entry is not a setting.
func sameLegacy(a, b MirrorFile) bool {
	if a.HasLegacy != b.HasLegacy {
		return false
	}
	if !a.HasLegacy {
		return true
	}
	return sameEntry(a.Legacy, b.Legacy)
}

// sameEntry compares two mirror entries by what a config file holds.
// The address an earlier build folded is remembered from the file this
// process read and is never written back, so it is not part of what a
// rendered file can be held to.
func sameEntry(a, b MirrorConfig) bool {
	a.legacyBaseURL, b.legacyBaseURL = "", ""
	return a == b
}

// sameEntries compares a list of entries, order included.
func sameEntries(a, b []MirrorConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameEntry(a[i], b[i]) {
			return false
		}
	}
	return true
}

// verifyMirrorRewrite is the proof UpdateMirrors demands before it
// replaces an operator's file: the result parses, it holds no key this
// server does not know, it decodes to the mirror configuration that was
// meant, the mirror set it now holds is one startup would accept, and
// everything outside the mirrors is exactly what it was. The list is only
// compared field by field when the original parsed: repairing a broken
// [mirror] table leaves the [[mirrors]] bytes alone, so there is nothing
// decoded to hold them against, and reading the rendered file whole is what
// proves they survived — and that they do not collide with the repaired
// entry.
func verifyMirrorRewrite(before, rendered string, want MirrorFile, compareRest bool) error {
	got := MirrorFile{Legacy: Default().Mirror}
	meta, err := toml.Decode(rendered, &got)
	if err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	got.HasLegacy = tableDefined(meta, "mirror")
	for i := range got.List {
		applyMirrorDefaults(&got.List[i])
	}
	if !sameLegacy(got, want) {
		return errors.New("rewritten config does not hold the mirror entry it was given")
	}
	if compareRest && !sameEntries(got.List, want.List) {
		return errors.New("rewritten config does not hold the mirror entries it was given")
	}

	// The same text decoded the way startup reads it, for the two things
	// only the whole configuration can answer: that no key became
	// unknown, and that nothing outside the mirrors moved.
	var updated Config
	meta, err = toml.Decode(rendered, &updated)
	if err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return fmt.Errorf("rewritten config carries unknown key(s): %s",
			strings.Join(keys, ", "))
	}
	if err := validateRenderedMirrors(rendered); err != nil {
		return err
	}
	if !compareRest {
		return nil
	}
	var original Config
	if _, err := toml.Decode(before, &original); err != nil {
		return safeConfigParseError("original config does not parse", err)
	}
	original.Mirror, original.Mirrors = MirrorConfig{}, nil
	updated.Mirror, updated.Mirrors = MirrorConfig{}, nil
	if !reflect.DeepEqual(original, updated) {
		return errors.New("rewriting the mirrors would change other settings; nothing was saved")
	}
	return nil
}

// validateRenderedMirrors holds the result to the mirror rules the next
// start applies: the defaults an omitted setting takes, whether the [mirror]
// table is there, the environment overlay and the set rules. A broken
// file hands the caller no list to validate against, so only the whole
// rendered text can show that repairing the table did not leave a
// configuration startup would refuse — two enabled entries reaching one peer
// account, say — and a save that reports success must not write one.
func validateRenderedMirrors(rendered string) error {
	c := Default()
	meta, err := toml.Decode(rendered, &c)
	if err != nil {
		return safeConfigParseError("rewritten config would not parse", err)
	}
	c.mirrorDefined = tableDefined(meta, "mirror")
	// The whole environment, not just the mirror variables: what an HTTP
	// peer address is held against (LISEUR_INSECURE_HTTP) is part of it.
	c.applyEnv()
	if err := c.validateMirror(); err != nil {
		return fmt.Errorf("the rewritten config would not start: %w; nothing was saved", err)
	}
	return nil
}

// resolveConfigPath follows a symlinked config file to the file it names.
// Replacing the link itself would leave a deployment whose config lives
// in a mounted volume pointing at nothing.
func resolveConfigPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return path, nil
	}
	return "", fmt.Errorf("resolve config path: %w", err)
}

// writeConfigAtomically replaces a file in one step, keeping the
// permissions it had. A new file is written 0600: it may hold a peer
// password.
func writeConfigAtomically(path string, body []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dir, file := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+file+".")
	if err != nil {
		return fmt.Errorf("create config temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("protect config temporary file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// rewriteTable replaces every block named name (a table, or an array of
// tables when array is true) with blocks, in the place the first one
// occupied. Nothing else in the body moves, so a section nobody edited
// keeps its bytes, its comments and its position.
func rewriteTable(body, name string, array bool, blocks []string) string {
	lines := splitLines(body)
	out := make([]string, 0, len(lines)+len(blocks))
	placed := false
	for i := 0; i < len(lines); i++ {
		if table, ok := tableHeaderName(trimmedLine(lines[i])); ok &&
			strings.EqualFold(table, name) && isArrayHeader(lines[i]) == array {
			for i++; i < len(lines); i++ {
				if _, ok := tableHeaderName(trimmedLine(lines[i])); ok {
					i--
					break
				}
			}
			if !placed {
				out = append(out, blocks...)
				placed = true
			}
			continue
		}
		out = append(out, lines[i])
	}
	if !placed && len(blocks) > 0 {
		if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) != "" {
			out = append(out, "\n")
		}
		out = append(out, blocks...)
	}
	return strings.Join(out, "")
}

// hasTable reports whether body carries a section named name.
func hasTable(body, name string, array bool) bool {
	for _, line := range splitLines(body) {
		if table, ok := tableHeaderName(trimmedLine(line)); ok &&
			strings.EqualFold(table, name) && isArrayHeader(line) == array {
			return true
		}
	}
	return false
}

func splitLines(body string) []string {
	lines := strings.SplitAfter(body, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

func trimmedLine(line string) string {
	return strings.TrimSpace(strings.TrimSuffix(line, "\n"))
}

func isArrayHeader(line string) bool {
	return strings.HasPrefix(trimmedLine(line), "[[")
}

// mirrorTableBlocks is the single entry as a file section. The trailing
// empty line is what separates it from whatever follows.
func mirrorTableBlocks(m MirrorConfig) []string {
	lines := append([]string{"[mirror]\n"}, mirrorEntryFields(m)...)
	return append(lines, "\n")
}

// mirrorListBlocks writes the list, one section each. Comments inside a
// [[mirrors]] section are not preserved: the section is rewritten whole,
// because a field the page switched off has to stop existing rather than
// linger with an old value.
func mirrorListBlocks(list []MirrorConfig) []string {
	var out []string
	for _, m := range list {
		out = append(out, "[[mirrors]]\n")
		out = append(out, mirrorEntryFields(m)...)
		out = append(out, "\n")
	}
	return out
}

func mirrorEntryFields(m MirrorConfig) []string {
	return []string{
		fmt.Sprintf("enabled = %t\n", m.Enabled),
		fmt.Sprintf("protocol = %q\n", m.Protocol),
		fmt.Sprintf("name = %q\n", m.Name),
		fmt.Sprintf("base_url = %q\n", m.BaseURL),
		fmt.Sprintf("account = %q\n", m.Account),
		fmt.Sprintf("remote_user = %q\n", m.RemoteUser),
		fmt.Sprintf("remote_key = %q\n", m.RemoteKey),
		fmt.Sprintf("remote_password = %q\n", m.RemotePassword),
		fmt.Sprintf("device_id = %q\n", m.DeviceID),
		fmt.Sprintf("peer_path_prefix = %q\n", m.PeerPathPrefix),
		fmt.Sprintf("local_path_prefix = %q\n", m.LocalPathPrefix),
		fmt.Sprintf("poll_interval = %q\n", writtenDuration(m.PollInterval)),
		fmt.Sprintf("resolve_retry_interval = %q\n", writtenDuration(m.ResolveRetryInterval)),
		fmt.Sprintf("active_days = %d\n", m.ActiveDays),
		fmt.Sprintf("timeout = %q\n", writtenDuration(m.Timeout)),
	}
}

// tableHeaderName reports the table name a TOML header line names, if
// it is one. It strips a trailing inline comment first, so `[mirror] #
// peer settings` is recognized exactly like `[mirror]` — both by the
// scan that finds where the section starts and by the scan that finds
// where it ends. Without that, a commented header is invisible to the
// first and swallows everything after it in the second.
func tableHeaderName(line string) (string, bool) {
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") && len(line) >= 4 {
		return strings.TrimSpace(line[2 : len(line)-2]), true
	}
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && len(line) >= 2 {
		return strings.TrimSpace(line[1 : len(line)-1]), true
	}
	return "", false
}

// writtenDuration spells a duration the way the file's other durations
// are spelled. time.Duration's own String writes five minutes as
// "5m0s", which parses back perfectly well and reads like a machine
// wrote it — this is a file an operator edits by hand.
func writtenDuration(d Duration) string {
	value := d.Duration()
	switch {
	case value == 0:
		return "0s"
	case value%time.Hour == 0:
		return fmt.Sprintf("%dh", value/time.Hour)
	case value%time.Minute == 0:
		return fmt.Sprintf("%dm", value/time.Minute)
	case value%time.Second == 0:
		return fmt.Sprintf("%ds", value/time.Second)
	}
	return value.String()
}
