package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SaveMirror replaces the [mirror] table while leaving the rest of the
// operator's config file untouched. The admin UI uses this so configuring a
// mirror does not rewrite unrelated settings or comments.
func SaveMirror(path string, m MirrorConfig) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("the server was not started with a writable config file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config: %w", err)
		}
		body = nil
	}
	lines := strings.SplitAfter(string(body), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	kept := make([]string, 0, len(lines)+12)
	removed := false
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(strings.TrimSuffix(lines[i], "\n"))
		if name, ok := tableHeaderName(trimmed); ok && name == "mirror" {
			removed = true
			for i++; i < len(lines); i++ {
				next := strings.TrimSpace(strings.TrimSuffix(lines[i], "\n"))
				if _, ok := tableHeaderName(next); ok {
					i--
					break
				}
			}
			continue
		}
		kept = append(kept, lines[i])
	}
	if len(kept) > 0 && !strings.HasSuffix(kept[len(kept)-1], "\n") {
		kept = append(kept, "\n")
	} else if removed && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) != "" {
		kept = append(kept, "\n")
	}
	kept = append(kept,
		"[mirror]\n",
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
	)
	rendered := strings.Join(kept, "")
	// A header the loop above misjudged, or any other mistake in the
	// rewrite, must not reach disk: parse the result the same way Load
	// does before it replaces the operator's file.
	var scratch Config
	if _, err := toml.Decode(rendered, &scratch); err != nil {
		return fmt.Errorf("rewritten config would not parse: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".liseur-sync-config-")
	if err != nil {
		return fmt.Errorf("create config temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect config temporary file: %w", err)
	}
	if _, err := tmp.WriteString(rendered); err != nil {
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

// MirrorFromFile reads only the config file's mirror table, without
// applying environment overrides. The admin UI needs that distinction
// so a credential supplied by LISEUR_MIRROR_REMOTE_KEY or
// LISEUR_MIRROR_REMOTE_PASSWORD can be used for a connection test
// without being copied back into the TOML file on the next save.
func MirrorFromFile(path string) (MirrorConfig, bool, error) {
	c := Default()
	if strings.TrimSpace(path) == "" {
		return c.Mirror, false, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Mirror, false, nil
		}
		return c.Mirror, false, fmt.Errorf("read config: %w", err)
	}
	hasMirror := false
	for _, line := range strings.Split(string(body), "\n") {
		if name, ok := tableHeaderName(strings.TrimSpace(line)); ok && name == "mirror" {
			hasMirror = true
			break
		}
	}
	if _, err := toml.Decode(string(body), &c); err != nil {
		return c.Mirror, hasMirror, err
	}
	return c.Mirror, hasMirror, nil
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
