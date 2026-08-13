# liseur-sync

A self-hostable reading-position sync and reading-statistics server,
companion to [Liseur](https://github.com/chmouel/liseur) and usable by
any reading app, including stock KOReader devices through a
kosync-compatible adapter.

Syncs reading positions across devices with history (not
last-write-wins), collects honest reading statistics, and recognises a
book across calibre re-encodes and library moves. One static Go binary.
SQLite by default, PostgreSQL optional. Multi-user.

It also holds the books. Upload an EPUB through the web UI or the API and
it is validated, stored once whatever its name, and served back to any
reader that speaks OPDS 1.2 — KOReader included — so one server supplies
the file and the reading position that goes with it. Or read it in the
browser: the reader unpacks the publication client-side, so no route ever
serves publisher markup, no script inside a book is allowed to run, and
it reports position like any other client. Operators who want a harder
line can serve the reader from a second hostname that holds no cookie.

A librarian can correct any book, browse by series, contributor, tag or
genre, search the lot, and — if the operator turns it on — ask
OpenLibrary or Google Books about a book and accept what comes back. A
correction is kept as the person's own: re-reading the file never undoes
it.

## Quick start

The easiest way is the installer — it detects Docker (or sets up
rootless Podman + a systemd user service), asks which database you
want, starts the server, and walks you through creating your first
user and device credentials:

```
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

(or clone the repo and run `scripts/install.sh`; pin a release with
`LISEUR_VERSION=vX.Y.Z`.)

### Manual

```
go build ./cmd/liseur-sync
./liseur-sync admin create-user alice        # password via prompt
./liseur-sync admin mint-token alice "Boox Palma"
./liseur-sync admin create-library alice "Books"
./liseur-sync serve
```

Then sign in at `/ui/books` to upload one, or point a reader at
`/opds/v1.2`. To index EPUBs you already have instead of uploading them,
`admin watch-library alice "Calibre" /srv/books` — the server reads that
directory and never writes to it.

Or with Docker Compose, three database postures:

```
docker compose --profile sqlite up -d        # single-file SQLite
docker compose --profile postgres up -d      # bundled Postgres
docker compose --profile external up -d      # your existing Postgres
```

Setup details (TLS, pairing KOReader, backups) are in
[docs/deployment.md](docs/deployment.md).

## Integration

[liseur](https://github.com/chmouel/liseur) is the reference client. If
you are writing another one, start with
[docs/integrating.md](docs/integrating.md), which explains the protocol's
reasoning, and keep [docs/openapi.yaml](docs/openapi.yaml) open for the
exact shapes.

## Security

See [SECURITY.md](SECURITY.md). Do not open a public issue.

## License

MIT
