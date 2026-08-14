package admin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/store"
)

// addLibrary registers an existing directory as a root-backed library.
//
// The three axes ADR-0014 separates are three flags here. Defaults
// follow the source: a plain directory is copied into content-addressed
// storage on the terms an upload gets, a Calibre library is read where
// it lies because copying somebody's whole Calibre tree a second time is
// the cost this exists to avoid.
//
// The root is checked here, at the moment an administrator names it,
// rather than left for the first sweep to complain about in a log nobody
// is reading. A typo in a path is the likeliest thing to go wrong with
// this command and the cheapest to catch.
//
// The path is stored absolute. A relative one would resolve against
// whatever directory the server was started from, which is not the
// directory the administrator was standing in when they typed it.
func addLibrary(ctx context.Context, st store.Store, args []string) error {
	const usage = "usage: add-library [-source directory|calibre] " +
		"[-storage cas|in-place] [-refresh manual|interval] " +
		"[-interval <duration>] <owner> <name> <root>"
	source := store.LibraryDirectory
	storage := store.LibraryStorage("")
	refresh := store.LibraryRefreshInterval
	interval := store.DefaultRefreshInterval
	var rest []string
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if !strings.HasPrefix(flag, "-") {
			rest = append(rest, flag)
			continue
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s requires a value", flag)
		}
		value := strings.TrimSpace(args[i+1])
		i++
		switch flag {
		case "-source":
			source = store.LibrarySource(value)
			if source == store.LibraryManaged || !source.Valid() {
				return fmt.Errorf(
					"-source must be directory or calibre, got %q", value)
			}
		case "-storage":
			// The wire and the column spell it in_place; a person
			// typing it at a shell reaches for the hyphen.
			storage = store.LibraryStorage(
				strings.ReplaceAll(value, "-", "_"))
			if !storage.Valid() {
				return fmt.Errorf(
					"-storage must be cas or in-place, got %q", value)
			}
		case "-refresh":
			refresh = store.LibraryRefresh(value)
			if !refresh.Valid() {
				return fmt.Errorf(
					"-refresh must be manual or interval, got %q", value)
			}
		case "-interval":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("-interval %q: %w", value, err)
			}
			if d < time.Minute {
				return errors.New("-interval must be at least 1m")
			}
			interval = d
		default:
			return fmt.Errorf("unknown flag %q\n%s", flag, usage)
		}
	}
	if len(rest) != 3 {
		return errors.New(usage)
	}
	if storage == "" {
		storage = store.LibraryStorageCAS
		if source == store.LibraryCalibre {
			storage = store.LibraryStorageInPlace
		}
	}
	u, err := st.UserByName(ctx, rest[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(rest[1])
	if err := ValidateLibraryName(name); err != nil {
		return err
	}
	root, err := libraryRoot(rest[2])
	if err != nil {
		return err
	}
	if source == store.LibraryCalibre {
		if _, err := os.Stat(filepath.Join(root, "metadata.db")); err != nil {
			return fmt.Errorf(
				"%q holds no metadata.db, so it is not a Calibre library: %w",
				root, err)
		}
	}
	if refresh != store.LibraryRefreshInterval {
		interval = 0
	}
	lib := store.Library{
		ID:          uuid.New().String(),
		OwnerUserID: u.ID,
		// The owner pays for the CAS snapshots taken from the root, on
		// the same terms as an upload (ADR-0002). An in-place library
		// copies nothing, so it charges nothing.
		QuotaUserID:     u.ID,
		Source:          source,
		Storage:         storage,
		Refresh:         refresh,
		RefreshInterval: interval,
		Name:            name,
		RootPath:        &root,
		CreatedAt:       time.Now().UTC(),
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		return err
	}
	fmt.Printf("created %s library %q (id %s) owned by %s over %s\n",
		lib.Source, lib.Name, lib.ID, u.Name, root)
	fmt.Printf("storage: %s; refresh: %s\n", lib.Storage, refreshDescription(lib))
	fmt.Println(
		"the server reads this directory and never writes, renames or " +
			"deletes anything below it")
	return nil
}

// refreshDescription spells a library's refresh policy for a terminal.
func refreshDescription(l store.Library) string {
	if l.Refresh == store.LibraryRefreshInterval {
		return "every " + l.RefreshInterval.String()
	}
	return "manual"
}

// libraryRoot resolves and checks the directory a root-backed library covers.

func libraryRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("library root must not be blank")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve library root %q: %w", trimmed, err)
	}
	// Stat rather than Lstat: a root that is a symlink to a directory is
	// a perfectly ordinary way to name one, and the scanner opens the
	// root by path anyway. The symlink policy is about entries *inside*
	// the tree, which are the ones that could be a second path to a file
	// or a loop.
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("library root %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("library root %q is not a directory", absolute)
	}
	// An unreadable root would produce a library that never scans and a
	// log line every interval, so it is refused while somebody is still
	// looking at the terminal.
	if _, err := os.OpenRoot(absolute); err != nil {
		return "", fmt.Errorf("library root %q is not readable: %w", absolute, err)
	}
	return absolute, nil
}
