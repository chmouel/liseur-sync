package config

import "testing"

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
	if cfg.Content.Root == "" || cfg.Content.FailureRetentionHours < 1 ||
		cfg.Content.RecoveryBatchSize < 1 {
		t.Fatalf("invalid content defaults: %+v", cfg.Content)
	}
	cfg.Content.RecoveryBatchSize = 501
	if err := cfg.Validate(); err == nil {
		t.Fatal("oversized recovery batch accepted")
	}
}

func TestContentRootEnvironmentOverride(t *testing.T) {
	t.Setenv("LISEUR_CONTENT_ROOT", "/srv/liseur-content")
	cfg := Default()
	cfg.applyEnv()
	if cfg.Content.Root != "/srv/liseur-content" {
		t.Fatalf("content root override: %q", cfg.Content.Root)
	}
}
