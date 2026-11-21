<p align="center">
  <img src="internal/webui/static/icon.svg" alt="LiseurSync" width="84" height="84">
</p>

<h1 align="center">LiseurSync</h1>

<p align="center">
  A self-hosted library and reading-sync server for Liseur, KOReader, CrossPoint, and the web.
</p>

<p align="center">
  <a href="https://github.com/chmouel/liseur-sync/actions/workflows/test.yaml"><img src="https://img.shields.io/github/actions/workflow/status/chmouel/liseur-sync/test.yaml?branch=main&label=tests&style=flat-square" alt="Tests"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/chmouel/liseur-sync?style=flat-square" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/chmouel/liseur-sync?style=flat-square" alt="License"></a>
</p>

<p align="center">
  <a href="https://chmouel.github.io/liseur-sync/">Website</a> ·
  <a href="#install">Install</a> ·
  <a href="#features">Features</a> ·
  <a href="docs/deployment.md">Deployment guide</a>
</p>

<p align="center">
  <img src="docs/screenshots/library.png" alt="LiseurSync library" width="640">
</p>

LiseurSync keeps books, reading positions, and reading statistics together across devices. It runs as a single Go binary, with SQLite by default or PostgreSQL if preferred. It supports multiple users and leaves existing book folders unchanged unless uploads are enabled.

## Features

### Library

- Index EPUBs from ordinary folders or a Calibre library without moving the files.
- Search the library and the text of books.
- Browse and download books from other reading apps through OPDS 1.2 and 2.0.
- Upload and delete EPUBs and audiobooks in folders where uploads are enabled.
- Give each person access to their own selection of folders.
- Keep track of series and export the library and reading status as CSV.

### Reading

- Sync reading positions across devices, including precise paragraph locations. Older updates do not overwrite newer progress.
- Sync highlights, notes, bookmarks, and reading preferences with supported readers.
- Read EPUBs in the browser, or [save them for offline reading](https://chmouel.github.io/liseur-sync/ipad/) and sync when back online.
- Play audiobooks stored beside EPUBs. The page follows the narration, with optional sentence highlighting.
- Follow listening from [Audiobookshelf](docs/deployment.md#following-an-audiobook-on-audiobookshelf).
- Save vocabulary words and sync their reviews across devices.
- Track reading time, sessions, streaks, reading speed, and daily activity.
- Share cached sentence translations between your devices.
- Mirror reading positions to another LiseurSync, BookOrbit, or KOReader sync server.

### Connections

- [Liseur on Android](https://chmouel.github.io/liseur-sync/liseur/), [KOReader](https://chmouel.github.io/liseur-sync/koreader/), [CrossPoint](https://chmouel.github.io/liseur-sync/crosspoint/), and the built-in web reader.
- Device setup for KOReader and CrossPoint from the web interface.
- An optional [MCP connection for ChatGPT and other assistants](https://chmouel.github.io/liseur-sync/assistants/) to ask about books, characters, reading progress, highlights, and vocabulary. Access is read-only and controlled by the reader.
- A [REST API](docs/openapi.yaml) for other clients.

## Readers

| Reader | How it connects | Setup |
| --- | --- | --- |
| Liseur (Android) | Reading progress, annotations, statistics, and library access | [Guide](https://chmouel.github.io/liseur-sync/liseur/) |
| KOReader | Progress sync, OPDS catalog, and reading time through KoInsight | [Guide](https://chmouel.github.io/liseur-sync/koreader/) |
| CrossPoint (Xteink) | Reading progress, OPDS, and reading time | [Guide](https://chmouel.github.io/liseur-sync/crosspoint/) |
| Web reader | EPUB reading, offline books, progress, highlights, and notes | [iPad setup](https://chmouel.github.io/liseur-sync/ipad/) |
| Other OPDS apps | Browse and download books | [Client setup](docs/integrating.md) |

For KOReader, download the setup ZIP from Settings → Devices & integrations, copy the plugin folder to the reader, and start KOReader. The setup configures the catalog, position syncing, and KoInsight. The [KOReader guide](https://chmouel.github.io/liseur-sync/koreader/) covers the steps.

For CrossPoint, the Devices page provides a pairing code for positions, an OPDS token for books, and a reading-statistics plugin. See the [CrossPoint guide](https://chmouel.github.io/liseur-sync/crosspoint/).

## Install

The installer works with Docker and rootless Podman. It starts the server and helps create the first account.

```sh
curl -fsSL https://raw.githubusercontent.com/chmouel/liseur-sync/main/scripts/install.sh | bash
```

Or use Docker Compose:

```sh
git clone https://github.com/chmouel/liseur-sync.git
cd liseur-sync
docker compose --profile sqlite up -d
```

Use `--profile postgres` for bundled PostgreSQL or `--profile external` for an existing PostgreSQL server. See the [deployment guide](docs/deployment.md) for configuration, TLS, storage, and backups.

Once the server is running, open `/ui/` to create the first account and add a folder of books. Folders are read-only until you explicitly enable uploads.

## Build from source

```sh
go tool templ generate ./internal/webui/
go build ./cmd/liseur-sync
./liseur-sync serve
```

## Documentation

- [Website](https://chmouel.github.io/liseur-sync/) and [installation guide](docs/deployment.md)
- [Connecting AI assistants](https://chmouel.github.io/liseur-sync/assistants/)
- [Client integration and sync protocol](docs/integrating.md)
- [API reference](docs/openapi.yaml)
- [KOReader reference](docs/koreader.md) and [CrossPoint reference](docs/crosspoint.md)

## Security

Please report vulnerabilities as described in [SECURITY.md](SECURITY.md), not through public GitHub issues.

## License

[MIT](LICENSE). The screenshot uses public-domain editions from [Standard Ebooks](https://standardebooks.org) and is generated by `scripts/screenshots.sh`.
