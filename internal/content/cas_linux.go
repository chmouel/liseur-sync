//go:build linux

// Package content implements the durable content-addressed filesystem.
package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"golang.org/x/sys/unix"
)

var (
	ErrTooLarge              = errors.New("content: input exceeds size limit")
	ErrStageMissing          = errors.New("content: staged file is missing")
	ErrDigestMismatch        = errors.New("content: staged content does not match its digest")
	ErrCorruptBlob           = errors.New("content: existing blob does not match its digest")
	ErrUnsafePath            = errors.New("content: unsafe path or file type")
	ErrUnsupportedFilesystem = errors.New("content: filesystem lacks atomic no-replace rename")
)

// StagedBlob is a complete, durable file under the CAS incoming directory.
type StagedBlob struct {
	Path   string
	SHA256 string
	Size   int64
}

// Blob is one durable content-addressed publication.
type Blob struct {
	Path           string
	SHA256         string
	Size           int64
	AlreadyPresent bool
}

// ArtifactLocation reports where a verified ingest artifact currently lives.
type ArtifactLocation string

const (
	ArtifactStaged   ArtifactLocation = "staged"
	ArtifactPromoted ArtifactLocation = "promoted"
)

// CAS owns a private content root. Close it after use.
type CAS struct {
	root       string
	rootFD     int
	incomingFD int
	shaFD      int
}

// Open creates and opens a durable private CAS root.
func Open(root string) (*CAS, error) {
	if root == "" {
		return nil, ErrUnsafePath
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if absolute == string(filepath.Separator) {
		return nil, ErrUnsafePath
	}
	rootFD, err := openOrCreateRoot(absolute)
	if err != nil {
		return nil, err
	}
	cas := &CAS{root: absolute, rootFD: rootFD, incomingFD: -1, shaFD: -1}
	cleanup := func(err error) (*CAS, error) {
		cas.Close()
		return nil, err
	}
	cas.incomingFD, err = ensureDirectoryAt(rootFD, ".incoming")
	if err != nil {
		return cleanup(err)
	}
	cas.shaFD, err = ensureDirectoryAt(rootFD, "sha256")
	if err != nil {
		return cleanup(err)
	}
	return cas, nil
}

// Close releases the directory descriptors held by the CAS.
func (c *CAS) Close() error {
	var errs []error
	for _, fd := range []*int{&c.shaFD, &c.incomingFD, &c.rootFD} {
		if *fd >= 0 {
			if err := unix.Close(*fd); err != nil {
				errs = append(errs, err)
			}
			*fd = -1
		}
	}
	return errors.Join(errs...)
}

// Root returns the absolute content root for administration and backups.
func (c *CAS) Root() string {
	return c.root
}

// Stage streams one job into the CAS-side incoming directory. Cancellation
// is observed between reads; it cannot interrupt a source Reader blocked
// inside Read.
func (c *CAS) Stage(ctx context.Context, jobID string, src io.Reader, maxBytes int64) (StagedBlob, error) {
	if jobID == "" || src == nil || maxBytes < 0 {
		return StagedBlob{}, ErrUnsafePath
	}
	prefix := stagePrefix(jobID)
	unlock, err := c.lockStage(ctx, prefix)
	if err != nil {
		return StagedBlob{}, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return StagedBlob{}, err
	}

	stageName := prefix + ".stage"
	if fd, err := unix.Openat(c.incomingFD, stageName,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0); err == nil {
		defer unix.Close(fd)
		return c.replayStage(ctx, fd,
			filepath.ToSlash(filepath.Join(".incoming", stageName)), maxBytes)
	} else if !errors.Is(err, unix.ENOENT) {
		return StagedBlob{}, classifyPathError(err)
	}

	partialName := prefix + ".partial"
	if err := unlinkIfExists(c.incomingFD, partialName); err != nil {
		return StagedBlob{}, err
	}
	fd, err := unix.Openat(c.incomingFD, partialName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600)
	if err != nil {
		return StagedBlob{}, classifyPathError(err)
	}
	file := os.NewFile(uintptr(fd), partialName)
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = unlinkIfExists(c.incomingFD, partialName)
			_ = unix.Fsync(c.incomingFD)
		}
	}()

	digest := sha256.New()
	size, err := copyBounded(ctx, file, digest, src, maxBytes)
	if err != nil {
		return StagedBlob{}, err
	}
	if err := file.Chmod(0o400); err != nil {
		return StagedBlob{}, err
	}
	if err := file.Sync(); err != nil {
		return StagedBlob{}, err
	}
	if err := file.Close(); err != nil {
		return StagedBlob{}, err
	}
	if err := unix.Renameat2(c.incomingFD, partialName, c.incomingFD, stageName,
		unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			stageFD, openErr := unix.Openat(c.incomingFD, stageName,
				unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if openErr != nil {
				return StagedBlob{}, classifyPathError(openErr)
			}
			defer unix.Close(stageFD)
			return c.replayStage(ctx, stageFD,
				filepath.ToSlash(filepath.Join(".incoming", stageName)), maxBytes)
		}
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) ||
			errors.Is(err, unix.EOPNOTSUPP) {
			return StagedBlob{}, ErrUnsupportedFilesystem
		}
		return StagedBlob{}, err
	}
	if err := unix.Fsync(c.incomingFD); err != nil {
		return StagedBlob{}, err
	}
	keep = true
	return StagedBlob{
		Path:   filepath.ToSlash(filepath.Join(".incoming", stageName)),
		SHA256: hex.EncodeToString(digest.Sum(nil)),
		Size:   size,
	}, nil
}

// Promote verifies and atomically publishes one staged blob. Once the
// no-replace rename succeeds, durability and cleanup finish even if ctx is
// canceled. Replaying after a lost successful result verifies the final blob
// and succeeds.
func (c *CAS) Promote(
	ctx context.Context,
	stagingPath, expectedSHA string,
	expectedSize int64,
) (Blob, error) {
	prefix, stageName, err := parseStagingPath(stagingPath)
	if err != nil || !validSHA256(expectedSHA) || expectedSize < 0 {
		return Blob{}, ErrUnsafePath
	}
	unlock, err := c.lockStage(ctx, prefix)
	if err != nil {
		return Blob{}, err
	}
	defer unlock()

	stageFD, err := unix.Openat(c.incomingFD, stageName,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return c.replayPromoted(ctx, expectedSHA, expectedSize)
	}
	if err != nil {
		return Blob{}, classifyPathError(err)
	}
	defer unix.Close(stageFD)
	if err := verifyFD(ctx, stageFD, expectedSHA, expectedSize); err != nil {
		if errors.Is(err, ErrCorruptBlob) {
			return Blob{}, ErrDigestMismatch
		}
		return Blob{}, err
	}

	prefixFD, leafFD, err := c.openFinalDirectories(expectedSHA, true)
	if err != nil {
		return Blob{}, err
	}
	defer unix.Close(prefixFD)
	defer unix.Close(leafFD)
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}

	published := false
	err = unix.Renameat2(c.incomingFD, stageName, leafFD, "file.epub",
		unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		published = true
	case errors.Is(err, unix.EEXIST):
		finalFD, openErr := unix.Openat(leafFD, "file.epub",
			unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return Blob{}, classifyPathError(openErr)
		}
		if verifyErr := verifyFD(ctx, finalFD, expectedSHA, expectedSize); verifyErr != nil {
			unix.Close(finalFD)
			return Blob{}, verifyErr
		}
		if syncErr := syncPublication(finalFD, leafFD, prefixFD, c.shaFD); syncErr != nil {
			unix.Close(finalFD)
			return Blob{}, syncErr
		}
		unix.Close(finalFD)
		if err := ctx.Err(); err != nil {
			return Blob{}, err
		}
		if err := unlinkIfExists(c.incomingFD, stageName); err != nil {
			return Blob{}, err
		}
		if err := unix.Fsync(c.incomingFD); err != nil {
			return Blob{}, err
		}
	case errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EOPNOTSUPP):
		return Blob{}, ErrUnsupportedFilesystem
	default:
		return Blob{}, err
	}
	if published {
		if err := syncPublication(stageFD, leafFD, prefixFD, c.shaFD); err != nil {
			return Blob{}, err
		}
		if err := unix.Fsync(c.incomingFD); err != nil {
			return Blob{}, err
		}
	}
	return Blob{
		Path:           finalRelativePath(expectedSHA),
		SHA256:         expectedSHA,
		Size:           expectedSize,
		AlreadyPresent: !published,
	}, nil
}

// InspectArtifact verifies a persisted ingest artifact without moving it.
// When the stage is absent, a matching final blob is accepted as evidence
// that filesystem promotion completed before the database response was lost.
func (c *CAS) InspectArtifact(
	ctx context.Context,
	stagingPath, expectedSHA string,
	expectedSize int64,
) (ArtifactLocation, error) {
	prefix, stageName, err := parseStagingPath(stagingPath)
	if err != nil || !validSHA256(expectedSHA) || expectedSize < 0 {
		return "", ErrUnsafePath
	}
	unlock, err := c.lockStage(ctx, prefix)
	if err != nil {
		return "", err
	}
	defer unlock()
	stageFD, err := unix.Openat(c.incomingFD, stageName,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		if _, replayErr := c.replayPromoted(ctx, expectedSHA, expectedSize); replayErr != nil {
			return "", replayErr
		}
		return ArtifactPromoted, nil
	}
	if err != nil {
		return "", classifyPathError(err)
	}
	defer unix.Close(stageFD)
	if err := verifyFD(ctx, stageFD, expectedSHA, expectedSize); err != nil {
		if errors.Is(err, ErrCorruptBlob) {
			return "", ErrDigestMismatch
		}
		return "", err
	}
	return ArtifactStaged, nil
}

// ListBlobs inventories and verifies every durable final blob. Empty hash
// directories left by an interrupted pre-publication attempt are ignored;
// every other unexpected entry is treated as unsafe CAS corruption.
func (c *CAS) ListBlobs(ctx context.Context) ([]Blob, error) {
	prefixes, err := readDirectoryEntries(c.shaFD)
	if err != nil {
		return nil, err
	}
	var blobs []Blob
	for _, prefix := range prefixes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(prefix) != 2 || !validLowerHex(prefix) {
			return nil, ErrUnsafePath
		}
		prefixFD, err := openDirectoryAt(c.shaFD, prefix)
		if err != nil {
			return nil, err
		}
		leaves, err := readDirectoryEntries(prefixFD)
		if err != nil {
			unix.Close(prefixFD)
			return nil, err
		}
		for _, leaf := range leaves {
			if err := ctx.Err(); err != nil {
				unix.Close(prefixFD)
				return nil, err
			}
			digest := prefix + leaf
			if len(leaf) != sha256.Size*2-2 || !validSHA256(digest) {
				unix.Close(prefixFD)
				return nil, ErrUnsafePath
			}
			leafFD, err := openDirectoryAt(prefixFD, leaf)
			if err != nil {
				unix.Close(prefixFD)
				return nil, err
			}
			entries, err := readDirectoryEntries(leafFD)
			if err != nil {
				unix.Close(leafFD)
				unix.Close(prefixFD)
				return nil, err
			}
			if len(entries) == 0 {
				unix.Close(leafFD)
				continue
			}
			if len(entries) != 1 || entries[0] != "file.epub" {
				unix.Close(leafFD)
				unix.Close(prefixFD)
				return nil, ErrUnsafePath
			}
			fileFD, err := unix.Openat(leafFD, "file.epub",
				unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			if err != nil {
				unix.Close(leafFD)
				unix.Close(prefixFD)
				return nil, classifyPathError(err)
			}
			var stat unix.Stat_t
			if err := unix.Fstat(fileFD, &stat); err != nil {
				unix.Close(fileFD)
				unix.Close(leafFD)
				unix.Close(prefixFD)
				return nil, err
			}
			if err := verifyFD(ctx, fileFD, digest, stat.Size); err != nil {
				unix.Close(fileFD)
				unix.Close(leafFD)
				unix.Close(prefixFD)
				return nil, err
			}
			unix.Close(fileFD)
			unix.Close(leafFD)
			blobs = append(blobs, Blob{
				Path: finalRelativePath(digest), SHA256: digest, Size: stat.Size,
			})
		}
		unix.Close(prefixFD)
	}
	return blobs, nil
}

// RemoveStage removes completed or partial staging state for one job. It is
// idempotent and serialized with Stage and Promote for that job.
func (c *CAS) RemoveStage(ctx context.Context, stagingPath string) error {
	prefix, stageName, err := parseStagingPath(stagingPath)
	if err != nil {
		return ErrUnsafePath
	}
	unlock, err := c.lockStage(ctx, prefix)
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unlinkIfExists(c.incomingFD, stageName); err != nil {
		return err
	}
	if err := unlinkIfExists(c.incomingFD, prefix+".partial"); err != nil {
		return err
	}
	return unix.Fsync(c.incomingFD)
}

func (c *CAS) replayPromoted(ctx context.Context, expectedSHA string, expectedSize int64) (Blob, error) {
	prefixFD, leafFD, err := c.openFinalDirectories(expectedSHA, false)
	if err != nil {
		if errors.Is(err, ErrStageMissing) {
			return Blob{}, ErrStageMissing
		}
		return Blob{}, err
	}
	defer unix.Close(prefixFD)
	defer unix.Close(leafFD)
	finalFD, err := unix.Openat(leafFD, "file.epub",
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return Blob{}, ErrStageMissing
	}
	if err != nil {
		return Blob{}, classifyPathError(err)
	}
	defer unix.Close(finalFD)
	if err := verifyFD(ctx, finalFD, expectedSHA, expectedSize); err != nil {
		return Blob{}, err
	}
	if err := syncPublication(finalFD, leafFD, prefixFD, c.shaFD); err != nil {
		return Blob{}, err
	}
	if err := unix.Fsync(c.incomingFD); err != nil {
		return Blob{}, err
	}
	return Blob{
		Path: finalRelativePath(expectedSHA), SHA256: expectedSHA,
		Size: expectedSize, AlreadyPresent: true,
	}, nil
}

func (c *CAS) openFinalDirectories(digest string, create bool) (int, int, error) {
	open := openDirectoryAt
	if create {
		open = ensureDirectoryAt
	}
	prefixFD, err := open(c.shaFD, digest[:2])
	if err != nil {
		if !create && errors.Is(err, unix.ENOENT) {
			return -1, -1, ErrStageMissing
		}
		return -1, -1, err
	}
	leafFD, err := open(prefixFD, digest[2:])
	if err != nil {
		unix.Close(prefixFD)
		if !create && errors.Is(err, unix.ENOENT) {
			return -1, -1, ErrStageMissing
		}
		return -1, -1, err
	}
	return prefixFD, leafFD, nil
}

func (c *CAS) lockStage(ctx context.Context, prefix string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(c.incomingFD, ".lock-"+prefix[:2],
		unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, classifyPathError(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return nil, ErrUnsafePath
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if contextErr := ctx.Err(); contextErr != nil {
				_ = unix.Flock(fd, unix.LOCK_UN)
				unix.Close(fd)
				return nil, contextErr
			}
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			unix.Close(fd)
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			unix.Close(fd)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = unix.Close(fd)
	}, nil
}

func copyBounded(ctx context.Context, dst io.Writer, digest hash.Hash, src io.Reader, maxBytes int64) (int64, error) {
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		readSize := len(buffer)
		remaining := maxBytes - total
		if remaining < int64(readSize) {
			readSize = int(remaining) + 1
		}
		n, readErr := src.Read(buffer[:readSize])
		if n > 0 {
			if int64(n) > remaining {
				return 0, ErrTooLarge
			}
			if _, err := dst.Write(buffer[:n]); err != nil {
				return 0, err
			}
			if _, err := digest.Write(buffer[:n]); err != nil {
				return 0, err
			}
			total += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return 0, readErr
		}
		if n == 0 {
			return 0, io.ErrNoProgress
		}
	}
}

func verifyFD(ctx context.Context, fd int, expectedSHA string, expectedSize int64) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size != expectedSize ||
		stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return ErrCorruptBlob
	}
	actualSHA, _, err := hashFD(ctx, fd, expectedSize)
	if err != nil {
		return err
	}
	if actualSHA != expectedSHA {
		return ErrCorruptBlob
	}
	return nil
}

func (c *CAS) replayStage(ctx context.Context, fd int, path string, maxBytes int64) (StagedBlob, error) {
	actualSHA, size, err := hashFD(ctx, fd, maxBytes)
	if err != nil {
		return StagedBlob{}, err
	}
	if err := unix.Fsync(fd); err != nil {
		return StagedBlob{}, err
	}
	if err := unix.Fsync(c.incomingFD); err != nil {
		return StagedBlob{}, err
	}
	return StagedBlob{Path: path, SHA256: actualSHA, Size: size}, nil
}

func hashFD(ctx context.Context, fd int, maxBytes int64) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return "", 0, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return "", 0, ErrUnsafePath
	}
	if stat.Size > maxBytes {
		return "", 0, ErrTooLarge
	}
	digest := sha256.New()
	buffer := make([]byte, 64<<10)
	var offset int64
	for offset < stat.Size {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		readSize := int64(len(buffer))
		if remaining := stat.Size - offset; remaining < readSize {
			readSize = remaining
		}
		n, readErr := unix.Pread(fd, buffer[:readSize], offset)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			offset += int64(n)
		}
		if readErr != nil {
			return "", 0, readErr
		}
		if n == 0 {
			return "", 0, ErrCorruptBlob
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), stat.Size, nil
}

func syncPublication(fileFD, leafFD, prefixFD, shaFD int) error {
	for _, fd := range []int{fileFD, leafFD, prefixFD, shaFD} {
		if err := unix.Fsync(fd); err != nil {
			return err
		}
	}
	return nil
}

func openDirectoryAt(parentFD int, name string) (int, error) {
	fd, err := unix.Openat(parentFD, name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, classifyPathError(err)
	}
	if err := validateDirectoryFD(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func ensureDirectoryAt(parentFD int, name string) (int, error) {
	if err := unix.Mkdirat(parentFD, name, 0o700); err != nil &&
		!errors.Is(err, unix.EEXIST) {
		return -1, err
	}
	fd, err := openDirectoryAt(parentFD, name)
	if err != nil {
		return -1, err
	}
	if err := unix.Fsync(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if err := unix.Fsync(parentFD); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func readDirectoryEntries(parentFD int) ([]string, error) {
	fd, err := openDirectoryAt(parentFD, ".")
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "content-directory")
	entries, err := file.ReadDir(-1)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func validateDirectoryFD(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 ||
		stat.Uid != uint32(os.Geteuid()) {
		return ErrUnsafePath
	}
	return nil
}

func openOrCreateRoot(path string) (int, error) {
	if !filepath.IsAbs(path) {
		return -1, ErrUnsafePath
	}
	parentFD, err := unix.Open(string(filepath.Separator),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)),
		string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			unix.Close(parentFD)
			return -1, ErrUnsafePath
		}
		nextFD, openErr := unix.Openat(parentFD, component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if err := unix.Mkdirat(parentFD, component, 0o700); err != nil &&
				!errors.Is(err, unix.EEXIST) {
				unix.Close(parentFD)
				return -1, err
			}
			nextFD, openErr = unix.Openat(parentFD, component,
				unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			unix.Close(parentFD)
			return -1, classifyPathError(openErr)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(nextFD, &stat); err != nil {
			unix.Close(nextFD)
			unix.Close(parentFD)
			return -1, err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(nextFD)
			unix.Close(parentFD)
			return -1, ErrUnsafePath
		}
		if err := unix.Fsync(nextFD); err != nil {
			unix.Close(nextFD)
			unix.Close(parentFD)
			return -1, err
		}
		if err := unix.Fsync(parentFD); err != nil {
			unix.Close(nextFD)
			unix.Close(parentFD)
			return -1, err
		}
		unix.Close(parentFD)
		parentFD = nextFD
		if index == len(components)-1 {
			if err := validateDirectoryFD(parentFD); err != nil {
				unix.Close(parentFD)
				return -1, err
			}
		}
	}
	return parentFD, nil
}

func unlinkIfExists(dirFD int, name string) error {
	err := unix.Unlinkat(dirFD, name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func stagePrefix(jobID string) string {
	return contentpath.JobDigest(jobID)
}

func parseStagingPath(path string) (string, string, error) {
	clean := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(clean, ".incoming/") ||
		strings.Count(clean, "/") != 1 {
		return "", "", ErrUnsafePath
	}
	name := strings.TrimPrefix(clean, ".incoming/")
	if !strings.HasSuffix(name, ".stage") {
		return "", "", ErrUnsafePath
	}
	prefix := strings.TrimSuffix(name, ".stage")
	if !validSHA256(prefix) {
		return "", "", ErrUnsafePath
	}
	return prefix, name, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validLowerHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func finalRelativePath(digest string) string {
	return filepath.ToSlash(filepath.Join(
		"sha256", digest[:2], digest[2:], "file.epub"))
}

func classifyPathError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrUnsafePath
	}
	return err
}

func (b Blob) String() string {
	return fmt.Sprintf("%s (%d bytes)", b.SHA256, b.Size)
}
