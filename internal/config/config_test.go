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
		cfg.Content.IngestWorkerInterval < 1 ||
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
	cfg.Content.IngestWorkerInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid ingest worker interval accepted")
	}
	cfg.Content.IngestWorkerInterval = 5
	cfg.Content.RecoveryBatchSize = 501
	if err := cfg.Validate(); err == nil {
		t.Fatal("oversized recovery batch accepted")
	}
}

// TestStagingCapMustLeaveRoomForOneUpload: a cap below the largest
// permitted upload refuses every upload, which looks like a broken server
// rather than a full one. Better to refuse the config.
func TestStagingCapMustLeaveRoomForOneUpload(t *testing.T) {
	cfg := Default()
	if cfg.Content.MaxStagingBytes < cfg.Content.MaxUploadBytes {
		t.Fatalf("default cap %d is under the default upload limit %d",
			cfg.Content.MaxStagingBytes, cfg.Content.MaxUploadBytes)
	}
	cfg.Content.MaxStagingBytes = cfg.Content.MaxUploadBytes - 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("a cap smaller than one upload was accepted")
	}
	cfg.Content.MaxStagingBytes = cfg.Content.MaxUploadBytes
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a cap of exactly one upload: %v", err)
	}
	// Zero keeps the pre-cap behaviour for operators who bound the disk
	// some other way, so it is not measured against the upload limit.
	cfg.Content.MaxStagingBytes = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an unlimited staging area: %v", err)
	}
	cfg.Content.MaxStagingBytes = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("a negative cap was accepted")
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

// TestExternalLookupIsOffUntilAskedFor. A self-hosted server that talks
// to nobody is the posture an operator gets without choosing it, and
// ADR-0004 keeps it that way: no default configuration may cause an
// outbound request.
func TestExternalLookupIsOffUntilAskedFor(t *testing.T) {
	c := Default()
	if len(c.Metadata.Providers) != 0 {
		t.Errorf("a default install would contact %v", c.Metadata.Providers)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the defaults do not validate: %v", err)
	}

	// The bounds are set even though the feature is off, so turning it
	// on is one line rather than four.
	limits := c.MetadataLimits()
	if limits.Timeout <= 0 || limits.MaxBytes <= 0 || limits.MaxRedirects <= 0 {
		t.Errorf("lookup limits are unset: %+v", limits)
	}

	// A misspelled provider stops the server rather than being ignored.
	c.Metadata.Providers = []string{"openlibary"}
	if err := c.Validate(); err == nil {
		t.Error("an unknown provider was accepted")
	}

	c.Metadata.Providers = []string{"openlibrary"}
	if err := c.Validate(); err != nil {
		t.Errorf("a known provider was refused: %v", err)
	}
}
