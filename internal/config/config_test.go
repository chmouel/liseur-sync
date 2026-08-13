package config

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
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
	if cfg.Content.Root == "" || cfg.Content.FailureRetentionHours < 1 ||
		cfg.Content.OrphanGraceHours < 1 || cfg.Content.RecoveryBatchSize < 1 ||
		cfg.EPUBLimits().Validate() != nil {
		t.Fatalf("invalid content defaults: %+v", cfg.Content)
	}
	cfg.Content.OrphanGraceHours = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid orphan grace accepted")
	}
	cfg.Content.OrphanGraceHours = int((1<<63-1)/time.Hour) + 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("overflowing orphan grace accepted")
	}
	cfg.Content.OrphanGraceHours = 168
	cfg.Content.EPUBMaxXMLDepth = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid EPUB limits accepted")
	}
	cfg.Content.EPUBMaxXMLDepth = epub.DefaultLimits().MaxXMLDepth
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
