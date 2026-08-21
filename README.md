# liseur-sync

A self-hosted server for book and reading-progress sync.

[Liseur](https://github.com/chmouel/liseur)'s companion server. It also
works with stock KOReader through a kosync-compatible API.

It is a single Go binary with SQLite by default, optional PostgreSQL,
and multi-user support.

![The dashboard](docs/screenshots/dashboard.png)

![The reader-first library](docs/screenshots/library.png)

## Why liseur-sync

Komga and calibre-web both cover part of the job.

Komga has full Readium locator sync and a clean REST API. Its
filesystem-first design means clients cannot upload or delete books; you
put books in library folders by other means. Its JVM footprint is also
large for a Raspberry Pi or small VPS (about 600 MB on disk).

calibre-web has a rich web UI and can delete books. Its Kobo-protocol
sync carries percentages rather than exact reading positions. Upload and
delete use browser forms behind sessions and CSRF tokens, which makes
them a poor fit for another client.

Neither server syncs a book opened outside its catalog, such as one from
a file manager. Neither offers personal series claims or an append-only
sync log for deterministic conflict resolution.

liseur-sync combines those missing pieces: full-locator sync, including
books resolved by hash; REST API upload and delete; personal series
claims; and an append-only sync log. It stays small enough for a
Raspberry Pi, at about 30 MB on disk.

| Capability         | Komga            | calibre-web       | liseur-sync      |
|--------------------|------------------|-------------------|------------------|
| Catalog and search | REST API         | OPDS              | REST API + OPDS  |
| File download      | REST API         | OPDS              | REST API         |
| Position sync      | Full locators    | Percentages only  | Full locators    |
| Book upload        | Not possible     | Web UI form only  | REST API         |
| Book delete        | Not possible     | Web UI form only  | REST API         |
| Series management  | Read-only        | None              | Personal claims  |
| Footprint          | ~600 MB (JVM)    | ~200 MB (Python)  | ~30 MB (Go)      |

## Features

### Reading progress sync

`liseur-sync` records each user's reading positions from every device.

It keeps every update, not only the latest one. A stale device cannot
overwrite newer progress, and the history drives reading sessions and
statistics.

It matches books by content rather than filename, so a rename, move, or
re-encoded EPUB can retain its reading position.

### Watched folders and OPDS

Point `liseur-sync` at a directory of EPUBs or a Calibre library. It
watches the folder, reads metadata, and serves books through the web UI,
native catalog API, and OPDS 1.2.

Folders are read-only until an administrator enables uploads. Books stay
where they are: the server does not modify, rename, or delete files below
the folder root. An upload-enabled folder accepts a new file or Calibre
entry from the library page or a client with the `library-upload` scope.
The cover cache is the only other location where the server writes files.

```
liseur-sync admin folder-uploads <folder-id> on
```

Browse by series, contributor, or tag, and search within the selected
folder. In a plain folder, subdirectories form the series structure. In
a Calibre library, `metadata.db` is the catalog.

![Book details and reading status](docs/screenshots/book.png)

## Web reader

![The reader](docs/screenshots/reader.png)

`liseur-sync` includes a browser-based EPUB reader.

The browser unpacks and renders EPUBs without exposing publisher content
through application routes. Scripts inside EPUBs do not run.

The reader syncs through the same API as other clients. Switch between
it, Liseur, KOReader, and other apps without losing your place.

See [ADR-0007](docs/adr/0007-web-reader.md) for the design and security
model, including the optional separate hostname for the reader.

## Quick start

The installer detects Docker or configures rootless Podman with a systemd
user service. It then asks you to choose a database, starts the server,
and creates the first account.

```sh
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

If you prefer not to pipe a remote script directly into a shell, clone
the repository and run:

```sh
scripts/install.sh
```

You can install a specific release with:

```sh
LISEUR_VERSION=vX.Y.Z scripts/install.sh
```

### Docker Compose

Pick a database profile:

```sh
docker compose --profile sqlite up -d        # SQLite
docker compose --profile postgres up -d      # bundled PostgreSQL
docker compose --profile external up -d      # existing PostgreSQL server
```

### Build from source

```sh
go build ./cmd/liseur-sync
./liseur-sync serve
```

Once the server is running, open `/ui/`.

When no accounts exist, the setup page opens automatically. The first
account becomes the administrator.

Add a folder from Settings → Administration → Folders, or from a shell:

```sh
liseur-sync admin add-folder Shelf /srv/books
```

Use Settings or the `admin` CLI to manage users, folders, API tokens, and
reader pairing.

![The administration section](docs/screenshots/admin.png)

See [docs/deployment.md](docs/deployment.md) for KOReader pairing, TLS,
watched folders, Calibre integration, and backups.

## Client integration

[Liseur](https://github.com/chmouel/liseur) is the reference client.

If you are building another client,
[docs/integrating.md](docs/integrating.md) covers the synchronization
model and protocol.

Full API spec: [docs/openapi.yaml](docs/openapi.yaml).

## Security

See [SECURITY.md](SECURITY.md) for reporting security issues.

Please do not report security vulnerabilities through public GitHub
issues.

## License

MIT.

The screenshots use public-domain editions from [Standard
Ebooks](https://standardebooks.org) and are generated by
`scripts/screenshots.sh`.
