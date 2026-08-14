package admin

// The library operations the admin panel and the CLI share (ADR-0013).
// As with users.go, the rules — what a library may be called, what
// "no layout" means, who may not be granted access — live here once.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/google/uuid"
)

// MaxLibraryNameLength bounds a name so that it fits a table cell.
const MaxLibraryNameLength = 120

// Errors both surfaces render.
var (
	ErrLibraryNameEmpty   = errors.New("a library name is required")
	ErrLibraryNameTooLong = fmt.Errorf(
		"a library name may be at most %d characters", MaxLibraryNameLength)
	ErrGrantToOwner = errors.New(
		"the owner already has full access to this library")
	ErrWatchedLibraryFromUI = errors.New(
		"a watched library names a path on the server and is created with " +
			"the watch-library subcommand")
)

// ValidateLibraryName is the one definition of an acceptable name. It
// is looser than a user name — a library title is prose, and it is
// never a path component — but it is neither blank nor unbounded.
func ValidateLibraryName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return ErrLibraryNameEmpty
	case len(name) > MaxLibraryNameLength:
		return ErrLibraryNameTooLong
	}
	return nil
}

// NewManagedLibrary makes a managed library owned by ownerUserID. A
// managed library names no path on the server, which is the whole
// reason it is safe to create from a browser.
func NewManagedLibrary(ctx context.Context, st store.Store, ownerUserID, name string) (store.Library, error) {
	if err := ValidateLibraryName(name); err != nil {
		return store.Library{}, err
	}
	lib := store.Library{
		ID:          uuid.New().String(),
		OwnerUserID: ownerUserID,
		// The owner pays for what the library holds, including bytes
		// uploaded by others they grant access to (ADR-0002).
		QuotaUserID: ownerUserID,
		Kind:        store.LibraryManaged,
		Name:        strings.TrimSpace(name),
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		return store.Library{}, err
	}
	return lib, nil
}

// SetLibraryAccessAsAdmin grants a role on any library, or revokes when
// role is nil, without the acting administrator holding manage on it.
func SetLibraryAccessAsAdmin(ctx context.Context, st store.Store, actorUserID, libraryID, userID string, role *store.LibraryRole) error {
	return st.AdminSetLibraryAccess(
		ctx, actorUserID, libraryID, userID, role, time.Now().UTC())
}

// SetLibraryLayoutAsAdmin replaces the filename layouts one library is
// read with. A nil patterns means "back to the default", which is not
// the same as an empty list and is why the argument is a slice rather
// than a string.
//
// The document is read from the record the caller already has and
// rewritten whole, so a key this server does not know about survives
// the edit rather than being dropped by a form that never showed it.
func SetLibraryLayoutAsAdmin(ctx context.Context, st store.Store, actorUserID string, lib store.Library, patterns []metadata.PathPattern) error {
	config, err := metadata.WithPathPatterns(lib.ConfigJSON, patterns)
	if err != nil {
		return err
	}
	return st.AdminSetLibraryConfig(
		ctx, actorUserID, lib.ID, config, time.Now().UTC())
}

// LibraryLayouts describes how one library's filenames are read: the
// effective patterns, and whether that is a configured choice or the
// default.
func LibraryLayouts(lib store.Library) (patterns []metadata.PathPattern, configured bool, err error) {
	patterns, err = metadata.PathPatternsFromConfig(lib.ConfigJSON)
	if err != nil {
		return nil, false, err
	}
	configured, err = metadata.PathPatternsConfigured(lib.ConfigJSON)
	if err != nil {
		return nil, false, err
	}
	return patterns, configured, nil
}
