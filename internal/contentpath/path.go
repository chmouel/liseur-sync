// Package contentpath defines deterministic paths shared by the database and
// filesystem content layers.
package contentpath

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// JobDigest is the stable filesystem-safe digest for one ingest job ID.
func JobDigest(jobID string) string {
	sum := sha256.Sum256([]byte(jobID))
	return hex.EncodeToString(sum[:])
}

// StagingPath is the only completed staging path valid for a job.
func StagingPath(jobID string) string {
	return filepath.ToSlash(filepath.Join(
		".incoming", JobDigest(jobID)+".stage"))
}
