//go:build linux

package content

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStagingCapRefusesWhatWouldNotFit is the point of the cap: uploads
// that are each within every per-request and per-user bound must still be
// refused once they would collectively fill the disk.
func TestStagingCapRefusesWhatWouldNotFit(t *testing.T) {
	cas := openTestCAS(t)
	cas.SetStagingCap(100)

	if _, err := cas.Stage(t.Context(), "job-1",
		bytes.NewReader(make([]byte, 60)), 60); err != nil {
		t.Fatalf("first stage inside the cap: %v", err)
	}
	// 60 are on disk, so a second upload of 60 does not fit.
	_, err := cas.Stage(t.Context(), "job-2",
		bytes.NewReader(make([]byte, 60)), 60)
	if !errors.Is(err, ErrStagingFull) {
		t.Fatalf("second stage: %v, want ErrStagingFull", err)
	}
	// What does fit is still accepted, so the cap bounds rather than bars.
	if _, err := cas.Stage(t.Context(), "job-3",
		bytes.NewReader(make([]byte, 40)), 40); err != nil {
		t.Fatalf("stage that fits: %v", err)
	}
}

// TestStagingCapCountsBytesNoDatabaseKnowsAbout: stages orphaned by a crash
// occupy the disk and no table references them. A cap computed from job
// rows would hand out room that is not there.
func TestStagingCapCountsBytesNoDatabaseKnowsAbout(t *testing.T) {
	cas := openTestCAS(t)
	// An orphan, as a crash between staging and commit leaves behind.
	writeIncomingFile(t, cas, "orphan.stage", make([]byte, 90))

	cas.SetStagingCap(100)
	_, err := cas.Stage(t.Context(), "job-1",
		bytes.NewReader(make([]byte, 50)), 50)
	if !errors.Is(err, ErrStagingFull) {
		t.Fatalf("stage over an orphan: %v, want ErrStagingFull", err)
	}
}

// TestStagingCapIsUnlimitedWhenZero keeps the previous behaviour available,
// and is what an operator who has sized their disk another way gets.
func TestStagingCapIsUnlimitedWhenZero(t *testing.T) {
	cas := openTestCAS(t)
	cas.SetStagingCap(0)
	for _, job := range []string{"a", "b", "c"} {
		if _, err := cas.Stage(t.Context(), job,
			bytes.NewReader(make([]byte, 1000)), 1000); err != nil {
			t.Fatalf("stage %s with no cap: %v", job, err)
		}
	}
}

// TestStagingCapReservesTheWorstCase: the size of an upload is not known
// until it has been read, and reading it is the thing the cap must be able
// to refuse. So the bound is what the request may become, not what it is.
func TestStagingCapReservesTheWorstCase(t *testing.T) {
	cas := openTestCAS(t)
	cas.SetStagingCap(100)
	// One byte of content, but permitted to grow to 100.
	if _, err := cas.Stage(t.Context(), "job-1",
		bytes.NewReader([]byte("x")), 100); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	// The reservation is released when staging ends, so the one byte
	// actually written is all that counts against the next upload.
	if _, err := cas.Stage(t.Context(), "job-2",
		bytes.NewReader(make([]byte, 98)), 98); err != nil {
		t.Fatalf("second stage after release: %v", err)
	}
}

// TestStagingCapHoldsAcrossConcurrentUploads is the race the reservation
// exists for: without it every request measures the same free space and
// they all take it.
func TestStagingCapHoldsAcrossConcurrentUploads(t *testing.T) {
	cas := openTestCAS(t)
	const cap, each = 1000, 400
	cas.SetStagingCap(cap)

	var wg sync.WaitGroup
	results := make([]error, 8)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// A slow reader keeps every upload overlapping, which is
			// what makes them compete for the same reservation.
			body := slowReader{r: bytes.NewReader(make([]byte, each))}
			_, results[i] = cas.Stage(t.Context(),
				"concurrent-"+string(rune('a'+i)), &body, each)
		}()
	}
	close(start)
	wg.Wait()

	var ok int
	for i, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrStagingFull):
		default:
			t.Fatalf("upload %d failed unexpectedly: %v", i, err)
		}
	}
	if ok == 0 {
		t.Fatal("every concurrent upload was refused")
	}
	// Whatever got through must fit: that is the whole guarantee.
	used := incomingSize(t, cas)
	if used > cap {
		t.Fatalf("staged %d bytes over a cap of %d", used, cap)
	}
}

// TestStagingCapLetsAFinishedStageBeReentered: a replay re-enters bytes it
// already holds. Refusing it would strand an upload that is complete.
func TestStagingCapLetsAFinishedStageBeReentered(t *testing.T) {
	cas := openTestCAS(t)
	cas.SetStagingCap(100)
	first, err := cas.Stage(t.Context(), "job-1",
		bytes.NewReader(make([]byte, 80)), 80)
	if err != nil {
		t.Fatal(err)
	}
	// The cap is now full, but this job's bytes are already inside it.
	again, err := cas.Stage(t.Context(), "job-1",
		strings.NewReader("ignored"), 80)
	if err != nil {
		t.Fatalf("replay of a staged job: %v", err)
	}
	if again.SHA256 != first.SHA256 || again.Size != first.Size {
		t.Fatalf("replay = %+v, want %+v", again, first)
	}
}

// writeIncomingFile puts a file directly into the incoming directory, as a
// crash between staging and commit leaves behind.
func writeIncomingFile(t *testing.T, cas *CAS, name string, data []byte) {
	t.Helper()
	path := filepath.Join(cas.Root(), ".incoming", name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func incomingSize(t *testing.T, cas *CAS) int64 {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cas.Root(), ".incoming"))
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

type slowReader struct {
	r    *bytes.Reader
	done bool
}

func (s *slowReader) Read(p []byte) (int, error) {
	if !s.done {
		s.done = true
		// Hold the reservation briefly so the other uploads reach theirs
		// before this one finishes and releases it.
		time.Sleep(20 * time.Millisecond)
	}
	return s.r.Read(p)
}

// TestStagingCapCountsOnlyFiles: a directory's own inode size is not
// staged content, and charging an upload for it would refuse room that
// exists.
func TestStagingCapCountsOnlyFiles(t *testing.T) {
	cas := openTestCAS(t)
	if err := os.Mkdir(filepath.Join(cas.Root(), ".incoming", "stray"),
		0o700); err != nil {
		t.Fatal(err)
	}
	cas.SetStagingCap(100)
	if _, err := cas.Stage(t.Context(), "job-1",
		bytes.NewReader(make([]byte, 100)), 100); err != nil {
		t.Fatalf("stage beside a stray directory: %v", err)
	}
}

// TestOpenSaysWhyARootWasRefused: an operator who prepares the directory
// by hand gets 0755 from mkdir, and the refusal has to point at the
// permissions rather than at the path.
func TestOpenSaysWhyARootWasRefused(t *testing.T) {
	root := filepath.Join(t.TempDir(), "content")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Open(root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
	for _, want := range []string{root, "0755", "chmod 700"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	// The same directory, made private, is accepted: the message named
	// the actual fix.
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cas, err := Open(root)
	if err != nil {
		t.Fatalf("open a private root: %v", err)
	}
	cas.Close()
}

// TestOpenRefusesARootThatIsAFile keeps the other reasons distinguishable,
// since a single sentinel for all of them is what made this opaque.
func TestOpenRefusesARootThatIsAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "content")
	if err := os.WriteFile(root, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(root)
	if !errors.Is(err, ErrUnsafePath) ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v, want a not-a-directory refusal", err)
	}
}
