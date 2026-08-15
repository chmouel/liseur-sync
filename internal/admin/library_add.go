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

// RootLibraryOptions is how a root-backed library is described, on the
// three axes ADR-0014 separates: where its books come from, where their
// bytes live, and how often the source is read again. Both surfaces —
// the `add-library` subcommand and the admin panel's form — fill this
// in and hand it to NewRootLibrary, so a library made from a browser is
// the same row as one made from a shell.
type RootLibraryOptions struct {
	Source   store.LibrarySource
	Storage  store.LibraryStorage
	Refresh  store.LibraryRefresh
	Interval time.Duration
}

// Errors both surfaces render when the axes do not make sense.
var (
	ErrSourceNotRootBacked = errors.New(
		"a root-backed library is a directory or a Calibre library")
	ErrStorageInvalid = errors.New("storage is cas or in-place")
	ErrRefreshInvalid = errors.New("refresh is manual or on an interval")
	ErrIntervalTooShort = errors.New(
		"a refresh interval of less than a minute would sweep the disk "+
			"more often than it can finish")
)

// Normalize fills in the defaults and refuses the combinations that are
// not libraries. Defaults follow the source: a plain directory is
// copied into content-addressed storage on the terms an upload gets, a
// Calibre library is read where it lies because copying somebody's
// whole Calibre tree a second time is the cost this exists to avoid.
func (o *RootLibraryOptions) Normalize() error {
	if o.Source == "" {
		o.Source = store.LibraryDirectory
	}
	if !o.Source.RootBacked() {
		return ErrSourceNotRootBacked
	}
	if o.Storage == "" {
		o.Storage = store.LibraryStorageCAS
		if o.Source == store.LibraryCalibre {
			o.Storage = store.LibraryStorageInPlace
		}
	}
	if !o.Storage.Valid() {
		return ErrStorageInvalid
	}
	if o.Refresh == "" {
		o.Refresh = store.LibraryRefreshInterval
	}
	if !o.Refresh.Valid() {
		return ErrRefreshInvalid
	}
	if o.Refresh != store.LibraryRefreshInterval {
		o.Interval = 0
		return nil
	}
	if o.Interval == 0 {
		o.Interval = store.DefaultRefreshInterval
	}
	if o.Interval < time.Minute {
		return ErrIntervalTooShort
	}
	return nil
}

// NewRootLibrary registers an existing directory as a root-backed
// library owned by ownerUserID.
//
// The root is checked here, at the moment an administrator names it,
// rather than left for the first sweep to complain about in a log nobody
// is reading. A typo in a path is the likeliest thing to go wrong and
// the cheapest to catch.
//
// The path is stored absolute. A relative one would resolve against
// whatever directory the server was started from, which is not the
// directory the administrator was standing in when they typed it.
func NewRootLibrary(
	ctx context.Context,
	st store.Store,
	ownerUserID, name, root string,
	opts RootLibraryOptions,
) (store.Library, error) {
	if err := opts.Normalize(); err != nil {
		return store.Library{}, err
	}
	if err := ValidateLibraryName(name); err != nil {
		return store.Library{}, err
	}
	absolute, err := ResolveLibraryRoot(root)
	if err != nil {
		return store.Library{}, err
	}
	if err := CheckLibraryRoot(absolute, opts.Source); err != nil {
		return store.Library{}, err
	}
	lib := store.Library{
		ID:          uuid.New().String(),
		OwnerUserID: ownerUserID,
		// The owner pays for the CAS snapshots taken from the root, on
		// the same terms as an upload (ADR-0002). An in-place library
		// copies nothing, so it charges nothing.
		QuotaUserID:     ownerUserID,
		Source:          opts.Source,
		Storage:         opts.Storage,
		Refresh:         opts.Refresh,
		RefreshInterval: opts.Interval,
		Name:            strings.TrimSpace(name),
		RootPath:        &absolute,
		CreatedAt:       time.Now().UTC(),
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		return store.Library{}, err
	}
	return lib, nil
}

// DetectLibrarySource guesses the source of a root that has already
// resolved to a readable directory: a tree holding metadata.db is a
// Calibre library, anything else a plain directory. It is the guess
// that lets the admin panel's form skip asking; CheckLibraryRoot stays
// the proof, and the form keeps an override for when the guess is
// wrong.
func DetectLibrarySource(root string) store.LibrarySource {
	if st, err := os.Lstat(filepath.Join(root, "metadata.db")); err == nil &&
		st.Mode().IsRegular() {
		return store.LibraryCalibre
	}
	return store.LibraryDirectory
}

// CheckLibraryRoot is the source-specific half of naming a root: a
// Calibre library that holds no metadata.db is a directory somebody
// pointed at by mistake, and saying so now is cheaper than a refresh
// that fails every interval.
func CheckLibraryRoot(root string, source store.LibrarySource) error {
	if source != store.LibraryCalibre {
		return nil
	}
	metadataDB := filepath.Join(root, "metadata.db")
	st, err := os.Lstat(metadataDB)
	if err != nil {
		return fmt.Errorf(
			"%q holds no metadata.db, so it is not a Calibre library", root)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf(
			"%q has a metadata.db that is not a regular file, so it is not a Calibre library",
			root)
	}
	return nil
}

// addLibrary registers an existing directory as a root-backed library.
func addLibrary(ctx context.Context, st store.Store, args []string) error {
	const usage = "usage: add-library [-source directory|calibre] " +
		"[-storage cas|in-place] [-refresh manual|interval] " +
		"[-interval <duration>] <owner> <name> <root>"
	var opts RootLibraryOptions
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
			opts.Source = store.LibrarySource(value)
			if !opts.Source.RootBacked() {
				return fmt.Errorf(
					"-source must be directory or calibre, got %q", value)
			}
		case "-storage":
			// The wire and the column spell it in_place; a person
			// typing it at a shell reaches for the hyphen.
			opts.Storage = store.LibraryStorage(
				strings.ReplaceAll(value, "-", "_"))
			if !opts.Storage.Valid() {
				return fmt.Errorf(
					"-storage must be cas or in-place, got %q", value)
			}
		case "-refresh":
			opts.Refresh = store.LibraryRefresh(value)
			if !opts.Refresh.Valid() {
				return fmt.Errorf(
					"-refresh must be manual or interval, got %q", value)
			}
		case "-interval":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("-interval %q: %w", value, err)
			}
			opts.Interval = d
		default:
			return fmt.Errorf("unknown flag %q\n%s", flag, usage)
		}
	}
	if len(rest) != 3 {
		return errors.New(usage)
	}
	u, err := st.UserByName(ctx, rest[0])
	if err != nil {
		return err
	}
	lib, err := NewRootLibrary(ctx, st, u.ID, rest[1], rest[2], opts)
	if err != nil {
		return err
	}
	fmt.Printf("created %s library %q (id %s) owned by %s over %s\n",
		lib.Source, lib.Name, lib.ID, u.Name, *lib.RootPath)
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

// ResolveLibraryRoot resolves and checks the directory a root-backed
// library covers, and is the one definition of an acceptable root: both
// the subcommand and the panel go through it, so a path one accepts is
// a path the other accepts.
func ResolveLibraryRoot(path string) (string, error) {
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

// refreshLibrary queues a refresh of one library.
//
// It queues rather than sweeps: the work belongs to the running server,
// which holds the claim that stops two refreshes of one root, and a CLI
// that walked the tree itself would be a second implementation of the
// scan with none of that. The command therefore returns immediately, and
// what it reports is that the request was recorded.
func refreshLibrary(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: refresh-library <actor> <library-id>")
	}
	actor, err := st.UserByName(ctx, args[0])
	if err != nil {
		return err
	}
	libraryID := strings.TrimSpace(args[1])
	lib, err := st.AdminLibraryByID(ctx, libraryID)
	if err != nil {
		return err
	}
	if lib.RootPath == nil || *lib.RootPath == "" {
		return fmt.Errorf(
			"library %q is %s and has no root to refresh", lib.Name, lib.Source)
	}
	now := time.Now().UTC()
	if err := st.AdminRequestLibraryRefresh(
		ctx, actor.ID, libraryID, now); err != nil {
		return err
	}
	fmt.Printf("queued a refresh of %q (%s)\n", lib.Name, *lib.RootPath)
	fmt.Println(
		"the running server picks it up on its next refresh tick; if no " +
			"server is running, it is picked up when one starts")
	return nil
}
