package config

import (
	"errors"
	"fmt"
	"net/url"
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
//
// A save that writes no bytes still makes a file that already holds a
// stored credential owner-only. See writeConfigAtomically for how the mode
// of a written file is chosen.
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
		return tightenConfigCredentialMode(target, before)
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

// writeConfigAtomically replaces a file in one step. Its mode follows the
// bytes about to land: a file holding a stored credential is owner-only, a
// secret-free file keeps the mode it had, and a new secret-free file is
// owner-only too, because what a later save puts in it cannot be known.
// Bytes whose credential fields cannot be read are refused rather than
// written at a guessed mode; every caller has proved its rewrite parses
// first, so that refusal is a bug in the caller, not a file it was given.
func writeConfigAtomically(path string, body []byte) error {
	holds, err := configFileHoldsCredential(string(body))
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if !holds {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
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

// credentialProbe is the part of a config file that can carry a stored
// credential. It is written by hand rather than taken from Config, and it
// holds nothing else: a setting left out of it cannot make decoding fail,
// and whether a file holds a secret is not something a save may guess at.
type credentialProbe struct {
	Mirror         credentialMirror   `toml:"mirror"`
	Mirrors        []credentialMirror `toml:"mirrors"`
	Audiobookshelf struct {
		BaseURL   string `toml:"base_url"`
		APIKey    string `toml:"api_key"`
		STTURL    string `toml:"stt_url"`
		STTAPIKey string `toml:"stt_api_key"`
	} `toml:"audiobookshelf"`
	Database struct {
		URL string `toml:"url"`
	} `toml:"database"`
	// ReaderOrigin and CORSAllowedOrigins are origins, and neither could
	// ever work with a password in it. They are here because a file can
	// still hold one, and a file is where a secret leaks from.
	ReaderOrigin       string   `toml:"reader_origin"`
	CORSAllowedOrigins []string `toml:"cors_allowed_origins"`
}

type credentialMirror struct {
	BaseURL        string `toml:"base_url"`
	RemoteKey      string `toml:"remote_key"`
	RemotePassword string `toml:"remote_password"`
}

// configFileHoldsCredential reports whether config text about to be on
// disk carries a stored credential, which is what makes the file
// owner-only.
//
// It reads the text the way a file is read, not the way startup is: no
// environment is applied, so a secret a deployment keeps in a variable is
// never mistaken for one on disk — and never copied into a file that did
// not already hold it. Which values count follows the loader rather than a
// rule of thumb: a mirror's credential is used as written, so one that is
// only whitespace is a secret the peer is sent, while the [audiobookshelf]
// keys are trimmed before they are used, so there a blank one is nothing.
//
// The addresses count too. A disabled entry keeps an operator's address,
// validation skips its URL, and a `https://alice:hunter2@peer` left in one
// is a password on disk whether or not it is ever dialled: a mirror's
// base_url, an audiobookshelf base_url or stt_url, and a userinfo password
// in an origin. database.url is the same question in either shape libpq
// accepts, where `password` and `sslpassword` both carry a secret; a sqlite
// file path is not one.
//
// Text that does not decode tells nothing, and the answer then is an error
// rather than "no": a save that cannot read its own result is refused
// before it writes, so no mode is changed rather than a guess made at one.
func configFileHoldsCredential(rendered string) (bool, error) {
	var probe credentialProbe
	if _, err := toml.Decode(rendered, &probe); err != nil {
		return false, safeConfigParseError("the configuration file does not parse", err)
	}
	for _, m := range append([]credentialMirror{probe.Mirror}, probe.Mirrors...) {
		if m.RemoteKey != "" || m.RemotePassword != "" || urlHoldsUserPassword(m.BaseURL) {
			return true, nil
		}
	}
	abs := probe.Audiobookshelf
	if strings.TrimSpace(abs.APIKey) != "" || strings.TrimSpace(abs.STTAPIKey) != "" {
		return true, nil
	}
	addresses := []string{abs.BaseURL, abs.STTURL, probe.ReaderOrigin}
	addresses = append(addresses, probe.CORSAllowedOrigins...)
	for _, raw := range addresses {
		if urlHoldsUserPassword(raw) {
			return true, nil
		}
	}
	return dsnHoldsPassword(probe.Database.URL), nil
}

// urlHoldsUserPassword reports whether a stored address carries a password
// in its userinfo. A username alone is not a secret, an address with no
// userinfo is not one, and a value that is not an address at all is left
// to validation to refuse.
func urlHoldsUserPassword(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return false
	}
	password, ok := u.User.Password()
	return ok && password != ""
}

// dsnHoldsPassword reports whether a database.url carries a password. Both
// shapes libpq reads are handled, and the driver is not consulted: a
// password in the value is on disk whatever ends up reading it.
func dsnHoldsPassword(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		if u.User != nil {
			if password, ok := u.User.Password(); ok && password != "" {
				return true
			}
		}
		for key, values := range u.Query() {
			if !strings.EqualFold(key, "password") && !strings.EqualFold(key, "sslpassword") {
				continue
			}
			for _, value := range values {
				if value != "" {
					return true
				}
			}
		}
	}
	// A keyword/value string needs its own reader: a value may be quoted
	// and may hold anything, whitespace and `password=` included, so
	// splitting on spaces both misses `host=db password = 'x'` and reads
	// `application_name='backup password=fake'` as a credential.
	return dsnKeywordValueHoldsPassword(raw)
}

// dsnKeywordValueHoldsPassword scans the keyword/value form of a libpq
// connection string: pairs separated by whitespace, whitespace allowed
// around the '=', a value either bare up to the next whitespace or
// single-quoted with backslash escapes. Malformed text is scanned past,
// never refused: this only ever answers whether a password is in there.
func dsnKeywordValueHoldsPassword(raw string) bool {
	for i := 0; i < len(raw); {
		for i < len(raw) && isDSNSpace(raw[i]) {
			i++
		}
		if i >= len(raw) {
			return false
		}
		keyStart := i
		for i < len(raw) && !isDSNSpace(raw[i]) && raw[i] != '=' {
			i++
		}
		key := raw[keyStart:i]
		for i < len(raw) && isDSNSpace(raw[i]) {
			i++
		}
		if i >= len(raw) || raw[i] != '=' {
			// A word with no value: a trailing keyword, or one that was
			// never an assignment. The scan has moved past it.
			continue
		}
		i++
		for i < len(raw) && isDSNSpace(raw[i]) {
			i++
		}
		var value string
		value, i = dsnValueAt(raw, i)
		if value != "" && (strings.EqualFold(key, "password") || strings.EqualFold(key, "sslpassword")) {
			return true
		}
	}
	return false
}

// dsnValueAt reads one value starting at i and returns it with the index
// just past it. An unterminated quote ends at the end of the text, which
// is as far as there is anything to read.
func dsnValueAt(raw string, i int) (string, int) {
	var b strings.Builder
	for i < len(raw) && !isDSNSpace(raw[i]) {
		switch {
		case raw[i] == '\\' && i+1 < len(raw):
			b.WriteByte(raw[i+1])
			i += 2
		case raw[i] == '\\':
			// A backslash with nothing behind it.
			i++
		case raw[i] == '\'':
			i++
			for i < len(raw) && raw[i] != '\'' {
				if raw[i] == '\\' && i+1 < len(raw) {
					b.WriteByte(raw[i+1])
					i += 2
					continue
				}
				b.WriteByte(raw[i])
				i++
			}
			if i < len(raw) {
				i++
			}
		default:
			b.WriteByte(raw[i])
			i++
		}
	}
	return b.String(), i
}

func isDSNSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// tightenConfigCredentialMode makes a config file that already holds a
// stored credential owner-only, for a save that writes no bytes. A no-op is
// an accepted save, and leaving a secret the file already held readable by
// others across one is how this promise would break without anybody
// noticing. A secret-free file is left as it is.
//
// A body whose credential fields cannot be read is refused: an unreadable
// file is not a secret-free one, and a no-op save must not report success
// over a file nobody can tell the contents of.
//
// It runs under the same lock as the read-modify-write it belongs to, so a
// concurrent save cannot land between the read that found the secret and
// the chmod that protects it.
func tightenConfigCredentialMode(path, body string) error {
	holds, err := configFileHoldsCredential(body)
	if err != nil {
		// What the file holds cannot be read, so whether a secret is in
		// it is unknown. Reporting success over that is how a save would
		// leave one readable.
		return fmt.Errorf("%w; whether it holds a stored credential could not be read, so nothing was saved", err)
	}
	if !holds {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config permissions: %w", err)
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect config file: %w", err)
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
