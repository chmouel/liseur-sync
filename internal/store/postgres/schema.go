package postgres

// schema is migration 1 for PostgreSQL. Composite, database-enforced
// FKs; the edition<->record FK is DEFERRABLE INITIALLY DEFERRED so
// split/merge stay single transactions.
const schema = `
CREATE TABLE users (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    argon2_hash       TEXT NOT NULL,
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    kosync_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    koplugin_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL
);

CREATE TABLE tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    scope       TEXT NOT NULL,
    sha256      TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ,
    last_used   TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX tokens_user ON tokens(user_id);

CREATE TABLE auth_sessions (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sha256             TEXT NOT NULL UNIQUE,
    kind               TEXT NOT NULL,
    csrf_token_sha256  TEXT,
    created_at         TIMESTAMPTZ NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ
);
CREATE INDEX auth_sessions_user ON auth_sessions(user_id);

CREATE TABLE invites (
    id           TEXT PRIMARY KEY,
    code_sha256  TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_by      TEXT,
    used_at      TIMESTAMPTZ
);

CREATE TABLE pairing_codes (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_sha256 TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ
);

CREATE TABLE seq_counters (
    user_id  TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    next_seq BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE works (
    id         TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title      TEXT NOT NULL DEFAULT '',
    author     TEXT NOT NULL DEFAULT '',
    pending    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, id)
);

CREATE TABLE editions (
    user_id    TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    page_count BIGINT,
    char_count BIGINT,
    meta_json  BYTEA,
    PRIMARY KEY (user_id, sha256),
    UNIQUE (user_id, sha256, work_id),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE
);

CREATE TABLE aliases (
    user_id TEXT NOT NULL,
    kind    TEXT NOT NULL,
    value   TEXT NOT NULL,
    work_id TEXT NOT NULL,
    PRIMARY KEY (user_id, kind, value),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE
);

CREATE TABLE ops (
    user_id      TEXT NOT NULL,
    seq          BIGINT NOT NULL,
    op_id        TEXT NOT NULL,
    work_id      TEXT NOT NULL,
    edition_sha  TEXT,
    device_id    TEXT NOT NULL,
    client_ts    TIMESTAMPTZ NOT NULL,
    progression  DOUBLE PRECISION NOT NULL,
    locator_json BYTEA,
    foreign_pos  TEXT,
    origin       TEXT NOT NULL,
    origin_alias TEXT,
    received_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, seq),
    UNIQUE (user_id, op_id),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, edition_sha, work_id)
        REFERENCES editions(user_id, sha256, work_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX ops_work_seq ON ops(user_id, work_id, seq);
CREATE INDEX ops_device_received ON ops(user_id, device_id, received_at);

CREATE TABLE sessions (
    user_id      TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    work_id      TEXT NOT NULL,
    edition_sha  TEXT,
    device_id    TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ NOT NULL,
    start_prog   DOUBLE PRECISION NOT NULL,
    end_prog     DOUBLE PRECISION NOT NULL,
    idle_ms      BIGINT NOT NULL DEFAULT 0,
    origin       TEXT NOT NULL,
    origin_alias TEXT,
    source_key   TEXT,
    received_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, session_id),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, edition_sha, work_id)
        REFERENCES editions(user_id, sha256, work_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX sessions_work_started ON sessions(user_id, work_id, started_at);

CREATE TABLE session_supersessions (
    user_id     TEXT NOT NULL,
    source_key  TEXT NOT NULL,
    revision    BIGINT NOT NULL,
    session_id  TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, source_key, revision),
    FOREIGN KEY (user_id, session_id) REFERENCES sessions(user_id, session_id) ON DELETE CASCADE
);
CREATE INDEX supersessions_latest ON session_supersessions(user_id, source_key, revision DESC);

CREATE TABLE kosync_devices (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_slot TEXT NOT NULL,
    key_sha256  TEXT NOT NULL UNIQUE,
    label       TEXT NOT NULL DEFAULT '',
    revoked_at  TIMESTAMPTZ,
    PRIMARY KEY (user_id, device_slot)
);

CREATE TABLE koplugin_devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_sha256 TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL DEFAULT '',
    device_id    TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ
);

CREATE TABLE compaction_state (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    horizon BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
);
`

// migration2 adds per-(work, tz-local day) session aggregates and
// compact tombstones that preserve idempotency after raw rows age out.
const migration2 = `
CREATE TABLE session_rollups (
    user_id        TEXT NOT NULL,
    work_id        TEXT NOT NULL,
    day            TEXT NOT NULL,
    active_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    pages          DOUBLE PRECISION NOT NULL DEFAULT 0,
    prog_delta     DOUBLE PRECISION NOT NULL DEFAULT 0,
    session_count  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, work_id, day),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE
);

CREATE TABLE session_tombstones (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    PRIMARY KEY (user_id, session_id)
);
`

// migration3 records which kosync ops have already contributed to an
// inferred session. Existing raw sessions and rolled-up history mark
// their covered ops; unmatched activity remains pending.
const migration3 = `
ALTER TABLE ops ADD COLUMN inferred_session_id TEXT;

UPDATE sessions s
SET origin_alias = (
        SELECT MIN(o.origin_alias)
        FROM ops o
        WHERE o.user_id = s.user_id
          AND o.device_id = s.device_id
          AND o.received_at >= s.started_at
          AND o.received_at <= s.ended_at
          AND o.origin = 'kosync'
    ),
    work_id = (
        SELECT MIN(o.work_id)
        FROM ops o
        WHERE o.user_id = s.user_id
          AND o.device_id = s.device_id
          AND o.received_at >= s.started_at
          AND o.received_at <= s.ended_at
          AND o.origin = 'kosync'
    )
WHERE s.origin = 'inferred'
  AND s.edition_sha IS NULL
  AND s.origin_alias IS NULL
  AND 1 = (
      SELECT COUNT(DISTINCT (o.work_id, COALESCE(o.origin_alias, '')))
      FROM ops o
      WHERE o.user_id = s.user_id
        AND o.device_id = s.device_id
        AND o.received_at >= s.started_at
        AND o.received_at <= s.ended_at
        AND o.origin = 'kosync'
  );

UPDATE ops
SET inferred_session_id = (
    SELECT s.session_id
    FROM sessions s
    WHERE s.user_id = ops.user_id
      AND (s.work_id = ops.work_id
           OR (s.origin_alias IS NULL AND s.edition_sha IS NULL))
      AND s.device_id = ops.device_id
      AND s.origin = 'inferred'
      AND ops.received_at >= s.started_at
      AND ops.received_at <= s.ended_at
      AND (s.origin_alias = ops.origin_alias OR s.origin_alias IS NULL)
    ORDER BY s.started_at DESC
    LIMIT 1
)
WHERE origin = 'kosync'
  AND EXISTS (
      SELECT 1
      FROM sessions s
      WHERE s.user_id = ops.user_id
        AND (s.work_id = ops.work_id
             OR (s.origin_alias IS NULL AND s.edition_sha IS NULL))
        AND s.device_id = ops.device_id
        AND s.origin = 'inferred'
        AND ops.received_at >= s.started_at
        AND ops.received_at <= s.ended_at
        AND (s.origin_alias = ops.origin_alias OR s.origin_alias IS NULL)
  );

UPDATE ops
SET inferred_session_id = 'legacy-rollup'
WHERE origin = 'kosync'
  AND inferred_session_id IS NULL
  AND EXISTS (
      SELECT 1
      FROM session_rollups r
      WHERE r.user_id = ops.user_id
        AND (ops.received_at AT TIME ZONE 'UTC')::date <= r.day::date + 1
  );

CREATE INDEX idx_ops_inference_pending
    ON ops(user_id, received_at)
    WHERE origin = 'kosync' AND inferred_session_id IS NULL;
`

// migration4 reverses migration3's date-only rollup inference. A
// rollup for another work is not evidence that an unmatched op was
// materialized, so ambiguous legacy snapshots must remain pending.
const migration4 = `
UPDATE ops
SET inferred_session_id = NULL
WHERE inferred_session_id = 'legacy-rollup';
`

// migration5 replaces scalar token capabilities with a normalized scope
// relation. The legacy column remains populated for singleton compatibility;
// multi-scope tokens store an empty legacy value so old binaries fail closed.
const migration5 = `
CREATE UNIQUE INDEX tokens_id_user ON tokens(id, user_id);

CREATE TABLE token_scopes (
    token_id TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    scope    TEXT NOT NULL,
    PRIMARY KEY (token_id, scope),
    FOREIGN KEY (token_id, user_id)
        REFERENCES tokens(id, user_id) ON DELETE CASCADE
);
CREATE INDEX token_scopes_user ON token_scopes(user_id, token_id);

INSERT INTO token_scopes (token_id, user_id, scope)
SELECT id, user_id, scope FROM tokens;

CREATE OR REPLACE FUNCTION sync_legacy_token_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.scope <> '' THEN
        INSERT INTO token_scopes (token_id, user_id, scope)
        VALUES (NEW.id, NEW.user_id, NEW.scope)
        ON CONFLICT (token_id, scope) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tokens_scope_legacy_insert
AFTER INSERT ON tokens
FOR EACH ROW EXECUTE FUNCTION sync_legacy_token_scope();
`

// migration6 adds the shared catalog, ACL, metadata, durable ingest, and
// per-user catalog-to-work identity tables. Cross-library and cross-user
// joins are protected by composite foreign keys.
const migration6 = `
CREATE TABLE libraries (
    id            TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind          TEXT NOT NULL CHECK (kind IN ('managed', 'watched')),
    name          TEXT NOT NULL,
    root_path     TEXT,
    config_json   BYTEA,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (id, quota_user_id),
    CHECK ((kind = 'managed' AND root_path IS NULL) OR
           (kind = 'watched' AND root_path IS NOT NULL))
);
CREATE UNIQUE INDEX libraries_watched_root
    ON libraries(root_path) WHERE root_path IS NOT NULL;
CREATE INDEX libraries_owner ON libraries(owner_user_id);

CREATE TABLE library_access (
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('read', 'manage')),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (library_id, user_id)
);
CREATE INDEX library_access_user ON library_access(user_id, library_id);

CREATE TABLE books (
    id                    TEXT PRIMARY KEY,
    library_id            TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    status                TEXT NOT NULL CHECK (status IN ('active', 'missing', 'trashed', 'review')),
    title                 TEXT NOT NULL DEFAULT '',
    title_source          TEXT NOT NULL DEFAULT '',
    title_locked          BOOLEAN NOT NULL DEFAULT FALSE,
    subtitle              TEXT NOT NULL DEFAULT '',
    subtitle_source       TEXT NOT NULL DEFAULT '',
    subtitle_locked       BOOLEAN NOT NULL DEFAULT FALSE,
    description           TEXT NOT NULL DEFAULT '',
    description_source    TEXT NOT NULL DEFAULT '',
    description_locked    BOOLEAN NOT NULL DEFAULT FALSE,
    publisher             TEXT NOT NULL DEFAULT '',
    publisher_source      TEXT NOT NULL DEFAULT '',
    publisher_locked      BOOLEAN NOT NULL DEFAULT FALSE,
    published_date        TEXT NOT NULL DEFAULT '',
    published_date_source TEXT NOT NULL DEFAULT '',
    published_date_locked BOOLEAN NOT NULL DEFAULT FALSE,
    raw_metadata_json     BYTEA,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    trashed_at            TIMESTAMPTZ,
    trash_expires_at      TIMESTAMPTZ,
    UNIQUE (library_id, id)
);
CREATE INDEX books_library_status ON books(library_id, status, created_at);

CREATE TABLE blobs (
    sha256      TEXT PRIMARY KEY,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at  TIMESTAMPTZ NOT NULL,
    orphaned_at TIMESTAMPTZ
);

CREATE TABLE book_files (
    id                   TEXT PRIMARY KEY,
    library_id           TEXT NOT NULL,
    book_id              TEXT NOT NULL,
    blob_sha256          TEXT NOT NULL REFERENCES blobs(sha256) ON DELETE RESTRICT,
    source               TEXT NOT NULL CHECK (source IN ('upload', 'watched')),
    source_relative_path TEXT,
    original_filename    TEXT NOT NULL DEFAULT '',
    media_type           TEXT NOT NULL DEFAULT 'application/epub+zip',
    partial_md5          TEXT,
    dc_identifier        TEXT,
    availability         TEXT NOT NULL CHECK (availability IN ('available', 'missing', 'superseded')),
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);
CREATE INDEX book_files_book ON book_files(library_id, book_id, availability);
CREATE INDEX book_files_blob ON book_files(blob_sha256);
CREATE INDEX book_files_source_path ON book_files(library_id, source_relative_path);

CREATE TABLE blob_reservations (
    quota_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blob_sha256   TEXT NOT NULL REFERENCES blobs(sha256) ON DELETE CASCADE,
    bytes         BIGINT NOT NULL CHECK (bytes >= 0),
    created_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (quota_user_id, blob_sha256)
);

CREATE TABLE book_identifiers (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    scheme     TEXT NOT NULL,
    value      TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, scheme, value),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);
CREATE INDEX book_identifiers_lookup ON book_identifiers(library_id, scheme, value);

CREATE TABLE series (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_series (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    series_id  TEXT NOT NULL,
    position   DOUBLE PRECISION,
    source     TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, series_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, series_id)
        REFERENCES series(library_id, id) ON DELETE CASCADE
);

CREATE TABLE contributors (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_contributors (
    library_id      TEXT NOT NULL,
    book_id         TEXT NOT NULL,
    contributor_id  TEXT NOT NULL,
    role            TEXT NOT NULL,
    position        BIGINT NOT NULL DEFAULT 0,
    source          TEXT NOT NULL,
    locked          BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, contributor_id, role),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, contributor_id)
        REFERENCES contributors(library_id, id) ON DELETE CASCADE
);

CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_tags (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    tag_id     TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, tag_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, tag_id)
        REFERENCES tags(library_id, id) ON DELETE CASCADE
);

CREATE TABLE genres (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_genres (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    genre_id   TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, genre_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, genre_id)
        REFERENCES genres(library_id, id) ON DELETE CASCADE
);

CREATE TABLE book_languages (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    language   TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (book_id, language),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

CREATE TABLE collections (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    created_by      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE collection_books (
    library_id    TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    book_id       TEXT NOT NULL,
    position      BIGINT NOT NULL DEFAULT 0,
    added_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (collection_id, book_id),
    FOREIGN KEY (library_id, collection_id)
        REFERENCES collections(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

CREATE TABLE reading_lists (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    created_by      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE reading_list_books (
    library_id      TEXT NOT NULL,
    reading_list_id TEXT NOT NULL,
    book_id         TEXT NOT NULL,
    position        BIGINT NOT NULL CHECK (position >= 0),
    added_at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (reading_list_id, book_id),
    UNIQUE (reading_list_id, position),
    FOREIGN KEY (library_id, reading_list_id)
        REFERENCES reading_lists(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

CREATE TABLE user_book_works (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, book_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, work_id)
        REFERENCES works(user_id, id) ON DELETE CASCADE
);
CREATE INDEX user_book_works_work ON user_book_works(user_id, work_id);

CREATE TABLE ingest_jobs (
    id                   TEXT PRIMARY KEY,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id           TEXT NOT NULL,
    quota_user_id        TEXT NOT NULL,
    source               TEXT NOT NULL CHECK (source IN ('upload', 'watched')),
    client_key           TEXT,
    state                TEXT NOT NULL CHECK (state IN ('received', 'staged', 'validated', 'extracted', 'promoted', 'quarantined', 'failed')),
    bytes_received       BIGINT NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
    content_sha256       TEXT,
    staging_path         TEXT,
    source_relative_path TEXT,
    book_library_id      TEXT,
    book_id              TEXT,
    error_code           TEXT,
    error_detail         TEXT,
    retry_count          BIGINT NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    expires_at           TIMESTAMPTZ,
    CHECK ((book_library_id IS NULL AND book_id IS NULL) OR
           (book_library_id IS NOT NULL AND book_id IS NOT NULL AND
            book_library_id = library_id)),
    FOREIGN KEY (book_library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE SET NULL,
    FOREIGN KEY (library_id, quota_user_id)
        REFERENCES libraries(id, quota_user_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ingest_jobs_client_key
    ON ingest_jobs(user_id, library_id, client_key)
    WHERE client_key IS NOT NULL;
CREATE INDEX ingest_jobs_state ON ingest_jobs(state, updated_at);
CREATE INDEX ingest_jobs_library ON ingest_jobs(library_id, created_at);
`

const migration7 = `
ALTER TABLE ingest_jobs
    ADD COLUMN request_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE ingest_jobs
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1);
`

// Migration 8 is SQLite-only timestamp normalization. Keep an empty
// PostgreSQL migration so schema version numbers stay aligned.
const migration8 = `SELECT 1;`

const migration9 = `
ALTER TABLE ingest_jobs
    ADD COLUMN promotion_fingerprint TEXT;
ALTER TABLE ingest_jobs
    ADD COLUMN artifacts_expired BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX ingest_jobs_id_quota
    ON ingest_jobs(id, quota_user_id);

CREATE TABLE ingest_blob_holds (
    job_id        TEXT PRIMARY KEY,
    quota_user_id TEXT NOT NULL,
    blob_sha256   TEXT NOT NULL,
    bytes         BIGINT NOT NULL CHECK (bytes >= 0),
    created_at    TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (job_id, quota_user_id)
        REFERENCES ingest_jobs(id, quota_user_id) ON DELETE CASCADE
);
CREATE INDEX ingest_blob_holds_principal_blob
    ON ingest_blob_holds(quota_user_id, blob_sha256);

INSERT INTO ingest_blob_holds
    (job_id, quota_user_id, blob_sha256, bytes, created_at)
SELECT id, quota_user_id, content_sha256, bytes_received, updated_at
FROM ingest_jobs
WHERE state <> 'promoted'
  AND content_sha256 IS NOT NULL
  AND staging_path IS NOT NULL;
`

const migration10 = `
ALTER TABLE ingest_jobs
    ADD COLUMN artifact_cleanup_pending BOOLEAN NOT NULL DEFAULT FALSE;
`

const migration11 = `
ALTER TABLE blobs
    ADD COLUMN missing_at TIMESTAMPTZ;
`

const migration12 = `
ALTER TABLE ingest_jobs
    ADD COLUMN extracted_embedded_metadata_json BYTEA;
`

const migration13 = `
ALTER TABLE books
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1);
ALTER TABLE books
    ADD COLUMN identifiers_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE books
    ADD COLUMN languages_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE books
    ADD COLUMN tags_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE books
    ADD COLUMN genres_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE books
    ADD COLUMN series_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE books
    ADD COLUMN contributors_locked BOOLEAN NOT NULL DEFAULT FALSE;
`

// migration14 records what a watched sweep observed about each file's
// source path. Source presence is a separate axis from blob presence: a
// watched book's bytes stay in the CAS after the file they were copied
// from is deleted, so `blobs.missing_at` can never express that the
// library no longer contains the book. NULL means present, matching
// `blobs.missing_at`, so every existing row and every upload — which has
// no source path to lose — starts out present without a backfill.
const migration14 = `
ALTER TABLE book_files
    ADD COLUMN source_seen_at TIMESTAMPTZ;
ALTER TABLE book_files
    ADD COLUMN source_absent_at TIMESTAMPTZ;
ALTER TABLE book_files
    ADD COLUMN source_modified_at TIMESTAMPTZ;
ALTER TABLE books
    ADD COLUMN review_reason TEXT;
`

// migration15 adds the search index: one weighted tsvector per book,
// maintained by the same writes that maintain the SQLite FTS5 table.
//
// The `simple` configuration is chosen rather than `english` so that both
// backends answer the same question. `english` stems, so "reading" would
// find a book called "Read" on PostgreSQL and not on SQLite, and a
// self-hosted server whose behaviour depends on which database somebody
// picked is a server nobody can support.
//
// `unaccent` is deliberately not used: it is an extension, and requiring
// one would make the schema fail to migrate on a managed database that
// does not offer it. Diacritics are folded when the vector is built
// instead, where both backends can do it the same way.
const migration15 = `
ALTER TABLE books ADD COLUMN search_vector tsvector;
CREATE INDEX books_search_idx ON books USING GIN (search_vector);
`

// migration16 makes administration an account property rather than a
// credential (ADR-0013). See the SQLite copy for why; the backfill is
// the same set, expressed against the same tables.
const migration16 = `
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN disabled_at TIMESTAMPTZ;
UPDATE users SET is_admin = TRUE WHERE id IN (
    SELECT ts.user_id FROM token_scopes ts
    JOIN tokens t ON t.id = ts.token_id
    WHERE ts.scope = 'admin' AND t.revoked_at IS NULL
);
`

// migration17 splits libraries.kind into the three axes of ADR-0014. See
// the SQLite copy for why; PostgreSQL can alter constraints in place, so
// there is no table rebuild here — the same end state, reached the way
// this backend reaches it.
const migration17 = `
ALTER TABLE libraries ADD COLUMN source TEXT;
ALTER TABLE libraries ADD COLUMN storage TEXT;
ALTER TABLE libraries ADD COLUMN refresh TEXT;
ALTER TABLE libraries
    ADD COLUMN refresh_interval_seconds INTEGER NOT NULL DEFAULT 900;

UPDATE libraries SET
    source  = CASE kind WHEN 'managed' THEN 'managed' ELSE 'directory' END,
    storage = 'cas',
    refresh = CASE kind WHEN 'managed' THEN 'manual' ELSE 'interval' END;

ALTER TABLE libraries ALTER COLUMN source SET NOT NULL;
ALTER TABLE libraries ALTER COLUMN storage SET NOT NULL;
ALTER TABLE libraries ALTER COLUMN refresh SET NOT NULL;

ALTER TABLE libraries DROP CONSTRAINT libraries_kind_check;
ALTER TABLE libraries DROP CONSTRAINT libraries_check;
ALTER TABLE libraries DROP COLUMN kind;

ALTER TABLE libraries
    ADD CONSTRAINT libraries_source_check
        CHECK (source IN ('managed', 'directory')),
    ADD CONSTRAINT libraries_storage_check
        CHECK (storage IN ('cas')),
    ADD CONSTRAINT libraries_refresh_check
        CHECK (refresh IN ('manual', 'interval')),
    ADD CONSTRAINT libraries_refresh_interval_check
        CHECK (refresh_interval_seconds > 0),
    ADD CONSTRAINT libraries_root_check
        CHECK ((source = 'managed' AND root_path IS NULL) OR
               (source <> 'managed' AND root_path IS NOT NULL)),
    ADD CONSTRAINT libraries_managed_refresh_check
        CHECK (source <> 'managed' OR refresh = 'manual');

ALTER INDEX libraries_watched_root RENAME TO libraries_root;
CREATE INDEX libraries_refresh_due
    ON libraries(refresh, source) WHERE refresh = 'interval';
`

// migration18 is the PostgreSQL half of ADR-0014's storage split. See
// the SQLite migration for why identity and the CAS copy stop being the
// same column; here they are plain ALTERs, because a CHECK can be
// dropped and added in place.
const migration18 = `
ALTER TABLE libraries DROP CONSTRAINT libraries_storage_check;
ALTER TABLE libraries
    ADD CONSTRAINT libraries_storage_check
        CHECK (storage IN ('cas', 'in_place')),
    ADD CONSTRAINT libraries_in_place_root_check
        CHECK (storage = 'cas' OR root_path IS NOT NULL);

ALTER TABLE book_files ADD COLUMN storage TEXT;
ALTER TABLE book_files ADD COLUMN content_sha256 TEXT;
ALTER TABLE book_files ADD COLUMN content_size_bytes BIGINT;

UPDATE book_files f SET
    storage            = 'cas',
    content_sha256     = f.blob_sha256,
    content_size_bytes = COALESCE(
        (SELECT b.size_bytes FROM blobs b WHERE b.sha256 = f.blob_sha256), 0);

ALTER TABLE book_files
    ALTER COLUMN storage SET NOT NULL,
    ALTER COLUMN content_sha256 SET NOT NULL,
    ALTER COLUMN content_size_bytes SET NOT NULL,
    ALTER COLUMN blob_sha256 DROP NOT NULL;

UPDATE book_files SET source = 'scanned' WHERE source = 'watched';

ALTER TABLE book_files DROP CONSTRAINT book_files_source_check;
ALTER TABLE book_files
    ADD CONSTRAINT book_files_source_check
        CHECK (source IN ('upload', 'scanned')),
    ADD CONSTRAINT book_files_storage_check
        CHECK (storage IN ('cas', 'in_place')),
    ADD CONSTRAINT book_files_content_size_check
        CHECK (content_size_bytes >= 0),
    ADD CONSTRAINT book_files_storage_blob_check
        CHECK ((storage = 'cas' AND blob_sha256 IS NOT NULL) OR
               (storage = 'in_place' AND blob_sha256 IS NULL AND
                source_relative_path IS NOT NULL));

DROP INDEX book_files_blob;
CREATE INDEX book_files_blob ON book_files(blob_sha256)
    WHERE blob_sha256 IS NOT NULL;
CREATE INDEX book_files_content ON book_files(content_sha256);

ALTER TABLE ingest_jobs
    ADD COLUMN storage TEXT NOT NULL DEFAULT 'cas';

UPDATE ingest_jobs SET source = 'scanned' WHERE source = 'watched';

ALTER TABLE ingest_jobs DROP CONSTRAINT ingest_jobs_source_check;
ALTER TABLE ingest_jobs
    ADD CONSTRAINT ingest_jobs_source_check
        CHECK (source IN ('upload', 'scanned')),
    ADD CONSTRAINT ingest_jobs_storage_check
        CHECK (storage IN ('cas', 'in_place')),
    ADD CONSTRAINT ingest_jobs_in_place_path_check
        CHECK (storage = 'cas' OR source_relative_path IS NOT NULL);
`

var migrations = []string{
	schema, migration2, migration3, migration4, migration5, migration6,
	migration7, migration8, migration9, migration10, migration11, migration12,
	migration13, migration14, migration15, migration16, migration17,
	migration18, migration19, migration20, migration21,
}

// migration19 is the PostgreSQL half of the refresh history. See the
// SQLite migration for what each column is for.
const migration19 = `
ALTER TABLE libraries
    ADD COLUMN last_refresh_at TIMESTAMPTZ,
    ADD COLUMN last_refresh_attempt_at TIMESTAMPTZ,
    ADD COLUMN last_refresh_error TEXT,
    ADD COLUMN refresh_requested_at TIMESTAMPTZ;

CREATE INDEX libraries_refresh_requested
    ON libraries(refresh_requested_at)
    WHERE refresh_requested_at IS NOT NULL;
`

// migration20 is the PostgreSQL half of what a Calibre library needs.
// See the SQLite migration for what each piece is for; here the widened
// CHECK is an ALTER rather than a table rebuild.
const migration20 = `
ALTER TABLE libraries DROP CONSTRAINT libraries_source_check;
ALTER TABLE libraries
    ADD CONSTRAINT libraries_source_check
        CHECK (source IN ('managed', 'directory', 'calibre'));

ALTER TABLE libraries ADD COLUMN last_inventory_digest TEXT;

CREATE TABLE library_calibre_books (
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    calibre_id BIGINT NOT NULL,
    book_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (library_id, calibre_id),
    UNIQUE (library_id, book_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

ALTER TABLE book_files
    ADD COLUMN cover_relative_path TEXT,
    ADD COLUMN cover_sha256 TEXT;
`

// migration21 is the PostgreSQL half of the refresh lease and the
// bounded failure code. See the SQLite migration for what each column
// is for.
const migration21 = `
ALTER TABLE libraries
    ADD COLUMN refresh_lease_owner TEXT,
    ADD COLUMN refresh_lease_until TIMESTAMPTZ,
    ADD COLUMN last_refresh_code TEXT,
    DROP COLUMN last_refresh_error;
`
