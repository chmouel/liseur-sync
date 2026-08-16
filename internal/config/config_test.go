package config

import (
	"testing"
)

func TestDefaultEnablesCompaction(t *testing.T) {
	if !Default().Ops.CompactionEnabled {
		t.Fatal("op compaction must be enabled by default")
	}
}

func TestInferenceLatenessMustCoverGap(t *testing.T) {
	cfg := Default()
	cfg.Ops.InferenceGapMin = 121
	cfg.Ops.InferenceLateHours = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("inference lateness shorter than gap must be rejected")
	}

	cfg.Ops.InferenceLateHours = 3
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid inference window rejected: %v", err)
	}
}

func TestContentDefaultsAndValidation(t *testing.T) {
	cfg := Default()
	if cfg.Content.CacheDir == "" || cfg.Content.ScanMaxFiles < 1 ||
		cfg.Content.ScanMaxDepth < 1 ||
		cfg.EPUBLimits().Validate() != nil {
		t.Fatalf("invalid content defaults: %+v", cfg.Content)
	}
	cfg.Content.ScanMaxFiles = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid scan file bound accepted")
	}
	cfg.Content.ScanMaxFiles = 200_000
	cfg.Content.ScanMaxDepth = 257
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid scan depth accepted")
	}
	cfg.Content.ScanMaxDepth = 32
	cfg.Content.EPUBMaxXMLDepth = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid EPUB limits accepted")
	}
}

func TestCacheDirEnvironmentOverride(t *testing.T) {
	t.Setenv("LISEUR_CACHE_DIR", "/srv/liseur-cache")
	cfg := Default()
	cfg.applyEnv()
	if cfg.Content.CacheDir != "/srv/liseur-cache" {
		t.Fatalf("cache dir override: %q", cfg.Content.CacheDir)
	}
}

// TestFolderRootsAreUnsetByDefault. An empty allowlist means "anywhere
// the server can read", which is what the add-folder subcommand allows;
// an operator narrows it by listing the trees their books live under.
func TestFolderRootsAreUnsetByDefault(t *testing.T) {
	if roots := Default().Content.FolderRoots; len(roots) != 0 {
		t.Fatalf("default folder roots: %v", roots)
	}
}
