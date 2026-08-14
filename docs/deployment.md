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
Setting `LISEUR_CONFIG` instead is equivalent when `-config` is
omitted, which is more convenient for compose/systemd units that only
want to inject an environment variable.
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

Start the server and open `/ui/`. While the instance has no accounts at
all, it offers a one-time setup page instead of a sign-in form: pick a
name and a password and the account it makes is the first
administrator. The page closes for good the moment that account exists,
and everything after it happens in the admin panel at `/ui/admin`.

The same thing from a shell, when you would rather not open a browser
first:

```
liseur-sync admin -config liseur-sync.toml create-user alice
liseur-sync admin -config liseur-sync.toml grant-admin alice        # first administrator
liseur-sync admin -config liseur-sync.toml mint-token alice "Boox Palma"
liseur-sync admin -config liseur-sync.toml pairing-code alice      # for KOReader kosync
liseur-sync admin -config liseur-sync.toml koplugin-device alice kobo  # stats plugin
```

`grant-admin` is how the shell path makes the *first* administrator —
`create-user` alone makes an ordinary account, and prints a reminder
when the instance has no administrator yet. After that, the admin panel
promotes and demotes accounts. The role lives on the account, so
granting it hands nobody a secret, and the last enabled administrator
cannot be demoted.

## The admin panel

`/ui/admin` is where an administrator runs the instance without a
shell. It holds four things:

- **Overview** — version, build and uptime, how many accounts,
  libraries, books and devices there are, and the effective
  configuration with the database URL left out on purpose.
- **Users** — the account list, and a page per account: rename nothing,
  but reset a password, grant or revoke the administrator role, revoke a
  device credential, disable or enable the account, and see which
  libraries it can reach. Every one of those asks for *your* password
  again, and every attempt is logged.
- **Libraries** — every library on the instance with its owner, who else
  may read or write it, and its filing layout. Create a library and hand
  it to somebody in one step.
- **Maintenance** — what the background jobs are doing: ingest queue by
  state with the age of the oldest item, books waiting for review, trash
  and when it next expires, blob count and orphans. Read-only; there is
  no button that runs a job early.

The panel administers accounts, not their contents: it never shows what
anybody is reading, and no page there can open a book.

Disabling an account is the reversible half of deleting one. Every way
in stops at once — password, web session, API token, kosync device,
koplugin device, pairing code — and sessions are revoked, so a signed-in
user is out on their next click. Enabling it restores exactly what it
had; nothing is minted or revoked in between:

```
liseur-sync admin -config liseur-sync.toml disable-user alice
liseur-sync admin -config liseur-sync.toml enable-user alice
```

The last enabled administrator can be neither demoted nor disabled, from
the panel or the shell, so an instance cannot lock itself out.

## Watching a folder you already have

A directory library indexes an existing directory of EPUBs without ever
writing to it:

```
liseur-sync admin -config liseur-sync.toml add-library alice "Shelf" /srv/books
```

A library has three independent properties: a **source** (`managed` for
uploads, `directory` for a tree of EPUBs), a **storage** mode (`cas`, so
the bytes are copied, or `in-place`, so they are read where they lie) and
a **refresh** policy (`manual`, or `interval` every `-interval`).
`add-library` defaults to a directory source, copied, refreshed on an
interval — which is what a watch folder was.

Mount that directory read-only if you can. The server does not need write
access and treating the mount as the enforcement point means a bug cannot
become a data loss. Point it at the directory itself, not at a parent that
happens to contain it: the sweep walks everything below the root, and
`watched_max_files` and `watched_max_depth` exist to stop a mistake there
from becoming a very long sweep.

Each library is swept on its own `-interval`, and the server looks for
one that has come due every `refresh_tick_seconds` (60 by default; 0
turns refreshing off entirely). A library set to `manual` is only swept
when somebody asks, with **Refresh now** on the admin panel's Libraries
page or:

```
liseur-sync admin -config liseur-sync.toml refresh-library alice <library-id>
```

Both queue the work for the running server rather than doing it
themselves, so neither holds a terminal or a browser open for the length
of a sweep, and neither can start a second sweep of a root that is
already being read.

What each sweep found — when it last succeeded, and what went wrong if it
did not — is on that library's card, and the count of libraries that are
overdue or failing is on the Maintenance page.

The EPUBs a sweep finds are ingested through the same pipeline uploads
use. For a `cas` library the bytes are copied, so books are served from a
validated copy in `content.root` and editing a file under the root does
not change what readers are being served; the library costs disk like a
managed one. An `in-place` library copies nothing and costs no quota, and
its books are read from the source file at download time.

What the sweep concludes about files that are *not* there is deliberately
cautious:

- A file that disappears is only marked missing by a sweep that finished.
  A sweep that hit `watched_max_files` or `watched_max_depth` saw part of
  a root and is not allowed to speak for the rest of it.
- A root that cannot be opened at all changes nothing. An unmounted volume
  and a deleted library look identical from here, and only one of them is
  a reason to empty a catalog.
- A path whose contents changed does not silently become a new edition of
  the same book. The book is flagged for review and keeps the copy it was
  promoted with, because a file appearing at a path proves nothing about
  whether it is the same work — replacing `Author/Title.epub` with an
  unrelated book must not inherit the first one's reading history.
- Renaming a file is an unrecognized path plus a missing one, for the same
  reason. Identity is not transferred on a matching hash alone.

Symlinks under the root are skipped rather than followed, and the sweep
walks by file descriptor rather than by path, so renaming a directory
mid-sweep cannot redirect it outside the root.

### Books waiting on your decision

A changed path leaves a book flagged rather than reingested, so somebody
has to be asked:

```
liseur-sync admin -config liseur-sync.toml list-review alice <library-id>
liseur-sync admin -config liseur-sync.toml clear-review alice <library-id> <book-id>
```

`clear-review` says only that you are content with the copy being served;
the book leaves review and the next availability pass puts it back in the
catalog if it still has a servable file. If the new file really is a
different book, delete the flagged one and let the next sweep ingest the
path as what it now holds.



A file that arrives with a path — one found under a watched root rather
than uploaded — can say something about its own author, series and title,
but only if the server knows how the library is laid out. Two common
layouts are the same shape on disk: `Author/Title.epub` and
`Series/Author - Title.epub` are both one directory and one file, and only
you know which one your library uses.

```
liseur-sync admin -config liseur-sync.toml library-layout alice <library-id>
liseur-sync admin -config liseur-sync.toml library-layout alice <library-id> series/author-title
liseur-sync admin -config liseur-sync.toml library-layout alice <library-id> none
liseur-sync admin -config liseur-sync.toml library-layout alice <library-id> default
```

With no layout argument it prints what the library uses now and what it
could use. The layouts are tried in the order you list them, which is how
you resolve two that claim the same shape. `none` turns filename parsing
off for that library, and `default` restores the conservative built-in
list — `author/title`, `author/series/title` and `author-series-title`,
which leaves out `series/author-title` precisely because it would
otherwise reinterpret every `Author/Title.epub` library as a series
library.

Changing this affects files ingested afterwards. Books already in the
catalog keep the metadata they were promoted with; a wrong layout is
corrected by editing those books, not by re-reading their names.

A library whose configuration cannot be parsed stops being promoted
rather than being read with the wrong layout: the ingest pass counts it as
`misconfigured` and moves on to the other libraries. Uploads are
unaffected either way — an upload carries no path, so there is nothing for
a layout to read.

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
   anybody with a shell can read.

   `covers/` inside it may be skipped, and may be deleted at any time to
   reclaim space: every file in it is a rendered copy of a cover that
   lives inside a book, and the next request for one rebuilds it. Nothing
   else in the content directory is regenerable;
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
