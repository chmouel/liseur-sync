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

var migrations = []string{schema}
