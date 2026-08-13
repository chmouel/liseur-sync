# Deploying liseur-sync

## Install script

`scripts/install.sh` automates the two most common setups: Docker
Compose (sqlite or bundled-postgres profile) when Docker is present,
and a rootless Podman + systemd user quadlet otherwise (it offers to
install podman). It starts the server, waits for `/healthz`, and
optionally creates the first user, a device token, and a kosync
pairing code.

```
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

Knobs: `LISEUR_VERSION` (image tag, default `latest`), `LISEUR_REF`
(git ref for the fetched `compose.yaml`, default `main`),
`LISEUR_COMPOSE_URL` (full URL override), and `--yes --db=…
--runtime=… --port=…` for non-interactive runs. The rest of this
document applies regardless of how the server was installed.

## Postures

One static binary, one config file, one database, and one private content
directory. Three supported
database setups, all covered by `compose.yaml`:

| Posture | Command | Notes |
|---|---|---|
| SQLite (default) | `docker compose --profile sqlite up -d` | Database and CAS share the persistent app volume |
| Bundled Postgres | `docker compose --profile postgres up -d` | Set `POSTGRES_PASSWORD` in `.env`; CAS uses a separate persistent volume |
| External Postgres | `docker compose --profile external up -d` | Set `LISEUR_DATABASE_URL` in `.env`; the database and role must exist, with DDL rights (migrations run at startup); CAS remains local and persistent |

Or run the binary directly: `liseur-sync serve -config liseur-sync.toml`.
`content.root` defaults to `./content`; it must be owned by the server user
and have mode `0700`, since anything looser exposes every stored book. The
server creates it that way; a directory you make yourself gets `0755` from
`mkdir` and is refused, with a message naming the directory and the fix.
Startup also runs recovery for every pre-existing nonterminal ingest, and
reclaims uploads a previous crash left half-received, before listening.
When SQLite uses an absolute database path, a relative content root resolves
beside that database; container deployments still set `/data/content`
explicitly.

## TLS

Terminate TLS at a reverse proxy. The app publishes on localhost only.
Add your proxy's addresses to `trusted_proxies` so the app can see the
real scheme; without that it refuses credential traffic as insecure —
that includes the whole `/ui` surface, not just the login form, and the
session cookie is issued with `Secure` unless `insecure_http` is set.

Caddy example:

```
reader.example.com {
    reverse_proxy 127.0.0.1:8585
}
```

nginx example — the koplugin capability URLs carry a secret in the
path. The app redacts it from its own logs; do the same at the proxy:

```nginx
map $uri $redacted_uri {
    ~^/adapter/koplugin/(?<cap>[^/]+)(?<rest>/.*)$  /adapter/koplugin/[redacted]$rest;
    default  $uri;
}
log_format redacted '$remote_addr - $remote_user [$time_local] '
                    '"$request_method $redacted_uri $server_protocol" $status';
access_log /var/log/nginx/liseur-sync.log redacted;

location / {
    proxy_pass http://127.0.0.1:8585;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

`insecure_http = true` exists only for LAN-only setups where TLS is
genuinely out of scope. It is a top-level key, so it must appear above
the first `[table]` header in the config file — TOML binds a bare key to
the table above it, and `insecure_http` written under `[content]` becomes
`content.insecure_http`. The server refuses to start on an unrecognized
key rather than ignoring one, so a misplaced setting is reported instead
of silently doing nothing.

### Optional: extra auth at the proxy

The API is fully authenticated by design, but you can put an extra
basicauth layer in front of the whole path for non-LAN clients (the
civuole deployment does this with Caddy's `import auth` snippet; LAN
devices bypass it). Caveat: kosync clients cannot send HTTP basic-auth
headers, so this only works while KOReader devices sync from the LAN.
If one ever needs WAN access, exempt `/adapter/*` from the proxy auth
— the adapter authenticates with its own credentials either way.

## First run

```
liseur-sync admin -config liseur-sync.toml create-user alice
liseur-sync admin -config liseur-sync.toml mint-token alice "Boox Palma"
liseur-sync admin -config liseur-sync.toml pairing-code alice      # for KOReader kosync
liseur-sync admin -config liseur-sync.toml koplugin-device alice kobo  # stats plugin
```

## Reading statistics for books nobody has opened yet

A book is joined to a reader's sync work the first time a client resolves
it, so a freshly imported library reports no reading statistics until each
book has been opened at least once. To map a whole catalog in one pass:

```
liseur-sync admin -config liseur-sync.toml backfill-works alice
```

It is safe to re-run and reports what it did. Books that match an existing
work on title and author alone are counted as `needs-confirmation` and
left unmapped — only the reader can say whether two similarly titled books
are the same one, and a wrong guess merges two reading histories.

## Sizing the content disk

Uploads land in `content.root/.incoming` while they are received and
verified, and only then move to permanent storage. `max_staging_bytes`
(8 GiB by default) bounds what that directory holds at once; uploads that
would exceed it are answered `503` with `Retry-After`. Neither
`max_upload_bytes` nor a user's `quota_bytes` can do this job, since every
upload can be inside both and still, together, fill the disk.

Size it for the concurrency you expect — it must be at least
`max_upload_bytes`, or no upload could ever fit — and leave the permanent
library room to grow beside it. Setting it to `0` restores unbounded
staging, which only makes sense where the disk is bounded another way.

## Backup

The database and `content.root` are one backup unit. Until maintenance mode
is implemented, stop the app process before backup so ingestion recovery or
future uploads cannot change the CAS:

1. back up the database first;
   - **SQLite:** use `sqlite3 liseur-sync.db '.backup /backups/ls.db'`;
     never copy live `.db`/`.wal` files;
   - **Postgres:** use `pg_dump` as usual;
2. copy the content directory while the app remains stopped, with a tool
   that preserves permissions (`cp -a`, `tar -p`, `rsync -a`). A copy that
   widens them is refused on restore: the CAS requires a private root
   (`chmod 700`), and a world-readable content directory is a library
   anybody with a shell can read;
3. check the copy is restorable:

   ```
   liseur-sync admin -config backup-copy.toml verify-backup
   ```

   Point the config at the copy — its database and its content directory.
   The command compares the two and reports any blob the database
   references that the backup does not hold, holds at the wrong size, or
   holds with damaged bytes, naming each digest. It exits non-zero when
   the backup cannot be restored from, so a backup script can act on it.
   It changes neither side, so it is also safe to run against the live
   server.

## Upgrades

Migrations run at startup under a cross-process lock. If a migration
fails, the server refuses to start (non-zero exit, clear log) rather
than run against a partially migrated schema. Back up before upgrades.

Compaction and session rollups delete old rows, and SQLite reuses those
freed pages for future writes, so steady-state growth is bounded. The
database file does not automatically shrink below its high-water size;
use the documented `VACUUM INTO` backup procedure if physical shrinking
is ever needed.
