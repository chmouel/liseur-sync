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
		} else if !os.IsNotExist(err) {
			return c, err
		}
	}
	c.applyEnv()
	return c, c.Validate()
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
	hint := ""
	// The overwhelmingly likely cause is a top-level key written below a
	// table header, so name that case instead of leaving the operator to
	// work out why a key they can see in the example file is unknown.
	for _, key := range keys {
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
