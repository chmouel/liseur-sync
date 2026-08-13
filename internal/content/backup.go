//go:build linux

package content

import (
	"context"
	"fmt"

	"github.com/chmouel/liseur-sync/internal/store"
)

type backupVerificationStore interface {
	ListReferencedBlobs(context.Context, string, int) ([]store.BlobInfo, error)
}

// maxBackupProblemsReported keeps a report readable when a backup is
// wholly wrong. The count is exact whatever happens; only the list of
// digests is cut, because ten thousand of them help nobody.
const maxBackupProblemsReported = 50

// BackupProblem is one referenced blob a backup cannot honour.
type BackupProblem struct {
	SHA256 string
	// Detail says what is wrong in the operator's words: the blob is
	// absent, or present at the wrong size.
	Detail string
}

// BackupVerificationReport is the answer to "can I restore from this?".
type BackupVerificationReport struct {
	ReferencedBlobs int
	PresentBlobs    int
	MissingBlobs    int
	// MismatchedBlobs counts blobs that are in the backup at a size the
	// database disagrees with. They are separate from missing ones
	// because they mean something different to whoever has to fix it: a
	// missing blob was not copied, a mismatched one is not the same file.
	MismatchedBlobs int
	// ExtraBlobs counts content the database does not reference. It is
	// reported and never acted on: after a restore these are exactly what
	// ordinary grace-period reconciliation is for, and a verifier that
	// deleted them would destroy the evidence of a partial restore.
	ExtraBlobs int
	Problems   []BackupProblem
}

// Valid reports whether the backup can be restored from. Extra blobs do
// not make a backup invalid; a single referenced blob that is absent or
// the wrong size does.
func (r BackupVerificationReport) Valid() bool {
	return r.MissingBlobs == 0 && r.MismatchedBlobs == 0
}

// VerifyBackup checks that a database and a content directory are a
// restorable pair: every blob the database references is present, at the
// size the database recorded. It reads both and changes neither, so it
// is safe to run against a live server as well as a copy.
//
// It deliberately does not re-hash the content. Digests are verified when
// bytes are promoted and again by reconciliation; what a backup gets
// wrong is not corruption of individual files but capturing the database
// and the content directory at different moments, which shows up as a
// referenced blob that was never copied.
func VerifyBackup(
	ctx context.Context,
	st backupVerificationStore,
	inventory blobInventory,
	pageSize int,
) (BackupVerificationReport, error) {
	var report BackupVerificationReport
	if st == nil || inventory == nil || pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	blobs, err := inventory.ListBlobs(ctx)
	if err != nil {
		return report, fmt.Errorf("inventory content blobs: %w", err)
	}
	physical := make(map[string]int64, len(blobs))
	for _, blob := range blobs {
		if _, duplicate := physical[blob.SHA256]; duplicate {
			return report, fmt.Errorf(
				"inventory blob %q: %w", blob.SHA256, store.ErrInvariantViolation)
		}
		physical[blob.SHA256] = blob.Size
	}

	cursor := ""
	for {
		records, err := st.ListReferencedBlobs(ctx, cursor, pageSize)
		if err != nil {
			return report, fmt.Errorf(
				"list referenced blobs after %q: %w", cursor, err)
		}
		if len(records) > pageSize {
			return report, store.ErrInvariantViolation
		}
		previous := cursor
		for _, record := range records {
			if err := store.ValidateBlobInfo(record); err != nil ||
				record.SHA256 <= previous {
				return report, fmt.Errorf(
					"invalid referenced blob after %q: %w",
					previous, store.ErrInvariantViolation)
			}
			previous = record.SHA256
			report.ReferencedBlobs++
			size, present := physical[record.SHA256]
			switch {
			case !present:
				report.MissingBlobs++
				report.note(record.SHA256, "not in the backup")
			case size != record.SizeBytes:
				report.MismatchedBlobs++
				// Accounted for either way: a blob the database points at
				// is not "extra" content just because it is the wrong
				// size.
				delete(physical, record.SHA256)
				// A size that disagrees means the file in the backup is
				// not the file the database recorded, whatever its name
				// says. Restoring it would serve the wrong bytes.
				report.note(record.SHA256, fmt.Sprintf(
					"is %d bytes in the backup, %d in the database",
					size, record.SizeBytes))
			default:
				report.PresentBlobs++
				// Counted as accounted for, so what is left over at the
				// end is genuinely unreferenced.
				delete(physical, record.SHA256)
			}
		}
		if len(records) < pageSize {
			break
		}
		if previous == cursor {
			return report, store.ErrInvariantViolation
		}
		cursor = previous
	}
	report.ExtraBlobs = len(physical)
	return report, nil
}

func (r *BackupVerificationReport) note(sha256, detail string) {
	if len(r.Problems) < maxBackupProblemsReported {
		r.Problems = append(r.Problems, BackupProblem{
			SHA256: sha256, Detail: detail,
		})
	}
}
