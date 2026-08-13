//go:build linux

package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// backupVerifyPageSize bounds the walk. Verification is read-only, so
// the only reason to page at all is to keep memory flat on a large
// library.
const backupVerifyPageSize = 500

// verifyBackup answers the only question that matters about a backup:
// can it be restored from? It compares what the database says must exist
// against what the content directory holds, and reports rather than
// repairs — a verifier that changed either side would be altering the
// thing it was asked to inspect.
//
// It is run against a config pointing at the copy to check, which is why
// it takes no arguments: the config already names the database and the
// content directory, and a verifier that took a second, different path
// would invite checking one backup's database against another's files.
func verifyBackup(
	ctx context.Context, st store.Store, contentRoot string, args []string,
) error {
	if len(args) != 0 {
		return errors.New(
			"usage: verify-backup (checks the database and content " +
				"directory named by -config)")
	}
	if contentRoot == "" {
		return errors.New("no content directory is configured")
	}
	cas, err := content.Open(contentRoot)
	if err != nil {
		if errors.Is(err, content.ErrUnsafePath) {
			// Worth saying plainly: a content directory restored with a
			// plain recursive copy usually comes back world-readable, and
			// "unsafe path" on its own tells the operator nothing about
			// what to do next.
			return fmt.Errorf(
				"%s cannot be used as a content directory: it must be a "+
					"real directory owned by this user with no group or "+
					"other permissions (chmod 700): %w", contentRoot, err)
		}
		return fmt.Errorf("open content store: %w", err)
	}
	defer cas.Close()

	report, err := content.VerifyBackup(ctx, st, cas, backupVerifyPageSize)
	if err != nil {
		return err
	}
	fmt.Printf("content directory: %s\n", contentRoot)
	fmt.Printf("referenced blobs:  %d\n", report.ReferencedBlobs)
	fmt.Printf("present:           %d\n", report.PresentBlobs)
	fmt.Printf("missing:           %d\n", report.MissingBlobs)
	fmt.Printf("wrong size:        %d\n", report.MismatchedBlobs)
	fmt.Printf("corrupt:           %d\n", report.CorruptBlobs)
	fmt.Printf("unreferenced:      %d (left for ordinary reconciliation)\n",
		report.ExtraBlobs)
	for _, problem := range report.Problems {
		fmt.Printf("  %s %s\n", problem.SHA256, problem.Detail)
	}
	problems := report.MissingBlobs + report.MismatchedBlobs + report.CorruptBlobs
	if len(report.Problems) < problems {
		fmt.Printf("  ... and %d more\n", problems-len(report.Problems))
	}
	if !report.Valid() {
		// A non-zero exit is the part a backup script can act on, and the
		// reason this is a subcommand rather than a log line.
		return errors.New(
			"this backup is not restorable: content is missing or does not " +
				"match the database")
	}
	fmt.Println("this backup is restorable")
	return nil
}
