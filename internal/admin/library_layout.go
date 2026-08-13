package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// libraryLayout shows or sets the filename layouts one library's files are
// read with.
//
// Reading and writing are the same command because an operator setting this
// almost always wants to see what it is now first, and because the list it
// prints is exactly the list it accepts.
func libraryLayout(ctx context.Context, st store.Store, args []string) error {
	if len(args) != 2 && len(args) != 3 {
		return errors.New(
			"usage: library-layout <actor> <library-id> [<layout>[,<layout>...]|default|none]")
	}
	actor, err := st.UserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("actor %q: %w", args[0], err)
	}
	libraryID := args[1]
	if len(args) == 2 {
		return showLibraryLayout(ctx, st, actor, libraryID)
	}
	patterns, err := layoutArgument(args[2])
	if err != nil {
		return err
	}
	// The whole document is rewritten, so it is read under the same role
	// that will write it: an operator who cannot manage the library must
	// not be told what is in its configuration either.
	library, err := libraryToManage(ctx, st, actor, libraryID)
	if err != nil {
		return err
	}
	config, err := metadata.WithPathPatterns(library.Library.ConfigJSON, patterns)
	if err != nil {
		return err
	}
	if err := st.SetLibraryConfig(
		ctx, actor.ID, libraryID, config, time.Now().UTC()); err != nil {
		return libraryLayoutError(err, actor, libraryID)
	}
	effective, err := metadata.PathPatternsFromConfig(config)
	if err != nil {
		return err
	}
	fmt.Printf("library %s now reads filenames as %s\n",
		libraryID, describeLayouts(effective, patterns == nil))
	return nil
}

func showLibraryLayout(
	ctx context.Context, st store.Store, actor store.User, libraryID string,
) error {
	library, err := libraryToManage(ctx, st, actor, libraryID)
	if err != nil {
		return err
	}
	patterns, err := metadata.PathPatternsFromConfig(library.Library.ConfigJSON)
	if err != nil {
		// A configuration this server cannot read is why the library's
		// uploads are not being described, so it is the answer rather than
		// an obstacle to printing one.
		return err
	}
	configured, err := metadata.PathPatternsConfigured(library.Library.ConfigJSON)
	if err != nil {
		return err
	}
	fmt.Printf("library %s reads filenames as %s\n",
		libraryID, describeLayouts(patterns, !configured))
	fmt.Printf("available layouts: %s\n",
		metadata.FormatPathPatterns(metadata.AllPathPatterns()))
	return nil
}

// libraryToManage resolves a library the actor may reconfigure, and says why
// when they may not. The store answers a library that is invisible and one
// the actor only reads with the same ErrNotFound, so the message has to
// cover both.
func libraryToManage(
	ctx context.Context, st store.Store, actor store.User, libraryID string,
) (store.AccessibleLibrary, error) {
	library, err := st.LibraryByID(ctx, actor.ID, libraryID, store.LibraryRoleManage)
	if err != nil {
		return store.AccessibleLibrary{}, libraryLayoutError(err, actor, libraryID)
	}
	return library, nil
}

func libraryLayoutError(err error, actor store.User, libraryID string) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no library %s, or %s cannot manage it",
			libraryID, actor.Name)
	}
	return err
}

// layoutArgument reads the layout list an operator typed. A nil list means
// restore the defaults; an empty non-nil list means read no layouts at all.
func layoutArgument(argument string) ([]metadata.PathPattern, error) {
	switch strings.TrimSpace(argument) {
	case "default":
		return nil, nil
	case "none":
		return []metadata.PathPattern{}, nil
	default:
		patterns, err := metadata.ParsePathPatterns(argument)
		if err != nil {
			return nil, fmt.Errorf("%w\navailable layouts: %s", err,
				metadata.FormatPathPatterns(metadata.AllPathPatterns()))
		}
		return patterns, nil
	}
}

// describeLayouts says what a library does, not what is stored: the three
// answers an operator can get are the built-in defaults, a list they chose,
// and nothing at all, and printing an empty string for the last two would
// make them look identical.
func describeLayouts(patterns []metadata.PathPattern, byDefault bool) string {
	if len(patterns) == 0 {
		return "no layouts (filenames are not read)"
	}
	list := metadata.FormatPathPatterns(patterns)
	if byDefault {
		return list + " (the default)"
	}
	return list
}
