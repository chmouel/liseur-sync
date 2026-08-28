# Deploying liseur-sync

## Install script

`scripts/install.sh` automates the two most common setups: Docker
Compose (sqlite or bundled-postgres profile) when Docker is present, and
a rootless Podman + systemd user quadlet otherwise (it offers to install
podman). It starts the server, waits for `/healthz`, and optionally
creates the first user, a device token, and a kosync pairing code.

```
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

Knobs: `LISEUR_VERSION` (image tag, default `latest`), `LISEUR_REF` (git
ref for the fetched `compose.yaml`, default `main`),
`LISEUR_COMPOSE_URL` (full URL override), and `--yes --db=… --runtime=…
--port=…` for non-interactive runs. The rest of this document applies
regardless of how the server was installed.

## Postures

One static binary, one config file, one database, one disposable cover
cache, and one or more folders that already hold books. Three supported
database setups are covered by `compose.yaml`:

| Posture | Command | Notes |
|---|---|---|
| SQLite (default) | `docker compose --profile sqlite up -d` | Database and cache share the persistent app volume |
| Bundled Postgres | `docker compose --profile postgres up -d` | Set `POSTGRES_PASSWORD` in `.env`; the cache remains local |
| External Postgres | `docker compose --profile external up -d` | Set `LISEUR_DATABASE_URL` in `.env`; the database and role must exist, with DDL rights |

Or run the binary directly: `liseur-sync serve -config
liseur-sync.toml`. Setting `LISEUR_CONFIG` instead is equivalent
when `-config` is omitted, which is more convenient for
compose/systemd units that only want to inject an environment
variable.

`[content].cache_dir` defaults to `./cache`. It holds rendered covers
and nothing else. It is safe to delete while the server is running; the
cost is a re-render on the next cover request. Do not put books there.
Books are read from the folders you register.

## TLS

Terminate TLS at a reverse proxy. The app publishes on localhost only.
Add your proxy's addresses to `trusted_proxies` so the app can see the
real scheme; without that it refuses credential traffic as insecure.
That includes the whole `/ui` surface, not just the login form, and the
session cookie is issued with `Secure` unless `insecure_http` is set.
The same trust powers the per-IP rate limits: behind a trusted proxy
they key on the `X-Forwarded-For` client rather than the proxy's own
address, so one visitor probing the login cannot exhaust everyone
else's budget. A forwarded header from any other peer is ignored.

Caddy example:

```
reader.example.com {
    reverse_proxy 127.0.0.1:8585
}
```

### Serving below a path prefix

The UI can be served below a stripped prefix, but OPDS acquisition and
cover links are rooted at `/opds/v1.2`. If a reverse proxy exposes the
server below `/sync` with a path-stripping rule, also route the emitted
`/opds*` links without stripping that prefix:

```caddy
reader.example.com {
    redir /sync /sync/

    handle /opds* {
        reverse_proxy 127.0.0.1:8585
    }

    handle_path /sync* {
        reverse_proxy 127.0.0.1:8585
    }
}
```

Use `handle`, rather than `handle_path`, for `/opds*`: liseur-sync
expects the upstream request to retain `/opds/v1.2`. The root OPDS route
is protected by liseur-sync's own HTTP Basic authentication, just like
the prefixed route.

nginx example: the koplugin capability URLs carry a secret in the path.
The app redacts it from its own logs; do the same at the proxy:

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
out of scope. It is a top-level key, so it must appear above
the first `[table]` header in the config file; TOML binds a bare key to
the table above it, and `insecure_http` written under `[content]`
becomes `content.insecure_http`. The server refuses to start on an
unrecognized key rather than ignoring one, so a misplaced setting is
reported instead of silently doing nothing.

### Optional: extra auth at the proxy

The API is fully authenticated by design, but you can put an extra
basicauth layer in front of the whole path for non-LAN clients. Caveat:
kosync clients cannot send HTTP basic-auth headers, so this only works
while KOReader devices sync from the LAN. If one ever needs WAN access,
exempt `/adapter/*` from the proxy auth; the adapter authenticates with
its own credentials either way.

## First run

Start the server and open `/ui/`. While the instance has no accounts at
all, it offers a one-time setup page instead of a sign-in form: pick a
name and a password and the account it makes is the first administrator.
The page closes for good the moment that account exists.

The same thing from a shell, when you would rather not open a browser
first:

```
liseur-sync admin -config liseur-sync.toml create-user alice
liseur-sync admin -config liseur-sync.toml grant-admin alice
liseur-sync admin -config liseur-sync.toml mint-token alice "Boox Palma"
liseur-sync admin -config liseur-sync.toml pairing-code alice
liseur-sync admin -config liseur-sync.toml koplugin-device alice kobo
```

`grant-admin` is how the shell path makes the first administrator.
`create-user` alone makes an ordinary account, and prints a reminder
when the instance has no administrator yet. After that, the admin panel
promotes and demotes accounts. The role lives on the account, so
granting it hands nobody a secret, and the last enabled administrator
cannot be demoted.

Now add the books:

```
liseur-sync admin -config liseur-sync.toml add-folder Shelf /srv/books
```

That is the import story. The path must already exist and be readable by
the server. The server detects the kind: a root with `metadata.db` is a
Calibre folder, anything else is a plain folder. The running server
reconciles the folder immediately and watches it without a restart.

## The Administration section

The Administration section of `/ui/settings` is where an administrator runs
the instance without a shell. It holds four things:

- **Overview**: version, build and uptime, how many accounts, folders,
  books and devices there are, and the effective configuration with the
  database URL left out on purpose.
- **Users**: the account list, and a page per account: reset a password,
  grant or revoke the administrator role, revoke a device credential or
  every credential at once, disable or enable the account, mint an API
  token with the scopes you choose, generate a kosync pairing code, add
  a statistics-plugin capability, and map the account's books to works.
  Creating an account or invite, and every action that hands out or takes
  away a way into an account, asks for your password again; every attempt
  is logged.
- **Folders**: every watched folder on the instance. Add a plain or
  Calibre folder by naming an existing path, or remove one the server
  should stop reflecting. Only this page shows `root_path`.
- **Maintenance**: whether scheduled jobs are running, whether folders
  have missing books, and the backup check.

The panel administers accounts and folders. It never shows what anybody
is reading, and no page there can open another user's private reading
state.

Disabling an account is the reversible half of deleting one. Every way
in stops at once (password, web session, API token, kosync device,
koplugin device, pairing code) and sessions are revoked, so a signed-in
user is out on their next click. Enabling it restores exactly what it
had; nothing is minted or revoked in between:

```
liseur-sync admin -config liseur-sync.toml disable-user alice
liseur-sync admin -config liseur-sync.toml enable-user alice
```

The last enabled administrator can be neither demoted nor disabled, from
the panel or the shell, so an instance cannot lock itself out.

## Watching folders

A folder is a database row: `id`, `name`, `root_path`, and `kind`. It has no
owner. An administrator explicitly grants folders to accounts; administrator
status alone does not add a folder to that administrator's library. Only an
admin sees or changes folder paths and grants.

Add one from Settings > Administration > Folders, or from a shell:

```
liseur-sync admin -config liseur-sync.toml add-folder Shelf /srv/books
liseur-sync admin -config liseur-sync.toml list-folders
liseur-sync admin -config liseur-sync.toml remove-folder <folder-id>
liseur-sync admin -config liseur-sync.toml assign-folder alice <folder-id>
liseur-sync admin -config liseur-sync.toml unassign-folder alice <folder-id>
liseur-sync admin -config liseur-sync.toml list-user-folders alice
liseur-sync admin -config liseur-sync.toml assign-all-folders alice
```

The per-account checkboxes under Settings > Administration > Users submit
the account's complete grant set, so the page lists every watched folder
rather than a page of them. Past 500 folders it stops offering the form —
a list it had to truncate would revoke whatever fell off the end — and the
`assign-folder` and `unassign-folder` commands above take over.

Removing a folder removes the catalog rows that came from it and stops
watching the root. Nothing below the root is touched. Adding it back
reads the same files again.

A folder root needs only read access unless you want uploads. The
server opens books and Calibre's `metadata.db` read-only, refuses
symlinks inside a watched tree, and never writes, renames or deletes
anything below the root, so mounting it read-only makes that rule
enforceable by the operating system rather than only by this program,
and costs nothing.

The exception is a folder you mark as accepting uploads
([ADR-0023](adr/0023-uploads-land-in-a-folder.md)). That one needs
write access, and it is opt-in twice over: an administrator turns it on
per folder, and a token needs the `library-upload` scope to use it.
Even then the server only ever *creates*: a file that was not there,
or a book Calibre did not have. It still never modifies, renames or
deletes one. The cover cache is the only directory it writes to.

The browser form can be narrowed with `content.folder_roots`, which
lists the directories an admin may choose from. Empty means anywhere the
server can read, which is what the CLI allows. A root is accepted when
it is one of those paths or below one:

```toml
[content]
cache_dir = "cache"
folder_roots = ["/srv/books", "/srv/calibre"]
```

The watcher runs a pass at startup, on debounced filesystem events, and
on a slow safety timer. inotify is an optimization, not the source of
truth. If the server cannot create an inotify watcher, or if one root
cannot be watched because a kernel limit was reached or the mount does
not support events, the server logs a warning and keeps reconciling on
the periodic pass. The catalog may lag; the server does not fail to
start and the folder does not become unusable.

A network mount is the case where this matters. NFS and SMB report
nothing to inotify, so such a folder is only ever read by the periodic
pass, which is up to half an hour behind. **Settings > Administration > Folders**
has a
*Scan now* for each folder that runs a pass immediately. It is safe to
press at any time and safe to press twice: a pass is idempotent, so
asking again is asking once.

`scan_max_files` and `scan_max_depth` bound one pass. They are guards
against pointing at too much, not tuning knobs. A pass that hits either
bound is incomplete: it may add or update what it saw, but it is not
allowed to mark anything missing because it did not see the whole tree.
Raise them only when the folder is that large.

### Plain folders

A plain folder is keyed by relative path. Size and modification time
decide whether metadata needs to be read again. A subdirectory of EPUBs
is a series named after that directory, and the files in it are the
volumes. Files at the root belong to no series.

If bytes change at a path, the next pass treats that as a new catalog
book rather than as identity transfer. Reading state belongs to a user's
work graph, not to a path that may now hold a different book.

### Calibre folders

A Calibre folder is a directory with `metadata.db` at its root. The
server opens that database read-only and treats Calibre's rows as the
catalog. Books are keyed by Calibre id, never by path, because Calibre
renames a book's directory when its title or author changes.

Calibre metadata is read on every pass. Series, tags, descriptions and
the chosen `cover.jpg` can change in `metadata.db` without touching the
EPUB, so a stat gate on the publication file would miss the change.
Nothing is ever written to `metadata.db`, to `metadata.opf`, or anywhere
else under the folder.

Two-way Calibre synchronization is future work. For now Calibre is where
you edit a Calibre collection, and this server reflects it.

#### Auditing a Calibre library

The catalog can only be as truthful as `metadata.db`, and that database
can disagree with the disk in either direction: a row whose file is gone
produces a warning on every pass, and a book directory Calibre has
forgotten is invisible here however plainly it sits on the disk.

`scripts/calibre-audit.sh` reports both, plus stale format rows and
books claiming a cover that is not there:

```
scripts/calibre-audit.sh /path/to/Calibre
```

It is read-only: the database is opened immutable, so it is safe to run
against a library Calibre has open. It exits non-zero when it finds
something, so it can be run from cron. It prints the `calibredb` command
that fixes each finding but never runs it: `metadata.db` belongs to
Calibre, and Calibre should be the only thing that writes to it.

Run it against the machine that owns the library, not necessarily the
one running this server. If the library reaches the server over a file
synchronizer, note that `metadata.db` is a single SQLite file replicated
as an opaque blob: when two machines edit it, one edit is discarded
silently, taking its books with it while their directories survive on
the disk. That is precisely the disagreement this script finds, and the
fix is to let exactly one machine write the library.

### Missing books

A book whose file is not observed by a complete pass is marked
`missing`. It stays in the catalog and the reader's work mapping stays
with it, because a disconnected disk is not a deleted book. It does
drop out of the listings: browse, search and the entity pages offer
only active books, because the download and cover routes answer `410`
for anything else and listing a book the server will refuse to serve is
advertising a dead end. The reading it already has is still reachable.
A work whose book is missing appears on the shelf as a text tile, the
same as a work this server never held a file for. The book returns to
the listing whole as soon as a complete pass sees its file again.

Two safety rules stop a transient mount problem from hiding a whole
shelf. A pass that did not fully succeed never marks anything missing;
one unreadable file, one parse failure, or one bound hit is enough to
make the pass incomplete. A pass that observed no books also marks
nothing missing, even if the root was readable, because an unmounted
mount point can look like an empty directory.

## Reading statistics for books nobody has opened yet

A book is joined to a reader's sync work the first time a client
resolves it. The admin panel and CLI can run the same backfill for an
account so books already visible in the catalog appear on that reader's
shelf with a work mapping:

```
liseur-sync admin -config liseur-sync.toml backfill-works alice
```

It is safe to re-run and reports what it did. A title-and-author-only
match still needs confirmation from the reader, because a wrong answer
would merge reading histories.

## Configuration

The content block is intentionally small:

```toml
[content]
cache_dir = "cache"
folder_roots = ["/srv/books"]
scan_max_files = 200000
scan_max_depth = 32

epub_max_entries = 10000
epub_max_directory_bytes = 67108864
epub_max_expanded_bytes = 2147483648
epub_max_entry_bytes = 536870912
epub_max_compression_ratio = 1000
epub_max_metadata_bytes = 4194304
epub_max_xml_depth = 128
```

Environment overrides follow the same names: `LISEUR_CACHE_DIR` for the
cover cache and `LISEUR_FOLDER_ROOTS` for the comma-separated allowed
roots. `LISEUR_LISTEN_ADDR`, `LISEUR_DATABASE_DRIVER`,
`LISEUR_DATABASE_URL`, `LISEUR_INSECURE_HTTP`, `LISEUR_CORS_ORIGINS`,
`LISEUR_TRUSTED_PROXIES`, and `LISEUR_READER_ORIGIN` keep their usual
meanings.

## Backup

Back up the database. It holds users, tokens, reading state, folder rows
and catalog metadata. For SQLite, use the SQLite backup command rather
than copying live `.db` and `.wal` files; for Postgres, use `pg_dump` or
your ordinary database backup.

Back up the folders themselves with whatever already protects your book
collection. They are not owned by this server, and restoring the server
database without restoring the same mounted folders leaves books
`missing` until the paths return.

The cache directory may be skipped. Every file in it is a rendered cover
that can be produced again from a book. If you do copy it, it does not
need to be consistent with the database; stale entries are just cache
misses by another name.

## Upgrades

A new image reaches a running container only through `docker compose up
-d`. `docker pull` alone is not enough and neither is `docker restart`:
a container is bound to the image id it was created from, so pulling
moves the `latest` tag while the old container keeps running, and
restarting re-runs that same old container. `up -d` is the command that
notices the id moved and replaces it. `compose.yaml` sets `pull_policy:
always` so a single `up -d` both re-resolves the tag and recreates.

To check what is actually running, ask the server rather than the
registry:

```
curl -s https://books.example.com/healthz
{"status":"ok","version":"v1.2.3","revision":"46ac8da1b2c3"}
```

That is the stamp from the binary itself, so it cannot disagree with the
code that is answering. It is also the quickest way to tell a deploy
that did nothing from one that did.

Migrations run at startup under a cross-process lock. If a migration
fails, the server refuses to start rather than run against a partially
migrated schema. Back up before upgrades.

Compaction and session rollups delete old rows, and SQLite reuses those
freed pages for future writes, so steady-state growth is bounded. The
database file does not automatically shrink below its high-water size;
use the documented `VACUUM INTO` backup procedure if physical shrinking
is ever needed.
