# liseur-sync

A self-hostable reading-position sync and reading-statistics server,
designed as a companion to [Liseur](https://github.com/chmouel/liseur)
and usable by any reading app — including stock KOReader devices via a
kosync-compatible adapter.

**Status: design phase.** Nothing to run yet.

Read the [design document](docs/DESIGN.md). In short:

- Positions are an append-only, per-work operation log with delta sync
  in one round trip — history, not last-write-wins.
- Reading sessions are measured facts carrying progression fractions;
  pages and speed are derived, never fabricated.
- Book identity is layered (SHA-256 → KOReader partialMD5 →
  `dc:identifier` → title/author) so a book survives calibre re-encodes
  and library moves.
- Auth is argon2id accounts with revocable, scoped per-device tokens;
  every route is authenticated.
- Legacy protocols (kosync, the KOReader statistics plugin) are
  supported as edge adapters that write native records.
- One static Go binary, one SQLite file, multi-user.

## License

MIT
