package postgres

// schema is the whole database, in one migration.
//
// It is a baseline rather than a history, for the reason the SQLite copy
// gives: the project has never shipped, so ADR-0017 replaces
// twenty-two migrations with one description of the database as it
// should have been. The two backends are kept deliberately in step —
// same tables, same columns, same constraints — so that behaviour never
// depends on which database somebody picked.
//
// Composite, database-enforced FKs; the edition<->record FK is
// DEFERRABLE INITIALLY DEFERRED so split/merge stay single transactions.
const schema = `
-- ---------------------------------------------------------------------
-- Accounts and credentials
-- ---------------------------------------------------------------------

CREATE TABLE users (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    argon2_hash       TEXT NOT NULL,
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    kosync_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    koplugin_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    is_admin          BOOLEAN NOT NULL DEFAULT FALSE,
    disabled_at       TIMESTAMPTZ,
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

-- ---------------------------------------------------------------------
-- Reading identity and position sync
-- ---------------------------------------------------------------------

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
    user_id             TEXT NOT NULL,
    seq                 BIGINT NOT NULL,
    op_id               TEXT NOT NULL,
    work_id             TEXT NOT NULL,
    edition_sha         TEXT,
    device_id           TEXT NOT NULL,
    client_ts           TIMESTAMPTZ NOT NULL,
    progression         DOUBLE PRECISION NOT NULL,
    locator_json        BYTEA,
    foreign_pos         TEXT,
    origin              TEXT NOT NULL,
    origin_alias        TEXT,
    received_at         TIMESTAMPTZ NOT NULL,
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
    FOREIGN KEY (user_id, session_id)
        REFERENCES sessions(user_id, session_id) ON DELETE CASCADE
);
CREATE INDEX supersessions_latest
    ON session_supersessions(user_id, source_key, revision DESC);

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

CREATE TABLE compaction_state (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    horizon BIGINT NOT NULL DEFAULT 0
);

-- ---------------------------------------------------------------------
-- The catalog: folders on disk, reflected
-- ---------------------------------------------------------------------

CREATE TABLE folders (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    root_path  TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('plain', 'calibre')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX folders_root ON folders(root_path);

CREATE TABLE books (
    id                  TEXT PRIMARY KEY,
    folder_id           TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    status              TEXT NOT NULL CHECK (status IN ('active', 'missing')),

    relative_path       TEXT NOT NULL,
    size_bytes          BIGINT NOT NULL CHECK (size_bytes >= 0),
    mtime               TIMESTAMPTZ NOT NULL,
    content_sha256      TEXT NOT NULL,
    original_filename   TEXT NOT NULL DEFAULT '',
    media_type          TEXT NOT NULL DEFAULT 'application/epub+zip',
    calibre_id          BIGINT,
    cover_relative_path TEXT,
    cover_sha256        TEXT,

    title               TEXT NOT NULL DEFAULT '',
    subtitle            TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    publisher           TEXT NOT NULL DEFAULT '',
    published_date      TEXT NOT NULL DEFAULT '',

    search_vector       tsvector,

    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    seen_at             TIMESTAMPTZ,
    absent_at           TIMESTAMPTZ,
    UNIQUE (folder_id, id)
);
CREATE INDEX books_folder_status ON books(folder_id, status, created_at);
CREATE INDEX books_content ON books(content_sha256);
CREATE INDEX books_search_idx ON books USING GIN (search_vector);
CREATE UNIQUE INDEX books_folder_path ON books(folder_id, relative_path);
CREATE UNIQUE INDEX books_folder_calibre
    ON books(folder_id, calibre_id) WHERE calibre_id IS NOT NULL;

-- Series, tags and contributors belong to the library rather than to a
-- folder (ADR-0019); see the SQLite copy for why.
CREATE TABLE series (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (normalized_name)
);

CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (normalized_name)
);

CREATE TABLE contributors (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (normalized_name)
);

-- Membership keeps its folder_id for the composite key to books; only
-- the entity side is library-wide (ADR-0019).
CREATE TABLE book_series (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    position  DOUBLE PRECISION,
    PRIMARY KEY (book_id, series_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
);

CREATE TABLE book_tags (
    folder_id TEXT NOT NULL,
    book_id   TEXT NOT NULL,
    tag_id    TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
);

-- A series claim (ADR-0018); see the SQLite copy for why the claim is a
-- row of its own and why scope_user carries no foreign key.
CREATE TABLE book_series_overrides (
    folder_id  TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    scope_user TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    updated_by TEXT NOT NULL,
    PRIMARY KEY (book_id, scope_user),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
);
CREATE INDEX book_series_overrides_scope
    ON book_series_overrides(scope_user, folder_id);

CREATE TABLE book_series_override_items (
    folder_id  TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    scope_user TEXT NOT NULL,
    series_id  TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    position   DOUBLE PRECISION,
    PRIMARY KEY (book_id, scope_user, series_id),
    FOREIGN KEY (book_id, scope_user)
        REFERENCES book_series_overrides(book_id, scope_user) ON DELETE CASCADE
);
CREATE INDEX book_series_override_items_series
    ON book_series_override_items(series_id, scope_user);

-- A renamed series (ADR-0020); see the SQLite copy for why the scanned
-- name stays the fold key and why normalized_name is stored.
CREATE TABLE series_name_overrides (
    series_id       TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    scope_user      TEXT NOT NULL,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    updated_by      TEXT NOT NULL,
    PRIMARY KEY (series_id, scope_user)
);
CREATE INDEX series_name_overrides_scope
    ON series_name_overrides(scope_user, normalized_name);

-- Where an observed series name is bound (ADR-0021); see the SQLite copy
-- for why a merge is a binding rather than a delete, and why a binding
-- outlives the name that produced it.
CREATE TABLE series_bindings (
    id              TEXT PRIMARY KEY,
    folder_id       TEXT REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    series_id       TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL,
    created_by      TEXT NOT NULL
);
CREATE UNIQUE INDEX series_bindings_key
    ON series_bindings(COALESCE(folder_id, ''), normalized_name);
CREATE INDEX series_bindings_series ON series_bindings(series_id);

CREATE TABLE book_contributors (
    folder_id      TEXT NOT NULL,
    book_id        TEXT NOT NULL,
    contributor_id TEXT NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
    role           TEXT NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, contributor_id, role),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE
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

CREATE TABLE user_book_works (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id  TEXT NOT NULL,
    book_id    TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, book_id),
    FOREIGN KEY (folder_id, book_id)
        REFERENCES books(folder_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, work_id)
        REFERENCES works(user_id, id) ON DELETE CASCADE
);
CREATE INDEX user_book_works_work ON user_book_works(user_id, work_id);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
);
`

// claimRevisions is the SQLite copy's migration 2; see it for why these
// columns arrive as a migration rather than as an edit to the baseline.
const claimRevisions = `
ALTER TABLE book_series_overrides ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE book_series_overrides ADD COLUMN client_ts TEXT;
ALTER TABLE book_series_overrides ADD COLUMN request_hash TEXT;
`

// folderUploads is the SQLite copy's migration 3 (ADR-0023).
const folderUploads = `
ALTER TABLE folders ADD COLUMN accepts_uploads BOOLEAN NOT NULL DEFAULT FALSE;
`

// folderAccess is migration 4. Grants are explicit: no account and no
// folder receives one implicitly, and administrator status confers none
// (ADR-0027). Migration 6 backfills the accounts this left stranded, and
// ADR-0029 gives a new folder a grant for the account named when it was
// created.
const folderAccess = `
CREATE TABLE user_folders (
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, folder_id)
);

CREATE INDEX user_folders_folder ON user_folders(folder_id);
`

// annotationSync is migration 5 (ADR-0028); see the SQLite copy for
// what an annotation is and why most of its columns are nullable.
const annotationSync = `
ALTER TABLE seq_counters ADD COLUMN next_annotation_seq BIGINT NOT NULL DEFAULT 1;

CREATE TABLE annotations (
    user_id      TEXT NOT NULL,
    id           TEXT NOT NULL,
    seq          BIGINT NOT NULL,
    rev          BIGINT NOT NULL,
    work_id      TEXT NOT NULL,
    edition_sha  TEXT,
    kind         TEXT NOT NULL CHECK (kind IN ('highlight', 'note', 'bookmark')),
    locator_json BYTEA,
    progression  DOUBLE PRECISION,
    excerpt      TEXT NOT NULL DEFAULT '',
    color        TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    device_id    TEXT NOT NULL DEFAULT '',
    client_ts    TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL,
    deleted_at   TIMESTAMPTZ,
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, seq),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, edition_sha, work_id)
        REFERENCES editions(user_id, sha256, work_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX annotations_work ON annotations(user_id, work_id, deleted_at);
CREATE INDEX annotations_tombstones ON annotations(user_id, deleted_at)
    WHERE deleted_at IS NOT NULL;
`

// folderBackfill is migration 6 (ADR-0029); see the SQLite copy for the
// argument. The guard fires only when no grant exists anywhere, so a
// server where anybody has assigned anything is left alone.
const folderBackfill = `
INSERT INTO user_folders (user_id, folder_id)
SELECT u.id, f.id FROM users u CROSS JOIN folders f
WHERE NOT EXISTS (SELECT 1 FROM user_folders);
`

const statisticsStorage = `
ALTER TABLE sessions ADD COLUMN active_ms BIGINT;
ALTER TABLE sessions ADD COLUMN reported_pages DOUBLE PRECISION;

CREATE TABLE stats_revisions (
    user_id  TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL DEFAULT 0
);
INSERT INTO stats_revisions (user_id, revision)
SELECT id, 0 FROM users
ON CONFLICT (user_id) DO NOTHING;

CREATE TABLE session_rollups_v2 (
    user_id                 TEXT NOT NULL,
    work_id                 TEXT NOT NULL,
    day                     TEXT NOT NULL,
    timezone                TEXT NOT NULL,
    attribution_version     BIGINT NOT NULL DEFAULT 2 CHECK (attribution_version = 2),
    active_seconds          DOUBLE PRECISION NOT NULL DEFAULT 0,
    pages                   DOUBLE PRECISION NOT NULL DEFAULT 0,
    prog_delta              DOUBLE PRECISION NOT NULL DEFAULT 0,
    session_count           BIGINT NOT NULL DEFAULT 0,
    measured_active_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    measured_prog_delta     DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, work_id, day, timezone),
    FOREIGN KEY (user_id, work_id) REFERENCES works(user_id, id) ON DELETE CASCADE
);
CREATE INDEX session_rollups_v2_user_day ON session_rollups_v2(user_id, day);

ALTER TABLE session_tombstones ADD COLUMN work_id TEXT;
ALTER TABLE session_tombstones ADD COLUMN day TEXT;
ALTER TABLE session_tombstones ADD COLUMN timezone TEXT;
ALTER TABLE session_tombstones ADD COLUMN attribution_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE session_tombstones ADD COLUMN present BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE session_tombstones ADD COLUMN active_seconds DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE session_tombstones ADD COLUMN pages DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE session_tombstones ADD COLUMN prog_delta DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE session_tombstones ADD COLUMN measured_active_seconds DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE session_tombstones ADD COLUMN measured_prog_delta DOUBLE PRECISION NOT NULL DEFAULT 0;
CREATE INDEX session_tombstones_user_work ON session_tombstones(user_id, work_id);

CREATE OR REPLACE FUNCTION ensure_stats_revision_row()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO stats_revisions (user_id, revision) VALUES (NEW.id, 0)
    ON CONFLICT (user_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bump_stats_revision(p_user_id TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO stats_revisions (user_id, revision)
    SELECT p_user_id, 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = p_user_id)
    ON CONFLICT (user_id) DO UPDATE SET revision = stats_revisions.revision + 1;
END;
$$;

CREATE OR REPLACE FUNCTION bump_stats_revision_new()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM bump_stats_revision(NEW.user_id);
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bump_stats_revision_old()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM bump_stats_revision(OLD.user_id);
    RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION bump_stats_revision_user_timezone()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM bump_stats_revision(NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER stats_revisions_users_insert
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION ensure_stats_revision_row();
CREATE TRIGGER stats_revisions_users_timezone_update
AFTER UPDATE OF timezone ON users
FOR EACH ROW WHEN (OLD.timezone IS DISTINCT FROM NEW.timezone)
EXECUTE FUNCTION bump_stats_revision_user_timezone();

CREATE TRIGGER stats_revisions_sessions_insert AFTER INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_sessions_update AFTER UPDATE ON sessions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_sessions_delete AFTER DELETE ON sessions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_supersessions_insert AFTER INSERT ON session_supersessions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_supersessions_delete AFTER DELETE ON session_supersessions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_rollups_insert AFTER INSERT ON session_rollups FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_rollups_update AFTER UPDATE ON session_rollups FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_rollups_delete AFTER DELETE ON session_rollups FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_rollups_v2_insert AFTER INSERT ON session_rollups_v2 FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_rollups_v2_update AFTER UPDATE ON session_rollups_v2 FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_rollups_v2_delete AFTER DELETE ON session_rollups_v2 FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_tombstones_insert AFTER INSERT ON session_tombstones FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_tombstones_update AFTER UPDATE ON session_tombstones FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_tombstones_delete AFTER DELETE ON session_tombstones FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_ops_insert AFTER INSERT ON ops FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_ops_update AFTER UPDATE ON ops FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_ops_delete AFTER DELETE ON ops FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_works_insert AFTER INSERT ON works FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_works_update AFTER UPDATE ON works FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_works_delete AFTER DELETE ON works FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
CREATE TRIGGER stats_revisions_editions_insert AFTER INSERT ON editions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_editions_update AFTER UPDATE ON editions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_new();
CREATE TRIGGER stats_revisions_editions_delete AFTER DELETE ON editions FOR EACH ROW EXECUTE FUNCTION bump_stats_revision_old();
`

// migrations is append-only, for the reason the SQLite copy gives.
var migrations = []string{
	schema, claimRevisions, folderUploads, folderAccess, annotationSync,
	folderBackfill, statisticsStorage,
}
