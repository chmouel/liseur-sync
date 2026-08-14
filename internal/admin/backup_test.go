//go:build linux

package admin

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// captureIn is capture with a chosen content directory, which is the
// whole subject of these tests.
func captureIn(
	t *testing.T, st store.Store, root string, args ...string,
) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	runErr := Run(st, root, args)
	os.Stdout = saved
	w.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		sb.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	r.Close()
	return sb.String(), runErr
}

// seedBackup builds a library with one promoted book, and returns the
// content root holding its bytes.
func seedBackup(t *testing.T, st store.Store) string {
	t.Helper()
	root := t.TempDir() + "/content"
	cas, err := content.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cas.Close()
	ctx := t.Context()
	user := addUser(t, st, "backup-alice")
	now := time.Now().UTC()
	library := store.Library{
		ID: "lib", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Library", CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	body := []byte("a book that must survive a restore")
	job, _, err := st.CreateIngestJob(ctx, user.ID, store.IngestJobRequest{
		ID: "job", LibraryID: library.ID, Source: store.IngestUpload,
		RequestFingerprint: "fingerprint", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	staged, err := cas.Stage(ctx, job.ID, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	stage, err := st.CommitIngestStage(ctx, user.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact: store.BlobInfo{
				SHA256: staged.SHA256, SizeBytes: staged.Size,
			},
			StagingPath: staged.Path,
			UpdatedAt:   now,
		})
	if err != nil {
		t.Fatal(err)
	}
	job = stage.Job
	blob, err := cas.Promote(ctx, staged.Path, staged.SHA256, staged.Size)
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.TransitionIngestJob(ctx, user.ID, job.ID, store.IngestJobTransition{
		ExpectedState: job.State, ExpectedRevision: job.Revision,
		NextState: store.IngestValidated, UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.TransitionIngestJob(ctx, user.ID, job.ID, store.IngestJobTransition{
		ExpectedState: job.State, ExpectedRevision: job.Revision,
		NextState:                     store.IngestExtracted,
		ExtractedEmbeddedMetadataJSON: []byte(`{"title":"Restorable"}`),
		UpdatedAt:                     now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommitNewBookPromotion(ctx, user.ID, job.ID,
		store.CommitNewBookPromotionRequest{
			ExpectedRevision: job.Revision,
			Blob: store.BlobInfo{
				SHA256: blob.SHA256, SizeBytes: blob.Size,
			},
			Book: store.CatalogBook{
				ID: "book", LibraryID: library.ID, Status: store.BookActive,
				Title: "Restorable", TitleSource: store.MetadataEmbedded,
				CreatedAt: now, UpdatedAt: now,
			},
			File: store.BookFile{
				ID: "file", LibraryID: library.ID, BookID: "book",
				BlobSHA256: blob.SHA256, Source: store.IngestUpload,
				OriginalFilename: "book.epub",
				MediaType:        "application/epub+zip",
				Availability:     store.BookFileAvailable,
				CreatedAt:        now, UpdatedAt: now,
			},
			UpdatedAt: now.Add(3 * time.Second),
		}); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestVerifyBackupSaysWhenABackupIsSound is the command an operator runs
// after a backup finishes. It has to answer without ambiguity, because
// the alternative is finding out at restore time.
func TestVerifyBackupSaysWhenABackupIsSound(t *testing.T) {
	st := newAdminStore(t)
	root := seedBackup(t, st)
	out, err := captureIn(t, st, root, "verify-backup")
	if err != nil {
		t.Fatalf("sound backup reported broken: %v\n%s", err, out)
	}
	if !strings.Contains(out, "restorable") ||
		!strings.Contains(out, "referenced blobs:  1") {
		t.Fatalf("unhelpful output:\n%s", out)
	}
}

// TestVerifyBackupFailsWhenContentIsMissing: a backup that copied the
// database and not the files looks perfect until someone needs it. The
// command must exit non-zero so a backup script can notice.
func TestVerifyBackupFailsWhenContentIsMissing(t *testing.T) {
	st := newAdminStore(t)
	root := seedBackup(t, st)
	// The database is kept and the content directory is intact, but one
	// blob never made it across — which is what a copy interrupted
	// halfway actually looks like, and what a backup script cannot see.
	broken := t.TempDir() + "/broken"
	copyTree(t, root, broken)
	removeOneBlob(t, broken)
	out, err := captureIn(t, st, broken, "verify-backup")
	if err == nil {
		t.Fatalf("missing content reported restorable:\n%s", out)
	}
	if !strings.Contains(out, "missing:           1") {
		t.Fatalf("output does not say what is missing:\n%s", out)
	}
	if !strings.Contains(out, "not in the backup") {
		t.Fatalf("output does not name the blob:\n%s", out)
	}
	// The sound copy is still sound: verification changed nothing.
	if _, err := captureIn(t, st, root, "verify-backup"); err != nil {
		t.Fatalf("verification altered the backup it checked: %v", err)
	}
}

// TestVerifyBackupTakesNoArguments keeps the operator from checking one
// backup's database against another backup's files, which would report
// a healthy pair that does not exist.
func TestVerifyBackupTakesNoArguments(t *testing.T) {
	st := newAdminStore(t)
	root := seedBackup(t, st)
	if _, err := captureIn(
		t, st, root, "verify-backup", "/some/other/place",
	); err == nil {
		t.Fatal("a second path was accepted")
	}
	if _, err := captureIn(t, st, "", "verify-backup"); err == nil {
		t.Fatal("verification ran with no content directory")
	}
}

// TestVerifyBackupExplainsAWorldReadableRestore covers the mistake this
// command exists to catch early. A content directory restored with a
// plain recursive copy comes back readable by everyone, and the CAS
// refuses it — with a message that has to say what to do about it.
func TestVerifyBackupExplainsAWorldReadableRestore(t *testing.T) {
	st := newAdminStore(t)
	root := seedBackup(t, st)
	loose := filepath.Join(t.TempDir(), "loose")
	copyTree(t, root, loose)
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := captureIn(t, st, loose, "verify-backup")
	if err == nil {
		t.Fatal("a world-readable content directory was accepted")
	}
	if !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// copyTree duplicates a content directory so a test can damage the copy
// and still check the original was left alone.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		t.Fatal(err)
	}
	// os.CopyFS widens permissions; a faithful backup (cp -a, tar -p)
	// preserves the CAS's private modes, so they are restored here.
	if err := filepath.WalkDir(to, func(
		path string, d fs.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

// removeOneBlob deletes the first durable blob it finds, leaving the
// directory structure in place.
func removeOneBlob(t *testing.T, root string) {
	t.Helper()
	found := false
	err := filepath.WalkDir(filepath.Join(root, "sha256"),
		func(path string, d fs.DirEntry, err error) error {
			if err != nil || found || d.IsDir() {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			found = true
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no blob to remove")
	}
}

// TestVerifyBackupReportsRottedBytesRatherThanDying is the fault a backup
// check is most needed for and the easiest one to report badly: media
// that returns the file at the right name with the wrong bytes. The
// command has to name the digest and exit non-zero, not fail with an
// error about the content store that tells the operator nothing about
// which blob to re-copy.
func TestVerifyBackupReportsRottedBytesRatherThanDying(t *testing.T) {
	st := newAdminStore(t)
	root := seedBackup(t, st)
	rotted := t.TempDir() + "/rotted"
	copyTree(t, root, rotted)
	rotOneBlob(t, rotted)

	out, err := captureIn(t, st, rotted, "verify-backup")
	if err == nil {
		t.Fatalf("a backup of damaged bytes was reported restorable:\n%s", out)
	}
	if !strings.Contains(out, "corrupt:           1") {
		t.Fatalf("output does not count the damage:\n%s", out)
	}
	if !strings.Contains(out, "does not match its digest") {
		t.Fatalf("output does not say what is wrong:\n%s", out)
	}
	if !strings.Contains(out, "referenced blobs:  1") {
		t.Fatalf("output does not name the blob:\n%s", out)
	}
}

// rotOneBlob rewrites the first durable blob it finds with different
// bytes, leaving it filed under its original digest — media rot, or a
// copy that damaged the file in flight.
func rotOneBlob(t *testing.T, root string) {
	t.Helper()
	found := false
	err := filepath.WalkDir(filepath.Join(root, "sha256"),
		func(path string, d fs.DirEntry, err error) error {
			if err != nil || found || d.IsDir() {
				return err
			}
			if err := os.Chmod(path, 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("rot"), 0o400); err != nil {
				return err
			}
			found = true
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no blob to damage")
	}
}
