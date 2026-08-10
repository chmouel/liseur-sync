package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

// Load reads the TOML file at path (if it exists) over the defaults,
// then applies env overrides, then validates.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &c); err != nil {
				return c, err
			}
		} else if !os.IsNotExist(err) {
			return c, err
		}
	}
	c.applyEnv()
	return c, c.Validate()
}
