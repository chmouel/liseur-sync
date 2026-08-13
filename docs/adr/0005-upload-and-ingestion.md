# ADR-0005: Upload and ingestion pipeline

- **Status:** Proposed
- **Date:** 2026-08-12
- **Depends on:** [ADR-0002](0002-library-storage-and-ownership.md),
  [ADR-0004](0004-metadata-and-categorization.md)

## Context

Uploads and filesystem scans process attacker-controlled ZIP and XML input
and must survive crashes, retries, duplicates, and restarts. Performing the
work inside an HTTP handler would make partial writes and unexplained timeouts
inevitable.

## Decision

All content enters through a persistent ingestion job, regardless of source.

### State machine

```text
received -> staged -> validated -> extracted -> promoted
                    \-> quarantined
                    \-> failed
```

Jobs record owner, library, source, client idempotency key, an immutable
request fingerprint, state, revision, byte counts, timestamps, safe error
code, and retry count. Creation replays the original job only when the
idempotency key and request fingerprint agree. State changes compare both the
expected state and revision, so retries and concurrent workers cannot
overwrite a newer result. The generic transition operation cannot enter
`promoted`. A failure before a complete staged artifact retries through
`received`; failed or quarantined jobs that retain a complete staged artifact
retry from `staged`.

Managed uploads are streamed into a private CAS-side
`<content>/.incoming/<job-hash>.partial`, fsynced, and atomically renamed to
the completed `.stage` name. Validation and extraction read that immutable
stage. Promotion atomically renames it to the final content-addressed path.
Before promotion, newly created hash-shard directories are made durable by
fsyncing each directory and the parent in which its entry was created. After
the rename, the worker fsyncs the destination directory and `.incoming`, and
only then commits the blob/reference rows, quota reservation, and `promoted`
job state in one database transaction. A crash before the database commit
leaves a reconcilable orphan, never a database reference to a non-durable
path.

The Linux CAS implementation uses descriptor-relative operations,
`O_NOFOLLOW`, private ownership and permissions, and
`renameat2(RENAME_NOREPLACE)`. Incomplete `.partial` files are never treated
as durable stages. A completed deterministic `.stage` can be rehashed and
replayed after a lost result, and promotion can likewise be replayed after
the final rename. Concurrent identical promotions verify and fsync the
existing final blob rather than replacing it.

Watched ingestion opens the source through `os.Root` and copies from that
single descriptor into the same CAS-side staging path. Hashing, validation,
metadata extraction, covers, and downloads all use the immutable copied
snapshot, never a path that can change after validation. The source path and
fingerprint remain reconciliation inputs; the server never mutates them.
Successful validation does not prove catalog identity: a hash-changing
watched snapshot enters ADR-0002's identity reconciliation before it may
replace a book's current file reference.

On startup, stale jobs are resumed when safe or moved to a clear failed
state. Reconciliation detects missing rows, missing blobs, and unreferenced
blobs. It never deletes an unreferenced blob immediately; ADR-0002's grace
period applies.

The recovery coordinator verifies each persisted stage against its expected
hash and size. If the deterministic stage is absent but the final CAS blob is
valid, it reports the job as recoverable after a lost filesystem-promotion
response. Missing artifacts become failed; corrupt, unsafe, or mismatched
artifacts become quarantined or stop recovery without touching another job's
path. Valid jobs remain in their persisted state for the worker to resume.

The blob hash has a unique constraint. Concurrent identical uploads converge
on one blob while preserving separate authorized references.

Staging commits the job's content identity and a transient logical quota hold
in one transaction. Quota counts each principal/hash once across transient
holds and durable reservations, so concurrent identical jobs for one
principal do not double-charge while another principal is charged
independently. Promotion atomically creates or verifies the blob, installs
the durable reservation and catalog book/file rows, consumes the hold, and
advances the job. A canonical fingerprint of the complete normalized
promotion request makes a lost successful response replayable without
accepting a different book, file, metadata, source, or timestamp payload.

### EPUB bounds

Hashing is streaming SHA-256. The existing KOReader partial-MD5 helper is
used only when registering work aliases and is not the ingestion hash path.

Validation enforces configurable limits for:

- request and compressed file size;
- ZIP entry count;
- total uncompressed bytes;
- per-entry size and decompression ratio;
- metadata XML bytes and nesting depth.

ZIP paths are normalized and rejected if absolute, escaping, duplicated in a
conflicting way, or symlink/hardlink entries. XML parsing does not resolve
external entities. Container, OPF, navigation, and encryption documents are
bounded before parsing.

`META-INF/encryption.xml` is interpreted by algorithm. IDPF and Adobe font
obfuscation are allowed. Any unsupported encryption of publication content is recorded as a stable
DRM validation error.

### Extracted media

Covers are decoded and transcoded to a bounded raster format. SVG and
mislabeled/non-raster cover sources are not served directly. Extracted asset
responses use fixed server-selected MIME types and
`X-Content-Type-Options: nosniff`.

Thumbnails are a regenerable cache and are not part of the critical backup
unit.

### Upload interfaces

`POST /v1/library/upload` accepts multipart uploads for a managed library and
returns a job resource. API and web clients send a bounded client-generated
`Idempotency-Key`, unique per user and target library. Replaying a key returns
the original job; reusing it for a different content hash returns 409. A new
key may intentionally create another catalog reference to an already
deduplicated blob.

Request-envelope failures such as authentication, ACL, multipart,
declared-size, and quota errors return precise 4xx responses.
EPUB validation runs asynchronously: malformed content, unsupported DRM, and
extraction failures become stable `failed` or `quarantined` job states with
machine-readable error codes. Watched-file failures use the same job states.

The htmx UI supports file selection and drag/drop, shows transfer and ingest
state, and links to actionable errors. Uploads do not block until metadata
extraction finishes.

All UI mutations require CSRF. API upload requires `library-manage` plus
manage ACL access to the target library.

### Retention and quotas

Staged, quarantined, failed, and promoted bytes count toward the target
library's `quota_user_id`, matching ADR-0002. The initiating user is retained
for audit and authorization but is not a second quota principal; automated
watched scans use the same library principal. Instance-wide staging and
per-principal caps prevent unbounded failure storage. Quarantine and failed
artifacts expire after a configurable period and are cleaned by the GC
worker. Expiry releases the transient quota hold, marks cleanup pending, and
retains the staging path in a terminal job tombstone. Filesystem removal is
idempotent and explicitly acknowledged before the database clears the path,
so a crash or I/O error retries cleanup on the next pass. The tombstone cannot
be retried or recreated, preventing delayed cleanup from racing with a new
deterministic stage path. An authorized manager can delete failed jobs sooner
only through the same tombstone-safe cleanup lifecycle.

## Consequences

The durable queue is more work than synchronous ingestion but gives one
auditable path for uploads, scans, retries, and recovery. Error responses can
name a stable job and reason instead of losing state when a request ends.

## Implementation phases

1. Job schema, worker loop, staging, CAS promotion, recovery, and
   reconciliation. The revisioned, idempotent durable job store and
   blob/reference schema plus bounded, restart-safe filesystem staging and
   promotion are implemented. Atomic database promotion, logical quota
   holds/reservations, full-payload replay detection, and terminal artifact
   expiry are also implemented. Stale-artifact verification, lost-promotion
   detection, and retryable two-phase filesystem cleanup are implemented;
   `serve` recovers every pre-existing nonterminal job before accepting
   traffic. Strict final-blob filesystem inventory is implemented; workers
   remain. Database blob reconciliation can mark missing content and
   filesystem-only or unreferenced orphans, and startup performs the complete
   comparison followed by a configurable grace-period sweep. Active ingest
   holds prevent collection. The pure EPUB validator now performs a bounded
   central-directory preflight, streams every entry under expansion and ratio
   limits, rejects unsafe entry types and paths, bounds control XML, and
   accepts only manifest-declared font obfuscation. The CAS can validate either
   the immutable stage or an already-promoted lost-response artifact, and the
   revision-checked worker step advances valid jobs or retains content failures
   as quarantined jobs with stable codes. All ZIP, expansion, metadata, and
   XML bounds have explicit `[content]` configuration with conservative
   defaults. The server now runs one database-snapshotted, size-capped
   validation batch and one independent metadata-extraction batch on each
   configurable polling interval after startup recovery and GC. Extraction
   revalidates the immutable stage or lost-promotion final artifact, persists
   the bounded embedded metadata JSON in the revision-checked
   `validated -> extracted` transition, timestamps after processing, and
   quarantines content failures with stable validator codes. Both passes skip
   revision races for a later pass and surface operational failures by
   coordinating worker cancellation and HTTP shutdown. Catalog availability
   reconciliation remains.
2. EPUB security validator fixture expansion and worker scheduling.
   Implemented.
3. Metadata and cover extraction. The pure bounded OPF extractor and durable
   canonical embedded-metadata snapshot in the atomic
   `validated -> extracted` transition are implemented. Catalog-field/entity
   materialization, precedence and filename parsing, cover extraction and
   transcoding, and the automatic promotion worker remain.
4. API upload and htmx upload UI.

## Acceptance criteria

- Retrying an upload with its idempotency key returns the same job, and
  concurrent duplicate content shares one blob.
- Killing the server in every state leaves a recoverable or explicitly
  failed job.
- Request-envelope violations produce precise 4xx responses. Zip bombs,
  zip-slip paths, symlinks, malformed XML, unsupported encryption, and other
  asynchronous content failures produce stable failed/quarantined job codes
  and no writes outside staging.
- Font-obfuscated EPUBs remain valid.
- Mutating a watched source after ingest cannot change bytes served from the
  validated snapshot; only a successfully re-ingested snapshot can replace
  it after identity reconciliation.
- Failed artifacts respect quota and expiry.
