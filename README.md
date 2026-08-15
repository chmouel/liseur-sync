# liseur-sync

A self-hostable server for the books you read and the place you got to
in them. It is the companion to [Liseur](https://github.com/chmouel/liseur),
and it talks to stock KOReader too, through a kosync-compatible adapter.

One static Go binary. SQLite by default, PostgreSQL if you would rather.
Multi-user.

![The library](docs/screenshots/library.png)

## What it does

**It remembers where you were.** Not with last-write-wins, which is how
you lose an afternoon's reading to a phone that woke up in your pocket,
but with a per-user log of every position any device reported. Nothing
overwrites anything; the history is there to look at, as sessions and
statistics that come from what your readers actually reported rather
than from a page number nobody can agree on. It also recognises a book
across a Calibre re-encode, a rename, or a move to another shelf, so
switching devices does not mean starting the chapter again.

**It holds the books.** Upload an EPUB through the web UI or the API and
it is checked, stored once whatever the file is called, and handed to any
reader that speaks OPDS 1.2 — KOReader included. One server supplies the
file and the position that goes with it. If you already have a directory
of EPUBs, or a Calibre library, point it there instead: the server reads
what is on disk and never writes to it.

**It lets you fix the metadata.** Browse by series, contributor, tag or
genre, search the lot, correct anything, and — if the operator turns it
on — ask OpenLibrary or Google Books and accept what comes back. A
correction is yours: re-reading the file never undoes it.

![A book](docs/screenshots/book.png)

## Read it in the browser

![The reader](docs/screenshots/reader.png)

The reader unpacks the publication in your browser, so no route ever
serves publisher markup and no script inside a book gets to run. It
reports its position like any other client, so you can close the laptop
and pick the chapter up on an e-ink device. Details, including the
optional second hostname for the paranoid, are in
[ADR-0007](docs/adr/0007-web-reader.md).

## Quick start

The installer detects Docker (or sets up rootless Podman with a systemd
user service), asks which database you want, starts the server and walks
you through your first account:

```
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

Clone the repo and run `scripts/install.sh` if piping the internet into
bash makes you uneasy; pin a release with `LISEUR_VERSION=vX.Y.Z`.

With Docker Compose, three database postures:

```
docker compose --profile sqlite up -d        # single-file SQLite
docker compose --profile postgres up -d      # bundled Postgres
docker compose --profile external up -d      # your existing Postgres
```

Or from source:

```
go build ./cmd/liseur-sync
./liseur-sync serve
```

Either way, open `/ui/`. A server with no accounts offers a setup page,
and the account you make there is the first administrator. Everything
after that — accounts, libraries, tokens, pairing a reader — is in the
panel, and in the `admin` subcommands for people who prefer a shell.

![The admin panel](docs/screenshots/admin.png)

Pairing KOReader, TLS, watched folders, Calibre and backups are covered
in [docs/deployment.md](docs/deployment.md).

## Writing a client

[liseur](https://github.com/chmouel/liseur) is the reference client. For
another one, start with [docs/integrating.md](docs/integrating.md) — it
explains why the protocol is shaped the way it is — and keep
[docs/openapi.yaml](docs/openapi.yaml) open for the exact shapes.

## Security

See [SECURITY.md](SECURITY.md). Please do not open a public issue.

## License

MIT. The screenshots show [Standard Ebooks](https://standardebooks.org)
editions, which are public domain, and are made by
`scripts/screenshots.sh`.
