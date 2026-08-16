package sqlite

// schema is the whole database, in one migration.
//
// It is a baseline rather than a history. The project has never shipped,
// so there is no deployment whose data has to be carried forward, and
// ADR-0017 takes that freedom in full: a database written by an earlier
// build is deleted, not upgraded. What used to be twenty-two migrations
// — most of them undoing the ingest pipeline's own complexity — is one
// statement describing the database as it should have been.
//
// Composite, database-enforced foreign keys throughout; the
// edition<->record FK is DEFERRABLE INITIALLY DEFERRED so split/merge
// stay single transactions.
const schema = `
-- ---------------------------------------------------------------------
-- Accounts and credentials
-- ---------------------------------------------------------------------

CREATE TABLE users (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    argon2_hash       TEXT NOT NULL,
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    kosync_enabled    INTEGER NOT NULL DEFAULT 1,
    koplugin_enabled  INTEGER NOT NULL DEFAULT 1,
    is_admin          INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    disabled_at       TEXT,
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

CREATE TRIGGER tokens_scope_legacy_insert
AFTER INSERT ON tokens
WHEN NEW.scope <> ''
BEGIN
    INSERT OR IGNORE INTO token_scopes (token_id, user_id, scope)
    VALUES (NEW.id, NEW.user_id, NEW.scope);
END;

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

-- ---------------------------------------------------------------------
-- Reading identity and position sync
-- ---------------------------------------------------------------------

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
    user_id             TEXT NOT NULL,
    seq                 INTEGER NOT NULL,
    op_id               TEXT NOT NULL,
    work_id             TEXT NOT NULL,
    edition_sha         TEXT,
    device_id           TEXT NOT NULL,
    client_ts           TEXT NOT NULL,
    progression         REAL NOT NULL,
    locator_json        BLOB,
    foreign_pos         TEXT,
    origin              TEXT NOT NULL,
    origin_alias        TEXT,
    received_at         TEXT NOT NULL,
    inferred_session_id TEXT,
    PRIMARY KEY (user_id, seq),
    UNIQUE (user_id, op_id),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, edition_sha, work_id)
        REFERENCES editions(user_id, sha256, work_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX ops_work_seq ON ops(user_id, work_id, seq);
CREATE INDEX ops_device_received ON ops(user_id, device_id, received_at);
CREATE INDEX idx_ops_inference_pending
    ON ops(user_id, received_at)
    WHERE origin = 'kosync' AND inferred_session_id IS NULL;

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
    FOREIGN KEY (user_id, session_id)
        REFERENCES sessions(user_id, session_id) ON DELETE CASCADE
);
CREATE INDEX supersessions_latest
    ON session_supersessions(user_id, source_key, revision DESC);

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

CREATE TABLE compaction_state (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    horizon INTEGER NOT NULL DEFAULT 0
);

-- ---------------------------------------------------------------------
-- The catalog: folders on disk, reflected
-- ---------------------------------------------------------------------

-- A folder is a directory an administrator pointed at. It has no owner,
-- no quota principal and no access list: every signed-in account sees
-- every folder's books, and only an administrator sees the path
-- (ADR-0017). Nothing beneath root_path is ever written.
--
-- kind decides how the folder is read. 'calibre' means metadata.db sits
-- at the root and is the source of both discovery and metadata;
-- 'plain' means the tree is walked for EPUBs.
CREATE TABLE folders (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    root_path  TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('plain', 'calibre')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX folders_root ON folders(root_path);

-- One book is one file. The separate book_files table existed because a
-- book could have an uploaded copy, a content-addressed blob and a
-- source path at once; with uploads and content-addressed storage gone
-- there is exactly one publication per row and the join was pure cost.
--
-- status is 'active' or 'missing' and nothing else. A missing book is
-- one a completed scan did not find; it is kept, not deleted, because
-- the file usually comes back.
--
-- relative_path is slash-separated and relative to the folder root, so
-- it is the same string everywhere and is what an open is rooted at.
-- size_bytes and mtime are the cheap change gate for a plain folder.
-- content_sha256 is the publication's digest and is what the cover
-- cache is keyed by. calibre_id is the identity of a book in a Calibre
-- folder and is null everywhere else. cover_sha256 proves a curated
-- cover.jpg has not been replaced under a cache key naming the old one.
CREATE TABLE books (
    id                  TEXT PRIMARY KEY,
    folder_id           TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    status              TEXT NOT NULL CHECK (status IN ('active', 'missing')),

    relative_path       TEXT NOT NULL,
    size_bytes          INTEGER NOT NULL CHECK (size_bytes >= 0),
    mtime               TEXT NOT NULL,
    content_sha256      TEXT NOT NULL,
    original_filename   TEXT NOT NULL DEFAULT '',
    media_type          TEXT NOT NULL DEFAULT 'application/epub+zip',
    calibre_id          INTEGER,
    cover_relative_path TEXT,
    cover_sha256        TEXT,

    title               TEXT NOT NULL DEFAULT '',
    subtitle            TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    publisher           TEXT NOT NULL DEFAULT '',
    published_date      TEXT NOT NULL DEFAULT '',

    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    seen_at             TEXT,
    absent_at           TEXT,
    UNIQUE (folder_id, id)
);
CREATE INDEX books_folder_status ON books(folder_id, status, created_at);
CREATE INDEX books_content ON books(content_sha256);

-- The two identity keys, one per folder kind. They are unique because
-- rule 4 of ADR-0017 says replacing a file is a delete followed by an
-- insert, never two rows briefly claiming the same book.
CREATE UNIQUE INDEX books_folder_path ON books(folder_id, relative_path);
CREATE UNIQUE INDEX books_folder_calibre
    ON books(folder_id, calibre_id) WHERE calibre_id IS NOT NULL;

CREATE TABLE series (
    id              TEXT PRIMARY KEY,
    folder_id       TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    UNIQUE (folder_id, id),
    UNIQUE (folder_id, normalized_name)
);

CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    folder_id       TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    UNIQUE (folder_id, id),
    UNIQUE (folder_id, normalized_name)
);

CREATE TABLE contributors (
    id              TEXT PRIMARY KEY,
    folder_id       TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    UNIQUE (folder_id, id),
    UNIQUE (folder_id, normalized_name)
);

-- The relation tables carry no source and no lock. Both existed to rank
-- competing claims about a field — a filename guess against an EPUB
-- against an external provider against a human edit. With editing and
-- providers gone there is one source per folder kind and nothing to
-- rank, so a pass simply states what the folder says.
CREATE TABLE book_series (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    series_id TEXT NOT NULL,
    position  REAL,
    PRIMARY KEY (book_id, series_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id, series_id)
        REFERENCES series(folder_id, id) ON DELETE CASCADE
);

CREATE TABLE book_tags (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    tag_id    TEXT NOT NULL,
    PRIMARY KEY (book_id, tag_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id, tag_id)
        REFERENCES tags(folder_id, id) ON DELETE CASCADE
);

CREATE TABLE book_contributors (
    folder_id      TEXT NOT NULL,
    book_id        TEXT NOT NULL,
    contributor_id TEXT NOT NULL,
    role           TEXT NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, contributor_id, role),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id, contributor_id)
        REFERENCES contributors(folder_id, id) ON DELETE CASCADE
);

CREATE TABLE book_identifiers (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    scheme    TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY (book_id, scheme, value),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
);
CREATE INDEX book_identifiers_lookup
    ON book_identifiers(folder_id, scheme, value);

CREATE TABLE book_languages (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    language  TEXT NOT NULL,
    PRIMARY KEY (book_id, language),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE book_search USING fts5(
    book_id UNINDEXED,
    folder_id UNINDEXED,
    title,
    subtitle,
    description,
    publisher,
    people,
    subjects,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- The bridge between the shared catalog and per-user reading state. The
-- catalog is deliberately shared; a reading position never is.
CREATE TABLE user_book_works (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id  TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, book_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, work_id)
        REFERENCES works(user_id, id) ON DELETE CASCADE
);
CREATE INDEX user_book_works_work ON user_book_works(user_id, work_id);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
`

// migrations is deliberately one element long. A second entry may be
// appended once this has shipped somewhere; until then, changing the
// schema means editing the baseline and throwing the database away.
var migrations = []string{schema}
