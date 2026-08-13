//go:build linux

package content

import (
	"context"
	"fmt"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// PatternResolver answers which filename layouts one library's files should
// be read with. A pass asks per job because two libraries on one server can
// be organized differently, which is the whole point of configuring it.
type PatternResolver interface {
	PatternsFor(ctx context.Context, userID, libraryID string) ([]metadata.PathPattern, error)
}

// libraryConfigReader is the catalog surface a resolver needs. It takes the
// job's own user and the read role, so resolving a layout stays inside the
// same ACL that let the job be created.
type libraryConfigReader interface {
	LibraryByID(context.Context, string, string, store.LibraryRole) (store.AccessibleLibrary, error)
}

// LibraryPatterns resolves layouts from each library's stored
// configuration.
//
// It holds no cache of its own. A worker constructs one per pass and the
// pass memoizes within its batch, so an operator who changes a library's
// layout sees the next tick use it rather than the next restart.
type LibraryPatterns struct {
	store libraryConfigReader
}

// NewLibraryPatterns reads layouts from the catalog.
func NewLibraryPatterns(reader libraryConfigReader) *LibraryPatterns {
	return &LibraryPatterns{store: reader}
}

// PatternsFor reads one library's configured layouts.
//
// A library that cannot be read, or one whose configuration cannot be
// parsed, is an error rather than a quiet fall back to the defaults:
// parsing a library with the wrong layout writes wrong authors and series
// onto books, and nothing afterwards lists the books that were misfiled.
func (p *LibraryPatterns) PatternsFor(
	ctx context.Context, userID, libraryID string,
) ([]metadata.PathPattern, error) {
	if p == nil || p.store == nil {
		return nil, store.ErrInvalidTransition
	}
	library, err := p.store.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead)
	if err != nil {
		return nil, fmt.Errorf("read library %q: %w", libraryID, err)
	}
	patterns, err := metadata.PathPatternsFromConfig(library.Library.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("library %q: %w", libraryID, err)
	}
	return patterns, nil
}

// FixedPatterns resolves every library to the same layouts. It is what a
// caller with no catalog to consult uses, and what the built-in defaults
// are for a server that never configures a library.
type FixedPatterns []metadata.PathPattern

// PatternsFor returns the fixed list.
func (f FixedPatterns) PatternsFor(
	context.Context, string, string,
) ([]metadata.PathPattern, error) {
	return f, nil
}

// memoPatterns caches one pass's answers. A batch is frequently one
// library's backlog, and resolving it once per job would read the same row
// as many times as there are files.
type memoPatterns struct {
	inner PatternResolver
	seen  map[string][]metadata.PathPattern
}

func memoize(inner PatternResolver) *memoPatterns {
	return &memoPatterns{inner: inner, seen: map[string][]metadata.PathPattern{}}
}

// PatternsFor keys the cache by user and library together, because the
// lookup is ACL-scoped: the same library id read by two users is two
// questions, and one answer must never stand in for the other.
func (m *memoPatterns) PatternsFor(
	ctx context.Context, userID, libraryID string,
) ([]metadata.PathPattern, error) {
	key := userID + "\x00" + libraryID
	if patterns, ok := m.seen[key]; ok {
		return patterns, nil
	}
	patterns, err := m.inner.PatternsFor(ctx, userID, libraryID)
	if err != nil {
		return nil, err
	}
	m.seen[key] = patterns
	return patterns, nil
}
