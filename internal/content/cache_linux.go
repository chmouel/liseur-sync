//go:build linux

package content

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// The errors below describe what went wrong reading somebody else's
// directory, which is the only kind of directory this package reads.
var (
	// ErrStageMissing says the file that should be at a recorded path is
	// not there. It is the ordinary answer for a book whose file moved
	// between one pass and one request, not a failure to repair.
	ErrStageMissing = errors.New("content: file is missing")
	// ErrUnsafePath refuses a path or a file type this server will not
	// touch: an absolute path, a parent reference, a symlink, a device.
	ErrUnsafePath = errors.New("content: unsafe path or file type")
	// ErrSourceChanged says the bytes at a recorded path are not the
	// bytes that were recorded. It is not a corruption to repair: it is
	// a file its owner changed, and the next pass re-reads it.
	ErrSourceChanged = errors.New("content: file changed since it was scanned")
	// ErrRootMissing says a folder's root is not usable, so nothing
	// beneath it can be read right now — which says nothing at all about
	// whether those files still exist.
	ErrRootMissing = errors.New("content: folder root is unavailable")
)

// MaxCoverBytes bounds a curated cover.jpg. A cover is a thumbnail; a
// file larger than this is not one, and reading it would let a request
// pull an arbitrary amount of somebody's disk through this server.
const MaxCoverBytes = 16 << 20

// Cache is the one directory this server owns.
//
// Everything in it is derived and disposable: a cover in it can be
// rendered again from the book it came from. Deleting the whole
// directory while the server is running costs a re-render and nothing
// else, which is the property that makes it a cache rather than storage
// (ADR-0017).
type Cache struct {
	root   string
	rootFD int
}

// OpenCache opens or creates the cache directory. The mode check is
// deliberate: covers are pictures of what people are reading, so a
// world-readable cache directory is refused rather than warned about.
func OpenCache(root string) (*Cache, error) {
	if root == "" {
		return nil, ErrUnsafePath
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrUnsafePath
	}
	rootFD, err := openOrCreateRoot(absolute)
	if err != nil {
		return nil, err
	}
	return &Cache{root: absolute, rootFD: rootFD}, nil
}

func (c *Cache) Close() error {
	if c == nil || c.rootFD < 0 {
		return nil
	}
	err := unix.Close(c.rootFD)
	c.rootFD = -1
	return err
}

// Root reports the cache directory, for the startup log.
func (c *Cache) Root() string { return c.root }

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
			if errors.Is(openErr, unix.ENOTDIR) {
				return -1, fmt.Errorf(
					"%w: %q on the way to content root %q is not a directory",
					ErrUnsafePath, component, path)
			}
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
			if err := validateRootFD(parentFD, path); err != nil {
				unix.Close(parentFD)
				return -1, err
			}
		}
	}
	return parentFD, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func classifyPathError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrUnsafePath
	}
	return err
}

func validateRootFD(fd int, path string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	switch {
	case stat.Mode&unix.S_IFMT != unix.S_IFDIR:
		return fmt.Errorf("%w: content root %q is not a directory",
			ErrUnsafePath, path)
	case stat.Uid != uint32(os.Geteuid()):
		return fmt.Errorf(
			"%w: cache directory %q is owned by uid %d, not the uid %d "+
				"running this server; chown it",
			ErrUnsafePath, path, stat.Uid, os.Geteuid())
	case stat.Mode&0o077 != 0:
		return fmt.Errorf(
			"%w: cache directory %q is mode %04o and readable by other "+
				"users, which would show them what everybody here is "+
				"reading; chmod 700 it",
			ErrUnsafePath, path, stat.Mode&0o777)
	}
	return nil
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

func unlinkIfExists(dirFD int, name string) error {
	err := unix.Unlinkat(dirFD, name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
