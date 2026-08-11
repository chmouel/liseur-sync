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

One static binary, one config file, one database. Three supported
database setups, all covered by `compose.yaml`:

| Posture | Command | Notes |
|---|---|---|
| SQLite (default) | `docker compose --profile sqlite up -d` | Single file in a named volume |
| Bundled Postgres | `docker compose --profile postgres up -d` | Set `POSTGRES_PASSWORD` in `.env` |
| External Postgres | `docker compose --profile external up -d` | Set `LISEUR_DATABASE_URL` in `.env`; the database and role must exist, with DDL rights (migrations run at startup) |

Or run the binary directly: `liseur-sync serve -config liseur-sync.toml`.

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
genuinely out of scope.

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

## Backup

- **SQLite:** `sqlite3 liseur-sync.db 'VACUUM INTO "/backups/ls.db"'` —
  or the `.backup` command. Never copy the live `.db`/`.wal` files.
- **Postgres:** `pg_dump` as usual.

## Upgrades

Migrations run at startup under a cross-process lock. If a migration
fails, the server refuses to start (non-zero exit, clear log) rather
than run against a partially migrated schema. Back up before upgrades.

Compaction and session rollups delete old rows, and SQLite reuses those
freed pages for future writes, so steady-state growth is bounded. The
database file does not automatically shrink below its high-water size;
use the documented `VACUUM INTO` backup procedure if physical shrinking
is ever needed.
