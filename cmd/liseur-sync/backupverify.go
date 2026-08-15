package main

import (
	"context"
	"fmt"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/webui"
)

// backupVerifier is the panel's half of `admin verify-backup`: the same
// check, over the content store this server already has open, so the
// answer a browser gets and the answer a backup script gets come from
// one implementation.
//
// The subcommand opens the directory named by its own -config, which is
// how it can be pointed at a copy. This one can only check the running
// server's own pair, which is the question somebody with the panel open
// is asking.
type backupVerifier struct {
	st  store.Store
	cas *content.CAS
}

// backupVerifyPageSize bounds the walk. Verification is read-only, so
// the only reason to page at all is to keep memory flat.
const backupVerifyPageSize = 500

// backupProblemsShown bounds what reaches the page. The rest is
// counted: an operator with ten thousand damaged blobs needs the count
// and the log, not ten thousand table rows in a browser.
const backupProblemsShown = 50

func (v *backupVerifier) VerifyBackup(ctx context.Context) (webui.BackupReport, error) {
	var out webui.BackupReport
	if v.st == nil || v.cas == nil {
		return out, fmt.Errorf("no content store is open")
	}
	report, err := content.VerifyBackup(ctx, v.st, v.cas, backupVerifyPageSize)
	if err != nil {
		return out, err
	}
	out = webui.BackupReport{
		ReferencedBlobs: report.ReferencedBlobs,
		PresentBlobs:    report.PresentBlobs,
		MissingBlobs:    report.MissingBlobs,
		MismatchedBlobs: report.MismatchedBlobs,
		CorruptBlobs:    report.CorruptBlobs,
		ExtraBlobs:      report.ExtraBlobs,
		Valid:           report.Valid(),
	}
	for _, problem := range report.Problems {
		if len(out.Problems) == backupProblemsShown {
			break
		}
		out.Problems = append(out.Problems, problem.SHA256+" "+problem.Detail)
	}
	total := report.MissingBlobs + report.MismatchedBlobs + report.CorruptBlobs
	if total > len(out.Problems) {
		out.More = total - len(out.Problems)
	}
	return out, nil
}
