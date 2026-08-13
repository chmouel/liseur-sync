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

// watchLibrary registers an existing directory as a watched library.
//
// The root is checked here, at the moment an administrator names it,
// rather than left for the first sweep to complain about in a log nobody
// is reading. A typo in a path is the likeliest thing to go wrong with
// this command and the cheapest to catch.
//
// The path is stored absolute. A relative one would resolve against
// whatever directory the server was started from, which is not the
// directory the administrator was standing in when they typed it.
func watchLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 3 {
		return errors.New("usage: watch-library <owner> <name> <root>")
	}
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(args[1])
	if name == "" {
		return errors.New("library name must not be blank")
	}
	root, err := watchedRoot(args[2])
	if err != nil {
		return err
	}
	lib := store.Library{
		ID:          uuid.New().String(),
		OwnerUserID: u.ID,
		// The owner pays for the CAS snapshots taken from the root, on
		// the same terms as an upload (ADR-0002).
		QuotaUserID: u.ID,
		Kind:        store.LibraryWatched,
		Name:        name,
		RootPath:    &root,
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		return err
	}
	fmt.Printf("created watched library %q (id %s) owned by %s over %s\n",
		lib.Name, lib.ID, u.Name, root)
	fmt.Println(
		"the server reads this directory and never writes, renames or " +
			"deletes anything below it")
	return nil
}

// watchedRoot resolves and checks the directory a watched library covers.
func watchedRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("watched root must not be blank")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve watched root %q: %w", trimmed, err)
	}
	// Stat rather than Lstat: a root that is a symlink to a directory is
	// a perfectly ordinary way to name one, and the scanner opens the
	// root by path anyway. The symlink policy is about entries *inside*
	// the tree, which are the ones that could be a second path to a file
	// or a loop.
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("watched root %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("watched root %q is not a directory", absolute)
	}
	// An unreadable root would produce a library that never scans and a
	// log line every interval, so it is refused while somebody is still
	// looking at the terminal.
	if _, err := os.OpenRoot(absolute); err != nil {
		return "", fmt.Errorf("watched root %q is not readable: %w", absolute, err)
	}
	return absolute, nil
}

