package sqlite

// schema is migration 1: the full v1 schema. Composite, database-
// enforced foreign keys throughout; the edition<->record FK is
// DEFERRABLE INITIALLY DEFERRED so split/merge stay single
// transactions.
const schema = `
CREATE TABLE users (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    argon2_hash       TEXT NOT NULL,
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    kosync_enabled    INTEGER NOT NULL DEFAULT 1,
    koplugin_enabled  INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL
);

CREATE TABLE tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    scope       TEXT NOT NULL,
    sha256      TEXT NOT NULL UNIQUE,
    created_at  TEXT NOT NULL,
    expires_at  TEXT,
    last_used   TEXT,
    revoked_at  TEXT
);
CREATE INDEX tokens_user ON tokens(user_id);

CREATE TABLE auth_sessions (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sha256             TEXT NOT NULL UNIQUE,
    kind               TEXT NOT NULL,          -- login | web
    csrf_token_sha256  TEXT,
    created_at         TEXT NOT NULL,
    expires_at         TEXT NOT NULL,
    revoked_at         TEXT
);
CREATE INDEX auth_sessions_user ON auth_sessions(user_id);

CREATE TABLE invites (
    id           TEXT PRIMARY KEY,
    code_sha256  TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TEXT NOT NULL,
    used_by      TEXT,
    used_at      TEXT
);

CREATE TABLE pairing_codes (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_sha256 TEXT NOT NULL UNIQUE,
    expires_at  TEXT NOT NULL,
    used_at     TEXT
);

CREATE TABLE seq_counters (
    user_id  TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    next_seq INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE works (
    id         TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title      TEXT NOT NULL DEFAULT '',
    author     TEXT NOT NULL DEFAULT '',
    pending    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, id)
);

CREATE TABLE editions (
    user_id    TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    page_count INTEGER,
    char_count INTEGER,
    meta_json  BLOB,
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
    seq          INTEGER NOT NULL,
    op_id        TEXT NOT NULL,
    work_id      TEXT NOT NULL,
    edition_sha  TEXT,
    device_id    TEXT NOT NULL,
    client_ts    TEXT NOT NULL,
    progression  REAL NOT NULL,
    locator_json BLOB,
    foreign_pos  TEXT,
    origin       TEXT NOT NULL,
    origin_alias TEXT,
    received_at  TEXT NOT NULL,
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
    started_at   TEXT NOT NULL,
    ended_at     TEXT NOT NULL,
    start_prog   REAL NOT NULL,
    end_prog     REAL NOT NULL,
    idle_ms      INTEGER NOT NULL DEFAULT 0,
    origin       TEXT NOT NULL,
    origin_alias TEXT,
    source_key   TEXT,
    received_at  TEXT NOT NULL,
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
    revision    INTEGER NOT NULL,
    session_id  TEXT NOT NULL,
    received_at TEXT NOT NULL,
    PRIMARY KEY (user_id, source_key, revision),
    FOREIGN KEY (user_id, session_id) REFERENCES sessions(user_id, session_id) ON DELETE CASCADE
);
CREATE INDEX supersessions_latest ON session_supersessions(user_id, source_key, revision DESC);

CREATE TABLE kosync_devices (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_slot TEXT NOT NULL,
    key_sha256  TEXT NOT NULL UNIQUE,
    label       TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT,
    PRIMARY KEY (user_id, device_slot)
);

CREATE TABLE koplugin_devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_sha256 TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL DEFAULT '',
    device_id    TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    revoked_at   TEXT
);

CREATE TABLE compaction_state (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    horizon INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
`

// migration2 adds per-(work, tz-local day) session aggregates and
// compact tombstones that preserve idempotency after raw rows age out.
const migration2 = `
CREATE TABLE session_rollups (
    user_id        TEXT NOT NULL,
    work_id        TEXT NOT NULL,
    day            TEXT NOT NULL,
    active_seconds REAL NOT NULL DEFAULT 0,
    pages          REAL NOT NULL DEFAULT 0,
    prog_delta     REAL NOT NULL DEFAULT 0,
    session_count  INTEGER NOT NULL DEFAULT 0,
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

UPDATE sessions
SET origin_alias = (
        SELECT MIN(o.origin_alias)
        FROM ops o
        WHERE o.user_id = sessions.user_id
          AND o.device_id = sessions.device_id
          AND o.received_at >= sessions.started_at
          AND o.received_at <= sessions.ended_at
          AND o.origin = 'kosync'
    ),
    work_id = (
        SELECT MIN(o.work_id)
        FROM ops o
        WHERE o.user_id = sessions.user_id
          AND o.device_id = sessions.device_id
          AND o.received_at >= sessions.started_at
          AND o.received_at <= sessions.ended_at
          AND o.origin = 'kosync'
    )
WHERE origin = 'inferred'
  AND edition_sha IS NULL
  AND origin_alias IS NULL
  AND 1 = (
      SELECT COUNT(DISTINCT o.work_id || char(31) || COALESCE(o.origin_alias, ''))
      FROM ops o
      WHERE o.user_id = sessions.user_id
        AND o.device_id = sessions.device_id
        AND o.received_at >= sessions.started_at
        AND o.received_at <= sessions.ended_at
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
        AND substr(ops.received_at, 1, 10) <= date(r.day, '+1 day')
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

CREATE TRIGGER tokens_scope_legacy_insert
AFTER INSERT ON tokens
WHEN NEW.scope <> ''
BEGIN
    INSERT OR IGNORE INTO token_scopes (token_id, user_id, scope)
    VALUES (NEW.id, NEW.user_id, NEW.scope);
END;
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
    config_json   BLOB,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
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
    created_at TEXT NOT NULL,
    PRIMARY KEY (library_id, user_id)
);
CREATE INDEX library_access_user ON library_access(user_id, library_id);

CREATE TABLE books (
    id                    TEXT PRIMARY KEY,
    library_id            TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    status                TEXT NOT NULL CHECK (status IN ('active', 'missing', 'trashed', 'review')),
    title                 TEXT NOT NULL DEFAULT '',
    title_source          TEXT NOT NULL DEFAULT '',
    title_locked          INTEGER NOT NULL DEFAULT 0 CHECK (title_locked IN (0, 1)),
    subtitle              TEXT NOT NULL DEFAULT '',
    subtitle_source       TEXT NOT NULL DEFAULT '',
    subtitle_locked       INTEGER NOT NULL DEFAULT 0 CHECK (subtitle_locked IN (0, 1)),
    description           TEXT NOT NULL DEFAULT '',
    description_source    TEXT NOT NULL DEFAULT '',
    description_locked    INTEGER NOT NULL DEFAULT 0 CHECK (description_locked IN (0, 1)),
    publisher             TEXT NOT NULL DEFAULT '',
    publisher_source      TEXT NOT NULL DEFAULT '',
    publisher_locked      INTEGER NOT NULL DEFAULT 0 CHECK (publisher_locked IN (0, 1)),
    published_date        TEXT NOT NULL DEFAULT '',
    published_date_source TEXT NOT NULL DEFAULT '',
    published_date_locked INTEGER NOT NULL DEFAULT 0 CHECK (published_date_locked IN (0, 1)),
    raw_metadata_json     BLOB,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    trashed_at            TEXT,
    trash_expires_at      TEXT,
    UNIQUE (library_id, id)
);
CREATE INDEX books_library_status ON books(library_id, status, created_at);

CREATE TABLE blobs (
    sha256      TEXT PRIMARY KEY,
    size_bytes  INTEGER NOT NULL CHECK (size_bytes >= 0),
    created_at  TEXT NOT NULL,
    orphaned_at TEXT
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
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
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
    bytes         INTEGER NOT NULL CHECK (bytes >= 0),
    created_at    TEXT NOT NULL,
    PRIMARY KEY (quota_user_id, blob_sha256)
);

CREATE TABLE book_identifiers (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    scheme     TEXT NOT NULL,
    value      TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
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
    created_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_series (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    series_id  TEXT NOT NULL,
    position   REAL,
    source     TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
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
    created_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_contributors (
    library_id    TEXT NOT NULL,
    book_id       TEXT NOT NULL,
    contributor_id TEXT NOT NULL,
    role          TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    source        TEXT NOT NULL,
    locked        INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
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
    created_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_tags (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    tag_id     TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
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
    created_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE book_genres (
    library_id TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    genre_id   TEXT NOT NULL,
    source     TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    PRIMARY KEY (book_id, genre_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE,
    FOREIGN KEY (library_id, genre_id)
        REFERENCES genres(library_id, id) ON DELETE CASCADE
);

CREATE TABLE book_languages (
    library_id  TEXT NOT NULL,
    book_id     TEXT NOT NULL,
    language    TEXT NOT NULL,
    source      TEXT NOT NULL,
    locked      INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
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
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE collection_books (
    library_id   TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    book_id      TEXT NOT NULL,
    position     INTEGER NOT NULL DEFAULT 0,
    added_at     TEXT NOT NULL,
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
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (library_id, id),
    UNIQUE (library_id, normalized_name)
);

CREATE TABLE reading_list_books (
    library_id     TEXT NOT NULL,
    reading_list_id TEXT NOT NULL,
    book_id        TEXT NOT NULL,
    position       INTEGER NOT NULL CHECK (position >= 0),
    added_at       TEXT NOT NULL,
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
    created_at TEXT NOT NULL,
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
    bytes_received       INTEGER NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
    content_sha256       TEXT,
    staging_path         TEXT,
    source_relative_path TEXT,
    book_library_id      TEXT,
    book_id              TEXT,
    error_code           TEXT,
    error_detail         TEXT,
    retry_count          INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    expires_at           TEXT,
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
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1);
`

const migration8 = `
UPDATE ingest_jobs
SET created_at =
        CASE
            WHEN instr(created_at, '.') = 0
                THEN substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
            ELSE substr(created_at, 1, instr(created_at, '.')) ||
                 substr(substr(created_at, instr(created_at, '.') + 1,
                               length(created_at) - instr(created_at, '.') - 1) ||
                        '000000000', 1, 9) || 'Z'
        END,
    updated_at =
        CASE
            WHEN instr(updated_at, '.') = 0
                THEN substr(updated_at, 1, length(updated_at) - 1) || '.000000000Z'
            ELSE substr(updated_at, 1, instr(updated_at, '.')) ||
                 substr(substr(updated_at, instr(updated_at, '.') + 1,
                               length(updated_at) - instr(updated_at, '.') - 1) ||
                        '000000000', 1, 9) || 'Z'
        END,
    expires_at =
        CASE
            WHEN expires_at IS NULL THEN NULL
            WHEN instr(expires_at, '.') = 0
                THEN substr(expires_at, 1, length(expires_at) - 1) || '.000000000Z'
            ELSE substr(expires_at, 1, instr(expires_at, '.')) ||
                 substr(substr(expires_at, instr(expires_at, '.') + 1,
                               length(expires_at) - instr(expires_at, '.') - 1) ||
                        '000000000', 1, 9) || 'Z'
        END;
`

const migration9 = `
ALTER TABLE ingest_jobs
    ADD COLUMN promotion_fingerprint TEXT;
ALTER TABLE ingest_jobs
    ADD COLUMN artifacts_expired INTEGER NOT NULL DEFAULT 0
        CHECK (artifacts_expired IN (0, 1));

CREATE UNIQUE INDEX ingest_jobs_id_quota
    ON ingest_jobs(id, quota_user_id);

CREATE TABLE ingest_blob_holds (
    job_id        TEXT PRIMARY KEY,
    quota_user_id TEXT NOT NULL,
    blob_sha256   TEXT NOT NULL,
    bytes         INTEGER NOT NULL CHECK (bytes >= 0),
    created_at    TEXT NOT NULL,
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
    ADD COLUMN artifact_cleanup_pending INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_cleanup_pending IN (0, 1));
`

const migration11 = `
ALTER TABLE blobs
    ADD COLUMN missing_at TEXT;
`

const migration12 = `
ALTER TABLE ingest_jobs
    ADD COLUMN extracted_embedded_metadata_json BLOB;
`

const migration13 = `
ALTER TABLE books
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1);
ALTER TABLE books
    ADD COLUMN identifiers_locked INTEGER NOT NULL DEFAULT 0
        CHECK (identifiers_locked IN (0, 1));
ALTER TABLE books
    ADD COLUMN languages_locked INTEGER NOT NULL DEFAULT 0
        CHECK (languages_locked IN (0, 1));
ALTER TABLE books
    ADD COLUMN tags_locked INTEGER NOT NULL DEFAULT 0
        CHECK (tags_locked IN (0, 1));
ALTER TABLE books
    ADD COLUMN genres_locked INTEGER NOT NULL DEFAULT 0
        CHECK (genres_locked IN (0, 1));
ALTER TABLE books
    ADD COLUMN series_locked INTEGER NOT NULL DEFAULT 0
        CHECK (series_locked IN (0, 1));
ALTER TABLE books
    ADD COLUMN contributors_locked INTEGER NOT NULL DEFAULT 0
        CHECK (contributors_locked IN (0, 1));
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
    ADD COLUMN source_seen_at TEXT;
ALTER TABLE book_files
    ADD COLUMN source_absent_at TEXT;
ALTER TABLE book_files
    ADD COLUMN source_modified_at TEXT;
ALTER TABLE books
    ADD COLUMN review_reason TEXT;
`

// migration15 adds the search index. It is a plain FTS5 table rather than
// an external-content one because what a book is findable by is not one
// table: the title lives on `books` and the author lives two joins away,
// and an external-content index can only mirror a single table's rows.
// The cost of owning the text is that every write which changes it must
// say so, which is why reindexing is a call rather than a trigger — a
// trigger on `books` alone would silently miss a rename two tables over.
//
// The tokenizer folds diacritics, so a library catalogued as "Émile" is
// found by somebody who cannot type the accent. Both backends are
// configured not to stem, so "reading" does not match "read" on one
// backend and not the other.
const migration15 = `
CREATE VIRTUAL TABLE book_search USING fts5(
    book_id UNINDEXED,
    library_id UNINDEXED,
    title,
    subtitle,
    description,
    publisher,
    people,
    subjects,
    tokenize = 'unicode61 remove_diacritics 2'
);
`

// migration16 makes administration an account property rather than a
// credential (ADR-0013). "Is an admin" was "holds a live admin-scope
// token", which cannot be granted without handing somebody a bearer
// secret, cannot be revoked without destroying unrelated scopes on a
// multi-scope token, and cannot be counted atomically — so the last
// administrator could be demoted twice concurrently and lock the
// instance out. The backfill is exactly the set the old definition
// returned true for, so no instance gains or loses an administrator
// here.
//
// disabled_at arrives in the same migration although nothing sets it
// yet: every rule written against is_admin says "enabled admin", and a
// guard that has to be rewritten when its second column appears is a
// guard written twice.
const migration16 = `
ALTER TABLE users
    ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0
        CHECK (is_admin IN (0, 1));
ALTER TABLE users
    ADD COLUMN disabled_at TEXT;
UPDATE users SET is_admin = 1 WHERE id IN (
    SELECT ts.user_id FROM token_scopes ts
    JOIN tokens t ON t.id = ts.token_id
    WHERE ts.scope = 'admin' AND t.revoked_at IS NULL
);
`

// migration17 splits libraries.kind into the three axes of ADR-0014.
// `kind` said two things at once: where books come from and how often
// the server looks again. A watch folder is not a kind of library, it is
// a refresh policy any library with a root can have, and welding the two
// together is why "a directory I index once" and "a Calibre library"
// could not be expressed at all.
//
// SQLite cannot alter a CHECK constraint, so the table is rebuilt. The
// definition below is the one from migration6 plus the new columns and
// minus `kind`; every index is recreated. Both indexes are dropped first
// because SQLite keeps index names unique per database, not per table.
//
// The rebuild is safe only because Migrate runs with foreign keys off
// and checks them again before committing — see the comment there.
//
// The values a CHECK admits widen with the phase that implements them:
// `in_place` arrives with the storage work and `calibre` with the
// Calibre source, so a database cannot hold a state no code honours.
const migration17 = `
DROP INDEX libraries_watched_root;
DROP INDEX libraries_owner;
ALTER TABLE libraries RENAME TO libraries_old;

CREATE TABLE libraries (
    id            TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source        TEXT NOT NULL CHECK (source IN ('managed', 'directory')),
    storage       TEXT NOT NULL CHECK (storage IN ('cas')),
    refresh       TEXT NOT NULL CHECK (refresh IN ('manual', 'interval')),
    refresh_interval_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (refresh_interval_seconds > 0),
    name          TEXT NOT NULL,
    root_path     TEXT,
    config_json   BLOB,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (id, quota_user_id),
    CHECK ((source = 'managed' AND root_path IS NULL) OR
           (source <> 'managed' AND root_path IS NOT NULL)),
    CHECK (source <> 'managed' OR refresh = 'manual')
);

INSERT INTO libraries (
    id, owner_user_id, quota_user_id, source, storage, refresh,
    refresh_interval_seconds, name, root_path, config_json,
    created_at, updated_at
)
SELECT id, owner_user_id, quota_user_id,
       CASE kind WHEN 'managed' THEN 'managed' ELSE 'directory' END,
       'cas',
       CASE kind WHEN 'managed' THEN 'manual' ELSE 'interval' END,
       900, name, root_path, config_json, created_at, updated_at
FROM libraries_old;

DROP TABLE libraries_old;

CREATE UNIQUE INDEX libraries_root
    ON libraries(root_path) WHERE root_path IS NOT NULL;
CREATE INDEX libraries_owner ON libraries(owner_user_id);
CREATE INDEX libraries_refresh_due
    ON libraries(refresh, source) WHERE refresh = 'interval';
`

// migration18 gives a file an identity that does not depend on the
// server owning a copy of it (ADR-0014).
//
// `blob_sha256` used to mean two things at once: which bytes this file
// is, and where the server's copy of them lives. An in-place file has
// the first and not the second, so the two are split. `content_sha256`
// and `content_size_bytes` are the identity — what duplicate detection,
// edition matching and the cover cache key are about — and are NOT NULL
// for every row. `blob_sha256` keeps its foreign key and now means "the
// CAS copy", which an in-place file does not have. A `CHECK` ties the
// storage mode to which of the two is present, so a row can never claim
// to be in place and own a blob at the same time.
//
// `ingest_jobs` gains the same storage column, so a worker can tell an
// in-place pass from a staged one, and both tables lose the word
// `watched` from their source: a file discovered on disk is `scanned`,
// and how often the server looks is now a separate axis.
//
// Three tables are rebuilt because SQLite cannot alter a CHECK: the two
// above, and `libraries` so that `storage` admits `in_place` from this
// phase on. Each definition is written against the schema as it stands
// now, not against migration 6, and each recreates every index it had.
// The rebuild is safe only because Migrate runs with foreign keys off
// and legacy_alter_table on, and checks foreign keys again before it
// commits — see the comment there.
const migration18 = `
DROP INDEX libraries_root;
DROP INDEX libraries_owner;
DROP INDEX libraries_refresh_due;
ALTER TABLE libraries RENAME TO libraries_old;

CREATE TABLE libraries (
    id            TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source        TEXT NOT NULL CHECK (source IN ('managed', 'directory')),
    storage       TEXT NOT NULL CHECK (storage IN ('cas', 'in_place')),
    refresh       TEXT NOT NULL CHECK (refresh IN ('manual', 'interval')),
    refresh_interval_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (refresh_interval_seconds > 0),
    name          TEXT NOT NULL,
    root_path     TEXT,
    config_json   BLOB,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (id, quota_user_id),
    CHECK ((source = 'managed' AND root_path IS NULL) OR
           (source <> 'managed' AND root_path IS NOT NULL)),
    CHECK (source <> 'managed' OR refresh = 'manual'),
    -- Bytes the server does not own can only be found again by a path,
    -- which a managed library does not have.
    CHECK (storage = 'cas' OR root_path IS NOT NULL)
);

INSERT INTO libraries
SELECT id, owner_user_id, quota_user_id, source, storage, refresh,
       refresh_interval_seconds, name, root_path, config_json,
       created_at, updated_at
FROM libraries_old;

DROP TABLE libraries_old;

CREATE UNIQUE INDEX libraries_root
    ON libraries(root_path) WHERE root_path IS NOT NULL;
CREATE INDEX libraries_owner ON libraries(owner_user_id);
CREATE INDEX libraries_refresh_due
    ON libraries(refresh, source) WHERE refresh = 'interval';

DROP INDEX book_files_book;
DROP INDEX book_files_blob;
DROP INDEX book_files_source_path;
ALTER TABLE book_files RENAME TO book_files_old;

CREATE TABLE book_files (
    id                   TEXT PRIMARY KEY,
    library_id           TEXT NOT NULL,
    book_id              TEXT NOT NULL,
    storage              TEXT NOT NULL CHECK (storage IN ('cas', 'in_place')),
    content_sha256       TEXT NOT NULL,
    content_size_bytes   INTEGER NOT NULL CHECK (content_size_bytes >= 0),
    blob_sha256          TEXT REFERENCES blobs(sha256) ON DELETE RESTRICT,
    source               TEXT NOT NULL CHECK (source IN ('upload', 'scanned')),
    source_relative_path TEXT,
    original_filename    TEXT NOT NULL DEFAULT '',
    media_type           TEXT NOT NULL DEFAULT 'application/epub+zip',
    partial_md5          TEXT,
    dc_identifier        TEXT,
    availability         TEXT NOT NULL
        CHECK (availability IN ('available', 'missing', 'superseded')),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    source_seen_at       TEXT,
    source_absent_at     TEXT,
    source_modified_at   TEXT,
    UNIQUE (library_id, id),
    CHECK ((storage = 'cas' AND blob_sha256 IS NOT NULL) OR
           (storage = 'in_place' AND blob_sha256 IS NULL AND
            source_relative_path IS NOT NULL)),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

INSERT INTO book_files (
    id, library_id, book_id, storage, content_sha256, content_size_bytes,
    blob_sha256, source, source_relative_path, original_filename,
    media_type, partial_md5, dc_identifier, availability,
    created_at, updated_at, source_seen_at, source_absent_at,
    source_modified_at
)
SELECT f.id, f.library_id, f.book_id, 'cas', f.blob_sha256,
       COALESCE(b.size_bytes, 0), f.blob_sha256,
       CASE f.source WHEN 'watched' THEN 'scanned' ELSE f.source END,
       f.source_relative_path, f.original_filename, f.media_type,
       f.partial_md5, f.dc_identifier, f.availability,
       f.created_at, f.updated_at, f.source_seen_at, f.source_absent_at,
       f.source_modified_at
FROM book_files_old f
LEFT JOIN blobs b ON b.sha256 = f.blob_sha256;

DROP TABLE book_files_old;

CREATE INDEX book_files_book ON book_files(library_id, book_id, availability);
CREATE INDEX book_files_blob ON book_files(blob_sha256)
    WHERE blob_sha256 IS NOT NULL;
CREATE INDEX book_files_content ON book_files(content_sha256);
CREATE INDEX book_files_source_path
    ON book_files(library_id, source_relative_path);

DROP INDEX ingest_jobs_client_key;
DROP INDEX ingest_jobs_state;
DROP INDEX ingest_jobs_library;
DROP INDEX ingest_jobs_id_quota;
ALTER TABLE ingest_jobs RENAME TO ingest_jobs_old;

CREATE TABLE ingest_jobs (
    id                   TEXT PRIMARY KEY,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id           TEXT NOT NULL,
    quota_user_id        TEXT NOT NULL,
    source               TEXT NOT NULL CHECK (source IN ('upload', 'scanned')),
    storage              TEXT NOT NULL DEFAULT 'cas'
        CHECK (storage IN ('cas', 'in_place')),
    client_key           TEXT,
    state                TEXT NOT NULL CHECK (state IN ('received', 'staged', 'validated', 'extracted', 'promoted', 'quarantined', 'failed')),
    bytes_received       INTEGER NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
    content_sha256       TEXT,
    staging_path         TEXT,
    source_relative_path TEXT,
    book_library_id      TEXT,
    book_id              TEXT,
    error_code           TEXT,
    error_detail         TEXT,
    retry_count          INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    expires_at           TEXT,
    request_fingerprint  TEXT NOT NULL DEFAULT '',
    revision             INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    promotion_fingerprint TEXT,
    artifacts_expired    INTEGER NOT NULL DEFAULT 0
        CHECK (artifacts_expired IN (0, 1)),
    artifact_cleanup_pending INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_cleanup_pending IN (0, 1)),
    extracted_embedded_metadata_json BLOB,
    CHECK ((book_library_id IS NULL AND book_id IS NULL) OR
           (book_library_id IS NOT NULL AND book_id IS NOT NULL AND
            book_library_id = library_id)),
    -- An in-place pass has no staged artifact to promote, so it has a
    -- source path instead: the file it read is the only copy there is.
    CHECK (storage = 'cas' OR source_relative_path IS NOT NULL),
    FOREIGN KEY (book_library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE SET NULL,
    FOREIGN KEY (library_id, quota_user_id)
        REFERENCES libraries(id, quota_user_id) ON DELETE CASCADE
);

INSERT INTO ingest_jobs (
    id, user_id, library_id, quota_user_id, source, storage, client_key,
    state, bytes_received, content_sha256, staging_path,
    source_relative_path, book_library_id, book_id, error_code,
    error_detail, retry_count, created_at, updated_at, expires_at,
    request_fingerprint, revision, promotion_fingerprint, artifacts_expired,
    artifact_cleanup_pending, extracted_embedded_metadata_json
)
SELECT id, user_id, library_id, quota_user_id,
       CASE source WHEN 'watched' THEN 'scanned' ELSE source END,
       'cas', client_key, state, bytes_received, content_sha256,
       staging_path, source_relative_path, book_library_id, book_id,
       error_code, error_detail, retry_count, created_at, updated_at,
       expires_at, request_fingerprint, revision, promotion_fingerprint,
       artifacts_expired, artifact_cleanup_pending,
       extracted_embedded_metadata_json
FROM ingest_jobs_old;

DROP TABLE ingest_jobs_old;

CREATE UNIQUE INDEX ingest_jobs_client_key
    ON ingest_jobs(user_id, library_id, client_key)
    WHERE client_key IS NOT NULL;
CREATE INDEX ingest_jobs_state ON ingest_jobs(state, updated_at);
CREATE INDEX ingest_jobs_library ON ingest_jobs(library_id, created_at);
CREATE UNIQUE INDEX ingest_jobs_id_quota ON ingest_jobs(id, quota_user_id);
`

var migrations = []string{
	schema, migration2, migration3, migration4, migration5, migration6,
	migration7, migration8, migration9, migration10, migration11, migration12,
	migration13, migration14, migration15, migration16, migration17,
	migration18, migration19, migration20,
}

// migration19 records what happened the last time a library's root was
// looked at. Until now a sweep was a global tick with no memory: every
// root was walked on the same interval, and a root that failed said so
// once in the log and nowhere else.
//
// Four nullable columns make a refresh a per-library event with a
// history. last_refresh_attempt_at is stamped when a refresh is claimed
// and is what the schedule is computed from, so a library whose refresh
// keeps failing backs off on its interval instead of spinning; the
// refresh a claim wins is exclusive because the claim is the update.
// refresh_requested_at is an administrator asking for one now, and is
// cleared by the claim that honours it — which is what makes "Refresh
// now" work for a manual library that has no schedule at all.
const migration19 = `
ALTER TABLE libraries ADD COLUMN last_refresh_at TEXT;
ALTER TABLE libraries ADD COLUMN last_refresh_attempt_at TEXT;
ALTER TABLE libraries ADD COLUMN last_refresh_error TEXT;
ALTER TABLE libraries ADD COLUMN refresh_requested_at TEXT;

CREATE INDEX libraries_refresh_requested
    ON libraries(refresh_requested_at)
    WHERE refresh_requested_at IS NOT NULL;
`

// migration20 is what a Calibre library needs the database to hold
// (ADR-0014).
//
// `libraries` is rebuilt once more, because SQLite cannot alter a CHECK
// and `source` must now admit `calibre`. The definition is the one from
// migration18 with the widened constraint and two new columns; every
// index is recreated.
//
// `last_inventory_digest` is the change gate. A Calibre refresh reads
// the whole inventory — book ids, modification times, and the size and
// mtime of each file — and hashes it to one value; when that value has
// not moved, the refresh stops without touching a catalog row. It is not
// a stat of metadata.db, because Calibre runs SQLite in WAL mode and a
// commit can leave that file untouched.
//
// `library_calibre_books` is identity. A Calibre row resolves to a
// catalog row through its Calibre book id and nothing else: matching by
// content digest would merge two deliberately distinct books, which
// ADR-0002 keeps apart. Unique on both sides, and cascading from the
// book, so a deleted book takes its mapping with it.
//
// The two `book_files` columns are the cover Calibre chose. A cover.jpg
// somebody picked beats one extracted from the EPUB, and the cache
// cannot be keyed by the publication digest alone: two Calibre books can
// share one EPUB and have different covers, so the key gains the cover's
// own digest.
const migration20 = `
DROP INDEX libraries_root;
DROP INDEX libraries_owner;
DROP INDEX libraries_refresh_due;
DROP INDEX libraries_refresh_requested;
ALTER TABLE libraries RENAME TO libraries_old;

CREATE TABLE libraries (
    id            TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source        TEXT NOT NULL
        CHECK (source IN ('managed', 'directory', 'calibre')),
    storage       TEXT NOT NULL CHECK (storage IN ('cas', 'in_place')),
    refresh       TEXT NOT NULL CHECK (refresh IN ('manual', 'interval')),
    refresh_interval_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (refresh_interval_seconds > 0),
    name          TEXT NOT NULL,
    root_path     TEXT,
    config_json   BLOB,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    last_refresh_at         TEXT,
    last_refresh_attempt_at TEXT,
    last_refresh_error      TEXT,
    refresh_requested_at    TEXT,
    last_inventory_digest   TEXT,
    UNIQUE (id, quota_user_id),
    CHECK ((source = 'managed' AND root_path IS NULL) OR
           (source <> 'managed' AND root_path IS NOT NULL)),
    CHECK (source <> 'managed' OR refresh = 'manual'),
    CHECK (storage = 'cas' OR root_path IS NOT NULL)
);

INSERT INTO libraries (
    id, owner_user_id, quota_user_id, source, storage, refresh,
    refresh_interval_seconds, name, root_path, config_json,
    created_at, updated_at, last_refresh_at, last_refresh_attempt_at,
    last_refresh_error, refresh_requested_at
)
SELECT id, owner_user_id, quota_user_id, source, storage, refresh,
       refresh_interval_seconds, name, root_path, config_json,
       created_at, updated_at, last_refresh_at, last_refresh_attempt_at,
       last_refresh_error, refresh_requested_at
FROM libraries_old;

DROP TABLE libraries_old;

CREATE UNIQUE INDEX libraries_root
    ON libraries(root_path) WHERE root_path IS NOT NULL;
CREATE INDEX libraries_owner ON libraries(owner_user_id);
CREATE INDEX libraries_refresh_due
    ON libraries(refresh, source) WHERE refresh = 'interval';
CREATE INDEX libraries_refresh_requested
    ON libraries(refresh_requested_at)
    WHERE refresh_requested_at IS NOT NULL;

CREATE TABLE library_calibre_books (
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    calibre_id INTEGER NOT NULL,
    book_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (library_id, calibre_id),
    UNIQUE (library_id, book_id),
    FOREIGN KEY (library_id, book_id)
        REFERENCES books(library_id, id) ON DELETE CASCADE
);

ALTER TABLE book_files ADD COLUMN cover_relative_path TEXT;
ALTER TABLE book_files ADD COLUMN cover_sha256 TEXT;
`
