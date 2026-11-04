package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads the TOML file at path (if it exists) over the defaults,
// then applies env overrides, then validates.
//
// A key the decoder did not recognize is a startup error rather than a
// warning. TOML binds a bare key to the table above it, so writing
// insecure_http below [content] silently produces content.insecure_http
// and leaves the real setting at its default — the operator asked for
// something and got the opposite with no indication. Refusing to start is
// the only outcome that cannot be missed.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			meta, err := toml.DecodeFile(path, &c)
			if err != nil {
				return c, err
			}
			if err := unknownKeyError(path, meta.Undecoded()); err != nil {
				return c, err
			}
			// The single entry only exists when the file says so. Its
			// default name would otherwise look like a peer nobody
			// configured, and would refuse a list entry the same name.
			c.mirrorDefined = tableDefined(meta, "mirror")
		} else if !os.IsNotExist(err) {
			return c, err
		}
	}
	if err := legacySTTEnvError(); err != nil {
		return c, err
	}
	c.applyEnv()
	return c, c.Validate()
}

// movedSTTKeys maps the speech-to-text settings that used to sit under
// [audiobookshelf] to where they live now. They are refused rather than
// read: a key that silently stopped loading would just turn
// transcription off.
var movedSTTKeys = map[string]string{
	"audiobookshelf.stt_url":     "speech_to_text.url",
	"audiobookshelf.stt_api_key": "speech_to_text.api_key",
	"audiobookshelf.stt_model":   "speech_to_text.model",
}

var movedSTTEnv = map[string]string{
	"LISEUR_ABS_STT_URL":     "LISEUR_STT_URL",
	"LISEUR_ABS_STT_API_KEY": "LISEUR_STT_API_KEY",
	"LISEUR_ABS_STT_MODEL":   "LISEUR_STT_MODEL",
}

func legacySTTEnvError() error {
	var moved []string
	for old, now := range movedSTTEnv {
		if _, ok := os.LookupEnv(old); ok {
			moved = append(moved, old+" is now "+now)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	sort.Strings(moved)
	return fmt.Errorf("speech-to-text settings moved: %s", strings.Join(moved, ", "))
}

// movedSTTHint names the new home of any moved key among keys.
func movedSTTHint(keys []string) string {
	var moved []string
	for _, key := range keys {
		if now, ok := movedSTTKeys[strings.ToLower(key)]; ok {
			moved = append(moved, key+" is now "+now)
		}
	}
	if len(moved) == 0 {
		return ""
	}
	return "; speech-to-text settings moved to [speech_to_text]: " + strings.Join(moved, ", ")
}

// tableDefined reports whether the file carries a table named name,
// whatever case the operator wrote it in. The decoder binds a key to a
// struct field by folding case, while MetaData.IsDefined matches exactly,
// so a [Mirror] header fills the struct and looks absent at the same time.
func tableDefined(meta toml.MetaData, name string) bool {
	for _, key := range meta.Keys() {
		if len(key) > 0 && strings.EqualFold(key[0], name) {
			return true
		}
	}
	return false
}

func unknownKeyError(path string, undecoded []toml.Key) error {
	if len(undecoded) == 0 {
		return nil
	}
	keys := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	hint := movedSTTHint(keys)
	// The overwhelmingly likely cause is a top-level key written below a
	// table header, so name that case instead of leaving the operator to
	// work out why a key they can see in the example file is unknown.
	for _, key := range keys {
		if hint != "" {
			break
		}
		if base := key[strings.LastIndex(key, ".")+1:]; base != key &&
			isTopLevelKey(base) {
			hint = fmt.Sprintf(
				"; %q is a top-level setting and must appear "+
					"before the first [table] header", base)
			break
		}
	}
	return fmt.Errorf("%s: unknown configuration key(s): %s%s",
		path, strings.Join(keys, ", "), hint)
}

// topLevelKeys are the settings that live outside any table: exactly the
// ones whose position in the file decides whether they take effect. It is
// derived from the struct rather than listed, so a new top-level setting
// gets the diagnostic without anyone remembering to add it here.
var topLevelKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(Config{})
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		if name == "" || name == "-" {
			continue
		}
		if field.Type.Kind() == reflect.Struct {
			continue // a [table] header, not a bare key
		}
		keys[name] = true
	}
	return keys
}()

func isTopLevelKey(name string) bool { return topLevelKeys[name] }
