//go:build linux

package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/catalog"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// bookMetadataStore is the catalog surface one materialization needs.
type bookMetadataStore interface {
	CatalogBookMetadata(context.Context, string, string, store.LibraryRole) (store.BookMetadata, error)
	ApplyCatalogBookMetadata(context.Context, string, store.ApplyBookMetadataRequest) (store.BookMetadata, error)
}

// metadataApplyAttempts bounds the optimistic retry. Each attempt loses only
// to a writer that committed first, so a job that keeps losing is a job the
// next pass can pick up rather than one worth blocking a worker on.
const metadataApplyAttempts = 3

// ErrMetadataSnapshotInvalid reports an extracted metadata snapshot that
// cannot be decoded. The snapshot was written by this server from a parsed
// publication, so an undecodable one is a corrupt row rather than hostile
// input, and retrying decodes it exactly as badly: a scheduler must surface
// it for an operator rather than treat it as a transient failure.
var ErrMetadataSnapshotInvalid = errors.New("content: invalid extracted metadata snapshot")

// MaterializeBookMetadata resolves a promoted job's extracted snapshot and
// library path into the catalog book the job produced.
//
// Sources are applied in precedence order, embedded first: a filename
// outranks the file's own metadata, so applying them the other way round
// would leave the weaker source's provenance on a field the stronger one
// owns. The order also decides a set's row indices, since rows already
// known keep their place and a later proposal's rows are appended.
//
// Applying nothing is a normal outcome. A pass that learned nothing writes
// nothing, so repeated passes over the same job are idempotent and leave the
// book's revision alone.
func MaterializeBookMetadata(
	ctx context.Context,
	st bookMetadataStore,
	job store.IngestJob,
	patterns []metadata.PathPattern,
	clock func() time.Time,
) (store.BookMetadata, bool, error) {
	if st == nil || clock == nil || job.BookID == nil || *job.BookID == "" ||
		job.UserID == "" || job.State != store.IngestPromoted {
		return store.BookMetadata{}, false, store.ErrInvalidTransition
	}
	proposals, err := bookMetadataProposals(job, patterns)
	if err != nil {
		return store.BookMetadata{}, false, err
	}
	if len(proposals) == 0 {
		return store.BookMetadata{}, false, nil
	}

	var lastErr error
	for attempt := 0; attempt < metadataApplyAttempts; attempt++ {
		current, err := st.CatalogBookMetadata(
			ctx, job.UserID, *job.BookID, store.LibraryRoleManage)
		if err != nil {
			return store.BookMetadata{}, false, fmt.Errorf(
				"read catalog metadata for job %q: %w", job.ID, err)
		}
		resolved := current
		changed := false
		for _, proposal := range proposals {
			next, applied := catalog.Resolve(resolved, proposal)
			if applied {
				resolved = next
				changed = true
			}
		}
		if !changed {
			return current, false, nil
		}
		request := store.ApplyBookMetadataRequest{
			Metadata:         resolved,
			ExpectedRevision: current.Book.Revision,
			UpdatedAt:        clock().UTC(),
		}
		if err := store.ValidateApplyBookMetadata(request); err != nil {
			return store.BookMetadata{}, false, fmt.Errorf(
				"resolved metadata for job %q: %w", job.ID, err)
		}
		applied, err := st.ApplyCatalogBookMetadata(ctx, job.UserID, request)
		if err == nil {
			return applied, true, nil
		}
		if !errors.Is(err, store.ErrStaleRevision) {
			return store.BookMetadata{}, false, fmt.Errorf(
				"apply catalog metadata for job %q: %w", job.ID, err)
		}
		lastErr = err
	}
	return store.BookMetadata{}, false, fmt.Errorf(
		"apply catalog metadata for job %q: %w", job.ID, lastErr)
}

// bookMetadataProposals maps one job's durable evidence to proposals in
// precedence order. A job carries a path only when it came from a watched
// library; an upload has no meaningful one, and inventing a layout from the
// original filename alone would be guessing.
//
// A parsed path is used whatever its grade, because FromPath already leaves
// out the values the layout had to guess at. Gating on the grade instead
// would throw away the author a layout read from a directory of its own
// merely because it could not explain the rest of the name. A source left
// with nothing to assert is dropped, whether because a layout guessed every
// field or because a publication declared nothing usable, so it does not
// cost a catalog read that could never produce a write.
func bookMetadataProposals(
	job store.IngestJob, patterns []metadata.PathPattern,
) ([]metadata.Proposal, error) {
	var proposals []metadata.Proposal
	if len(job.ExtractedEmbeddedMetadataJSON) > 0 {
		var embedded epub.Metadata
		if err := json.Unmarshal(job.ExtractedEmbeddedMetadataJSON, &embedded); err != nil {
			return nil, fmt.Errorf("%w: job %q: %v",
				ErrMetadataSnapshotInvalid, job.ID, err)
		}
		if fromFile := metadata.FromEmbedded(embedded); !fromFile.AssertsNothing() {
			proposals = append(proposals, fromFile)
		}
	}
	if job.SourceRelativePath != nil && *job.SourceRelativePath != "" {
		candidate := metadata.ParsePath(*job.SourceRelativePath, patterns)
		if candidate.Confidence != metadata.ConfidenceNone {
			if fromPath := metadata.FromPath(candidate); !fromPath.AssertsNothing() {
				proposals = append(proposals, fromPath)
			}
		}
	}
	return proposals, nil
}
