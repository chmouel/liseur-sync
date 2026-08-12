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

var migrations = []string{schema, migration2, migration3, migration4, migration5}
