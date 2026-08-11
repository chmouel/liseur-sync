# liseur-sync

A self-hostable reading-position sync and reading-statistics server,
companion to [Liseur](https://github.com/chmouel/liseur) and usable by
any reading app, including stock KOReader devices through a
kosync-compatible adapter.

Syncs reading positions across devices with history (not
last-write-wins), collects honest reading statistics, and recognises a
book across calibre re-encodes and library moves. One static Go binary.
SQLite by default, PostgreSQL optional. Multi-user.

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
./liseur-sync serve
```

Or with Docker Compose, three database postures:

```
docker compose --profile sqlite up -d        # single-file SQLite
docker compose --profile postgres up -d      # bundled Postgres
docker compose --profile external up -d      # your existing Postgres
```

Setup details (TLS, pairing KOReader, backups) are in
[docs/deployment.md](docs/deployment.md). Client authors: see
[docs/integrating.md](docs/integrating.md) and the OpenAPI spec in
[docs/openapi.yaml](docs/openapi.yaml).

## Security

See [SECURITY.md](SECURITY.md). Do not open a public issue.

## License

MIT
