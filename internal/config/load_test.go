package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "liseur-sync.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestShippedExampleParsesIntoTheSettingsItNames is the regression test
// for a config file that looked correct and did nothing: every top-level
// key in the example sat below a [table] header, so TOML bound it to that
// table. Setting insecure_http = true in the shipped example left the
// server refusing plain HTTP with no indication why.
func TestShippedExampleParsesIntoTheSettingsItNames(t *testing.T) {
	body, err := os.ReadFile("../../liseur-sync.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	// Flip every top-level default so a key bound to the wrong table
	// cannot pass by coincidentally matching the default.
	text := string(body)
	for from, to := range map[string]string{
		"insecure_http = false":                                   "insecure_http = true",
		"open_registration = false":                               "open_registration = true",
		"pairing_code_ttl_min = 15":                               "pairing_code_ttl_min = 42",
		`# trusted_proxies = ["10.0.0.0/8", "192.168.0.0/16"]`:    `trusted_proxies = ["10.0.0.0/8"]`,
		`# cors_allowed_origins = ["https://reader.example.com"]`: `cors_allowed_origins = ["https://reader.example.com"]`,
	} {
		if !strings.Contains(text, from) {
			t.Fatalf("example config no longer contains %q", from)
		}
		text = strings.Replace(text, from, to, 1)
	}
	cfg, err := Load(writeConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureHTTP {
		t.Fatal("insecure_http did not reach the top-level setting")
	}
	if !cfg.OpenRegistration {
		t.Fatal("open_registration did not reach the top-level setting")
	}
	if cfg.PairingCodeTTLMin != 42 {
		t.Fatalf("pairing_code_ttl_min: got %d", cfg.PairingCodeTTLMin)
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("trusted_proxies: got %v", cfg.TrustedProxies)
	}
	if len(cfg.CORSAllowedOrigins) != 1 {
		t.Fatalf("cors_allowed_origins: got %v", cfg.CORSAllowedOrigins)
	}
}

// TestUnknownKeyIsAStartupError keeps the silent-ignore behavior from
// coming back. A security setting that is read but not applied is worse
// than one that is rejected.
func TestUnknownKeyIsAStartupError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"top-level key below a table header",
			"[content]\nroot = \"c\"\ninsecure_http = true\n",
			"top-level setting",
		},
		{
			"misspelled key",
			"insecure_htp = true\n",
			"insecure_htp",
		},
		{
			"unknown table",
			"[nonsense]\nvalue = 1\n",
			"nonsense.value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("unknown key accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestKnownKeysAreNotReportedAsUnknown(t *testing.T) {
	cfg, err := Load(writeConfig(t,
		"insecure_http = true\n[content]\nroot = \"c\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureHTTP || cfg.Content.Root != "c" {
		t.Fatalf("valid config not applied: %+v", cfg)
	}
}

// TestTopLevelKeysMatchTheExampleFile pins the derived set against an
// independent source, so neither the derivation nor the example can drift
// alone. Anything the example writes above its first [table] header is a
// top-level key by definition.
func TestTopLevelKeysMatchTheExampleFile(t *testing.T) {
	body, err := os.ReadFile("../../liseur-sync.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	fromExample := map[string]bool{}
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		// Commented-out optional settings still name a real key.
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key != "" && !strings.Contains(key, " ") {
			fromExample[key] = true
		}
	}
	if len(fromExample) == 0 {
		t.Fatal("example config has no top-level keys to compare against")
	}
	for key := range fromExample {
		if !topLevelKeys[key] {
			t.Errorf("%q is top-level in the example but not derived from Config", key)
		}
	}
	for key := range topLevelKeys {
		if !fromExample[key] {
			t.Errorf("%q is a top-level setting but the example does not document it", key)
		}
	}
}

// TestEveryTopLevelKeyIsNamedInTheHint keeps the diagnostic honest: a new
// top-level setting that is not listed would be misplaced just as easily
// but reported without the explanation.
func TestEveryTopLevelKeyIsNamedInTheHint(t *testing.T) {
	for key := range topLevelKeys {
		body := "[content]\nroot = \"c\"\n" + key + " = 1\n"
		_, err := Load(writeConfig(t, body))
		if err == nil {
			t.Fatalf("%s: misplaced key accepted", key)
		}
		if !strings.Contains(err.Error(), "top-level setting") {
			t.Fatalf("%s: no hint in %q", key, err)
		}
	}
}
