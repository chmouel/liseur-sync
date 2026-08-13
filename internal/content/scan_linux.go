//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// ErrRootUnavailable reports a watched root that could not be opened at
// all. It is deliberately distinct from every per-entry failure: a root
// that is not there says nothing about the files it used to hold, and a
// caller that cannot tell the two apart would mark a whole library missing
// because a network mount was slow to come back.
var ErrRootUnavailable = errors.New("content: watched root is unavailable")

// scanDefaults bound a traversal so one pathological tree cannot occupy a
// worker indefinitely. They are generous rather than tuned: the point is
// that a sweep terminates, not that it terminates at a particular size.
const (
	defaultMaxScanFiles = 200_000
	defaultMaxScanDepth = 32
)

// publicationExtension is the only file extension a sweep collects.
// Ingest re-derives the media type from the bytes, so this is a cheap
// filter to avoid staging every file in a user's home directory, not a
// statement that the file is a valid EPUB.
const publicationExtension = ".epub"

// ScanLimits bound one traversal.
type ScanLimits struct {
	MaxFiles int
	MaxDepth int
}

func (l ScanLimits) withDefaults() ScanLimits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaultMaxScanFiles
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaultMaxScanDepth
	}
	return l
}

// ScannedFile is one publication a traversal found, described exactly as
// the filesystem described it.
type ScannedFile struct {
	// RelativePath is slash-separated and relative to the watched root, so
	// it is the same string on every platform and is what the catalog
	// stores.
	RelativePath string
	SizeBytes    int64
	ModifiedAt   time.Time
}

// ScanReport is one traversal's outcome.
//
// Complete is the whole point of this type. Only a completed full sweep
// may conclude that an unlisted path is gone, so a traversal that hit a
// limit, was cancelled, or could not read a directory reports false and
// the reconciler declines to mark anything absent. Everything else in the
// report is diagnostics.
type ScanReport struct {
	Files []ScannedFile
	// Complete reports that every directory beneath the root was read to
	// the end. It is false whenever any part of the tree was not visited,
	// for any reason.
	Complete bool
	// Symlinks counts entries skipped by the symlink policy. Staying
	// beneath a root is not by itself a decision about symlinks, so they
	// are skipped explicitly and counted, rather than silently ignored.
	Symlinks int
	// Unreadable counts directories a traversal could not list. Each one
	// clears Complete.
	Unreadable int
	// Skipped counts entries that were neither directories nor
	// publications: other file types, and anything that vanished between
	// being listed and being stat'ed.
	Skipped int
}

// ScanWatchedRoot walks a watched root and reports the publications it
// holds.
//
// Every operation is descriptor-relative: each directory is entered by
// opening a new os.Root from its parent, so the traversal cannot be walked
// out of the tree by a rename between two steps, and an ancestor swapped
// mid-scan leaves the traversal on the directory it was already inside
// rather than following the name to a new one.
//
// Nothing below the root is written, opened for writing, or modified. The
// only filesystem calls made here are directory reads and lstats.
//
// Symlinks are skipped by explicit policy rather than followed and
// bounds-checked. os.Root would refuse one leaving the tree, but one
// pointing back inside it is still a second path to a file the sweep
// already has, and a loop of them is a traversal that does not terminate.
func ScanWatchedRoot(
	ctx context.Context, rootPath string, limits ScanLimits,
) (ScanReport, error) {
	var report ScanReport
	if rootPath == "" {
		return report, ErrRootUnavailable
	}
	limits = limits.withDefaults()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return report, fmt.Errorf("%w: %s: %v", ErrRootUnavailable, rootPath, err)
	}
	defer root.Close()

	report.Complete = true
	if err := scanDirectory(ctx, root, "", 0, limits, &report); err != nil {
		return report, err
	}
	// Ordering is not required for correctness — reconciliation is keyed
	// by path — but a deterministic sweep makes a diff between two runs
	// mean something, and makes a test that compares reports possible.
	slices.SortFunc(report.Files, func(a, b ScannedFile) int {
		return strings.Compare(a.RelativePath, b.RelativePath)
	})
	return report, nil
}

// scanDirectory reads one directory and descends into its subdirectories.
// It returns an error only for cancellation; every filesystem failure is
// recorded as incompleteness, because a sweep that gives up on the first
// unreadable directory reports nothing about the rest of the library.
func scanDirectory(
	ctx context.Context,
	dir *os.Root,
	prefix string,
	depth int,
	limits ScanLimits,
	report *ScanReport,
) error {
	if err := ctx.Err(); err != nil {
		report.Complete = false
		return err
	}
	if depth > limits.MaxDepth {
		report.Complete = false
		return nil
	}
	handle, err := dir.Open(".")
	if err != nil {
		report.Complete = false
		report.Unreadable++
		return nil
	}
	entries, err := handle.ReadDir(-1)
	handle.Close()
	if err != nil {
		report.Complete = false
		report.Unreadable++
		return nil
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			report.Complete = false
			return err
		}
		name := entry.Name()
		// Lstat rather than the DirEntry's own type: ReadDir may report
		// an unknown type on some filesystems, and a symlink must be
		// recognised as one before anything else looks at it.
		info, err := dir.Lstat(name)
		if err != nil {
			// A file removed between being listed and being stat'ed is
			// ordinary in a directory somebody else owns, and it is not
			// evidence of anything: the next sweep decides.
			report.Complete = false
			report.Skipped++
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			report.Symlinks++
		case info.IsDir():
			child, err := dir.OpenRoot(name)
			if err != nil {
				report.Complete = false
				report.Unreadable++
				continue
			}
			err = scanDirectory(
				ctx, child, path.Join(prefix, name), depth+1, limits, report)
			child.Close()
			if err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if !isPublicationName(name) {
				report.Skipped++
				continue
			}
			if len(report.Files) >= limits.MaxFiles {
				report.Complete = false
				return nil
			}
			report.Files = append(report.Files, ScannedFile{
				RelativePath: path.Join(prefix, name),
				SizeBytes:    info.Size(),
				ModifiedAt:   info.ModTime().UTC(),
			})
		default:
			// Devices, sockets and fifos. Opening one can block forever,
			// which is reason enough on a semi-trusted root.
			report.Skipped++
		}
	}
	return nil
}

func isPublicationName(name string) bool {
	return strings.EqualFold(path.Ext(name), publicationExtension)
}
