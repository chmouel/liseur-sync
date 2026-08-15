package webui

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Backup verification from the panel — the `verify-backup` subcommand,
// which answers the only question that matters about a backup: can it
// be restored from?
//
// It is here rather than only in a shell because the operator who most
// needs the answer is the one who has just restored onto a new machine
// and has a browser open. The check reads both sides and changes
// neither, so running it against a live server is safe.
//
// It is *not* run inside the request. Verification re-hashes every
// referenced blob, which on a real library is minutes of disk, and a
// browser that waits that long has already given up. So the button
// starts one run, the page reports what the run is doing, and the
// result stays until the next run replaces it.

// BackupVerifier checks that a database and a content directory are a
// restorable pair. The webui package takes it as an interface so that
// it keeps depending on nothing but the store: the implementation lives
// where the content store is already open.
type BackupVerifier interface {
	VerifyBackup(ctx context.Context) (BackupReport, error)
}

// BackupReport is the verifier's answer, in the shape the page renders.
// It is counts and digests, never a path: the content directory's
// layout is not something a browser needs.
type BackupReport struct {
	ReferencedBlobs int
	PresentBlobs    int
	MissingBlobs    int
	MismatchedBlobs int
	CorruptBlobs    int
	ExtraBlobs      int
	// Problems names the first few blobs that are wrong, by digest and
	// in words. A digest is not somebody's book: it is the identifier an
	// operator needs to find the file in the backup.
	Problems []string
	// More counts the problems beyond the ones listed.
	More  int
	Valid bool
}

// backupRun is the state of the one verification this server runs at a
// time. A second button press while one is running is ignored rather
// than queued: two concurrent passes over the same blobs answer the
// same question twice and halve the disk each gets.
type backupRun struct {
	mu       sync.Mutex
	running  bool
	started  time.Time
	finished time.Time
	report   BackupReport
	err      string
	ran      bool
}

func (b *backupRun) snapshot() backupRunView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return backupRunView{
		Running:  b.running,
		Ran:      b.ran,
		Started:  b.started,
		Finished: b.finished,
		Report:   b.report,
		Error:    b.err,
	}
}

// backupRunView is what the template reads: a value, taken under the
// lock, so a render never sees half of a finishing run.
type backupRunView struct {
	Running  bool
	Ran      bool
	Started  time.Time
	Finished time.Time
	Report   BackupReport
	Error    string
}

// Available says whether this server can verify at all. It cannot when
// no content store was handed to it, which is the case in tests and in
// any build that does not open one.
func (s *Server) backupsAvailable() bool { return s.Backups != nil }

func (v backupRunView) Took() string {
	if v.Started.IsZero() || v.Finished.IsZero() || v.Finished.Before(v.Started) {
		return ""
	}
	return age(v.Finished.Sub(v.Started))
}

// handleAdminVerifyBackup starts a verification, or says one is already
// running. It carries no password re-verification: it reads what the
// server already reads and hands out nothing (ADR-0013).
func (s *Server) handleAdminVerifyBackup(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.backupsAvailable() {
		s.renderAdminMaintenance(w, r, a, u, Flash{
			Error: "This server has no content directory to verify."})
		return
	}
	started := s.startBackupVerification()
	logAdminAction(r, u, "verify-backup", "", nil)
	if !started {
		s.renderAdminMaintenance(w, r, a, u, Flash{
			Error: "A verification is already running."})
		return
	}
	s.renderAdminMaintenance(w, r, a, u, Flash{
		Notice: "Verifying. It reads every referenced file, so it takes as " +
			"long as the content directory takes to read; reload this page " +
			"for the result."})
}

// startBackupVerification launches the run and reports whether it
// started. The context is the server's rather than the request's: the
// browser that asked has long since been answered, and a verification
// cancelled by a closed tab is a verification that never finishes.
func (s *Server) startBackupVerification() bool {
	s.backup.mu.Lock()
	if s.backup.running {
		s.backup.mu.Unlock()
		return false
	}
	s.backup.running = true
	s.backup.started = time.Now()
	s.backup.finished = time.Time{}
	s.backup.err = ""
	s.backup.report = BackupReport{}
	s.backup.mu.Unlock()

	verifier := s.Backups
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), backupVerifyTimeout)
		defer cancel()
		report, err := verifier.VerifyBackup(ctx)
		s.backup.mu.Lock()
		defer s.backup.mu.Unlock()
		s.backup.running = false
		s.backup.ran = true
		s.backup.finished = time.Now()
		s.backup.report = report
		if err != nil {
			s.backup.err = err.Error()
		}
	}()
	return true
}

// backupVerifyTimeout bounds one run so that a content directory on a
// disk that has stopped answering does not leave the page saying
// "running" until the process is restarted.
const backupVerifyTimeout = 6 * time.Hour
