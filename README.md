# liseur-sync

Self-hosted server for book libraries and reading-position synchronization.

`liseur-sync` is the companion server for [Liseur](https://github.com/chmouel/liseur). It also provides a kosync-compatible API for KOReader.

The server is a single Go binary with SQLite by default, optional PostgreSQL, and multi-user support.

![Dashboard](docs/screenshots/dashboard.png)

![Library](docs/screenshots/library.png)

## Interfaces

`liseur-sync` provides:

* reading-position synchronization using full locators
* a native REST API
* a kosync-compatible API for KOReader
* OPDS 1.2
* EPUB library indexing and search
* EPUB upload and deletion for writable folders
* a browser EPUB reader
* per-user reading statistics and sessions
* per-user series claims

API documentation is available in [docs/openapi.yaml](docs/openapi.yaml).

Client implementation notes and the synchronization protocol are documented in [docs/integrating.md](docs/integrating.md).

## Reading synchronization

Reading-position updates are stored as an append-only log rather than replacing the previous position.

This allows the server to resolve updates from multiple devices without an older client blindly replacing newer state. The same history is used to derive reading sessions and statistics.

Book identity is independent of its filesystem path. Clients can resolve books using content and metadata identifiers, including hashes, before exchanging reading state.

## Library

`liseur-sync` can index either:

* a directory containing EPUB files
* a Calibre library using `metadata.db`

Plain directories derive series information from their directory structure. Calibre libraries use `metadata.db` as the catalog source.

Catalogs are exposed through the web UI, REST API, and OPDS.

![Book details](docs/screenshots/book.png)

### Folder access

Folders are granted to accounts individually. Adding a folder does not automatically make it visible to every account.

From the CLI:

```sh
liseur-sync admin add-folder -assign alice Shelf /srv/books
```

Folders can also be configured under:

```text
Settings > Administration > Folders
```

Account folder grants are managed under:

```text
Settings > Administration > Users
```

### Writable folders

Folders are read-only by default.

Uploads and deletions are only allowed for folders explicitly configured to accept uploads:

```sh
liseur-sync admin folder-uploads <folder-id> on
```

For API clients:

* `library-upload` permits adding books to writable folders
* `library-delete` permits removing books from writable folders

These are separate token scopes.

Deleting through the web UI requires an administrator account.

A folder that does not accept uploads remains read-only: the server indexes its contents but does not add or remove files from it.

See:

* [ADR-0023](docs/adr/0023-uploads-land-in-a-folder.md)
* [ADR-0025](docs/adr/0025-deleting-a-book.md)

## Web reader

`liseur-sync` includes a browser-based EPUB reader.

![Reader](docs/screenshots/reader.png)

The reader uses the same synchronization API as other clients, so reading state is shared with Liseur, KOReader, and other compatible clients.

EPUB content is unpacked and rendered without exposing publisher files through normal application routes. Scripts embedded in EPUBs are not executed.

The reader design and security model are documented in [ADR-0007](docs/adr/0007-web-reader.md), including support for running the reader on a separate hostname.

## Installation

### Install script

The installer supports Docker and rootless Podman.

```sh
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

Alternatively:

```sh
git clone https://github.com/chmouel/liseur-sync.git
cd liseur-sync
scripts/install.sh
```

To install a specific version:

```sh
LISEUR_VERSION=vX.Y.Z scripts/install.sh
```

The installer configures the database, starts the server, and creates the initial account.

### Docker Compose

SQLite:

```sh
docker compose --profile sqlite up -d
```

Bundled PostgreSQL:

```sh
docker compose --profile postgres up -d
```

External PostgreSQL:

```sh
docker compose --profile external up -d
```

### Build from source

```sh
go build ./cmd/liseur-sync
./liseur-sync serve
```

## Initial setup

The web interface is available at:

```text
/ui/
```

When the database contains no accounts, the setup page creates the initial administrator account.

Users, folders, API tokens, folder grants, and reader pairing can be managed from the administration interface or with the `admin` CLI.

Deployment details, including TLS, KOReader pairing, Calibre integration, watched folders, and backups, are documented in [docs/deployment.md](docs/deployment.md).

## Client integration

[Liseur](https://github.com/chmouel/liseur) uses the native API.

Protocol and synchronization details:

[docs/integrating.md](docs/integrating.md)

OpenAPI specification:

[docs/openapi.yaml](docs/openapi.yaml)

## Security

Security issues should be reported according to [SECURITY.md](SECURITY.md).

Do not report security vulnerabilities through public GitHub issues.

## License

MIT.

The screenshots use public-domain editions from [Standard Ebooks](https://standardebooks.org) and are generated by `scripts/screenshots.sh`.
