package admin

// The folder operations the admin panel and the CLI share (ADR-0013).
// As with users.go, the rules — what a folder may be called, what counts
// as a usable root, how its kind is decided — live here once, so a
// folder added from a browser is the same row as one added from a shell.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// MaxFolderNameLength bounds a name so that it fits a table cell.
const MaxFolderNameLength = 120

// Errors both surfaces render.
var (
	ErrFolderNameEmpty   = errors.New("a folder name is required")
	ErrFolderNameTooLong = fmt.Errorf(
		"a folder name may be at most %d characters", MaxFolderNameLength)
	ErrFolderRootNotAllowed = errors.New(
		"that path is not below any of the roots this server may serve")
)

// ValidateFolderName is the one definition of an acceptable name. It is
// looser than a user name — a folder's title is prose, and it is never a
// path component — but it is neither blank nor unbounded.
func ValidateFolderName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return ErrFolderNameEmpty
	case len(name) > MaxFolderNameLength:
		return ErrFolderNameTooLong
	}
	return nil
}

// NewFolder registers an existing directory as a watched folder.
//
// The root is checked here, at the moment somebody names it, rather than
// left for the first pass to complain about in a log nobody is reading.
// A typo in a path is the likeliest thing to go wrong and the cheapest
// to catch.
//
// The path is stored absolute. A relative one would resolve against
// whatever directory the server was started from, which is not the
// directory the administrator was standing in when they typed it.
//
// The kind is detected rather than asked for: a tree holding metadata.db
// is a Calibre folder and everything else is a plain one. There is
// nothing a person could usefully tell the server here that the disk
// does not already say.
func NewFolder(
	ctx context.Context, st store.Store, name, root string, allowed []string,
) (store.Folder, error) {
	if err := ValidateFolderName(name); err != nil {
		return store.Folder{}, err
	}
	absolute, err := ResolveFolderRoot(root)
	if err != nil {
		return store.Folder{}, err
	}
	if !RootAllowed(absolute, allowed) {
		return store.Folder{}, ErrFolderRootNotAllowed
	}
	now := time.Now().UTC()
	folder := store.Folder{
		ID:        uuid.New().String(),
		Name:      strings.TrimSpace(name),
		RootPath:  absolute,
		Kind:      DetectFolderKind(absolute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateFolder(ctx, folder); err != nil {
		return store.Folder{}, err
	}
	return folder, nil
}

// DetectFolderKind reads what a root already says about itself.
func DetectFolderKind(root string) store.FolderKind {
	if content.IsCalibreFolder(root) {
		return store.FolderCalibre
	}
	return store.FolderPlain
}

// RootAllowed reports whether a resolved root is one the operator meant
// this server to serve. An empty allowlist means "anywhere the server
// can read", which is the default and what the subcommand has always
// permitted; configuring content.folder_roots turns the admin form from
// a filesystem-wide oracle into a choice among trees already intended.
func RootAllowed(root string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if root == candidate {
			return true
		}
		if strings.HasPrefix(root, candidate+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ResolveFolderRoot resolves and checks the directory a folder covers,
// and is the one definition of an acceptable root: both the subcommand
// and the panel go through it, so a path one accepts is a path the other
// accepts.
func ResolveFolderRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("a folder root must not be blank")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve folder root %q: %w", trimmed, err)
	}
	// Stat rather than Lstat: a root that is a symlink to a directory is
	// a perfectly ordinary way to name one, and a pass opens the root by
	// path anyway. The symlink policy is about entries *inside* the
	// tree, which are the ones that could be a second path to a file or
	// a loop.
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("folder root %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("folder root %q is not a directory", absolute)
	}
	// An unreadable root would produce a folder that never scans and a
	// log line every half hour, so it is refused while somebody is still
	// looking at the terminal.
	if _, err := os.OpenRoot(absolute); err != nil {
		return "", fmt.Errorf("folder root %q is not readable: %w", absolute, err)
	}
	return absolute, nil
}

// addFolder registers an existing directory as a watched folder.
func addFolder(ctx context.Context, st store.Store, args []string) error {
	const usage = "usage: add-folder <name> <root>"
	if len(args) != 2 {
		return errors.New(usage)
	}
	// The subcommand is run by whoever can run the binary, so it is not
	// bound by the config allowlist the browser form is: that allowlist
	// exists to stop an administrator's session from naming any path on
	// the machine, not to stop the operator from doing so at a shell.
	folder, err := NewFolder(ctx, st, args[0], args[1], nil)
	if err != nil {
		return err
	}
	fmt.Printf("watching %s folder %q (id %s) at %s\n",
		folder.Kind, folder.Name, folder.ID, folder.RootPath)
	fmt.Println(
		"the server reads this directory and never writes, renames or " +
			"deletes anything below it")
	return nil
}

// listFolders prints what this server watches.
func listFolders(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: list-folders")
	}
	var after string
	for {
		folders, err := st.ListFolders(ctx, after, 100)
		if err != nil {
			return err
		}
		if len(folders) == 0 {
			return nil
		}
		for _, folder := range folders {
			fmt.Printf("%s\t%s\t%s\t%s\n",
				folder.ID, folder.Kind, folder.Name, folder.RootPath)
		}
		if len(folders) < 100 {
			return nil
		}
		after = store.FolderCursor(folders[len(folders)-1])
	}
}

// removeFolder forgets a folder and everything catalogued from it.
//
// Nothing under the root is touched: the books are on somebody's disk
// and stay there. What goes is this server's record of them, which is
// the only thing it ever owned.
func removeFolder(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: remove-folder <folder-id>")
	}
	folderID := strings.TrimSpace(args[0])
	folder, err := st.FolderByID(ctx, folderID)
	if err != nil {
		return err
	}
	if err := st.DeleteFolder(ctx, folderID); err != nil {
		return err
	}
	fmt.Printf("stopped watching %q (%s)\n", folder.Name, folder.RootPath)
	fmt.Println("nothing under that directory was changed")
	return nil
}
