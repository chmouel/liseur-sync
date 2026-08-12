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

Jobs record owner, library, source, client idempotency key, state, byte
counts, timestamps, safe error code, and retry count. Managed uploads are
streamed into a private staging file. After validation and extraction, the
worker streams or copies the bytes into `<content>/.incoming/<job>.tmp`,
fsyncs the file, then atomically renames it to the final content-addressed
path. Before promotion, newly created hash-shard directories are made durable
by fsyncing each directory and the parent in which its entry was created.
After the rename, the worker fsyncs the destination directory and
`.incoming`, and only then commits the blob/reference rows and `promoted` job
state. A crash before the database commit leaves a reconcilable orphan, never
a database reference to a non-durable path. Atomic promotion therefore works
even when initial staging is on another filesystem. Watched files use the
same validation and extraction stages but remain at their source path.

On startup, stale jobs are resumed when safe or moved to a clear failed
state. Reconciliation detects missing rows, missing blobs, and unreferenced
blobs. It never deletes an unreferenced blob immediately; ADR-0002's grace
period applies.

The blob hash has a unique constraint. Concurrent identical uploads converge
on one blob while preserving separate authorized references.

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

Staged, quarantined, and failed bytes count toward the uploader's logical
quota. Instance and per-user caps prevent unbounded failure storage.
Quarantine and failed artifacts expire after a configurable period and are
cleaned by the GC worker. Users can delete their own failed jobs sooner.

## Consequences

The durable queue is more work than synchronous ingestion but gives one
auditable path for uploads, scans, retries, and recovery. Error responses can
name a stable job and reason instead of losing state when a request ends.

## Implementation phases

1. Job schema, worker loop, staging, CAS promotion, recovery, and
   reconciliation.
2. EPUB security validator and fixture corpus.
3. Metadata and cover extraction.
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
- Failed artifacts respect quota and expiry.
