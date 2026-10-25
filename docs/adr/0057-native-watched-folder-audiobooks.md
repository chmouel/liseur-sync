# ADR-0057: Watched-folder `.m4b` audiobooks and integrated Web Reader playback

- **Status:** Accepted
- **Date:** 2026-04-12
- **Depends on:** [ADR-0007](0007-web-reader.md),
  [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0022](0022-calibre-metadata-db-is-authoritative.md),
  [ADR-0055](0055-following-an-audiobook-on-audiobookshelf.md)

## Context

ADR-0055 added polling synchronization with an external Audiobookshelf
server. When a deployment already mounts its `.m4b` audiobooks inside a
watched folder (either alongside `.epub` editions in Calibre or in a
plain audiobook folder), polling a second server is not the only way to
listen and read: `liseur-sync` can index `.m4b` files directly from its
watched folders, align their chapters against the paired `.epub`
edition, stream them over HTTP `Range`, and play them inside the Web
Reader against the same append-only `ops` log and session store.

## Decision

**Leaf packages without import cycles.** ISO BMFF `.m4b` metadata,
chapter extraction (`moov/udta/chpl`, `moov/trak/udta/chpl`, QuickTime
`text`/`sbtl` chapter tracks, and companion `.cue` files), and bounded
audio snippet slicing live in `internal/m4b`. Chapter-to-EPUB alignment,
text-offset locator conversion, and Whisper STT transcript matching live
in `internal/audiomap`. Both `internal/content` and
`internal/audiobookshelf` import those packages without coupling.

**Windowed `stsz` snippet slicing.** Long audiobooks (30+ hours) carry
`moov`/`stsz` tables exceeding 19 MiB. `m4b.SliceSnippet` walks box
headers by range/seek and reads only the `stsz` sample-size window for
the enclosing audio chunk (`[chunkStartSample..s1)`, bounded to 256 KiB
and 65,536 samples) plus the target audio sample range (`<= 2 MiB`).

**Catalog reconciliation (`catalog_audiobooks` and `book_audiobooks`).**
Watched folders discover `.m4b` files (and companion `.cue` files)
through `openUnderRoot` (`os.OpenRoot`, `O_NOFOLLOW`, regular-file
verification). Plain folders key audiobooks by `(folder_id,
relative_path)` and track companion `.cue` `mtime` and size so `.cue`
edits invalidate cached chapters without re-hashing the `.m4b`. Calibre
folders discover `M4B` formats authoritatively from `metadata.db`'s
`data` table keyed by `(folder_id, calibre_id)` and follow ADR-0022 for
missing (`Unservable`) and removed rows. `ReconcileFolderAudiobooks`
enforces the `complete && len(observed) > 0` guard independently of EPUB
observations.

**Conservative auto-linking and dual-folder grant checks.** Active
audiobooks with chapters are paired 1:1 with active books by:
1. Same `folder_id` and `calibre_id`,
2. Same dedicated subdirectory (`dir != "."`) containing exactly one
   active `.epub` and one active `.m4b`,
3. Unique normalized `ISBN` or `ASIN` across the catalog, or
4. Unique normalized `(title, author)` across the catalog.

Every read, ticket, stream, and locate operation verifies `user_folders`
grants on both `book.FolderID` and `audiobook.FolderID`.

**Authenticated range streaming.** `POST /v1/books/{id}/audio/ticket`
mints a 6-hour HMAC-SHA256 path ticket bound to the caller's active
`Token.ID` or `AuthSession.ID`, user ID, book ID, audiobook ID, and
`content_sha256`. `GET /v1/books/{id}/audio/stream/{ticket}` (and
cookie-authenticated `GET /ui/books/{id}/audio`) re-verifies the active
credential, both folder grants, and the active `book_audiobooks` link
before serving bytes through `http.ServeContent` with a strong
`ETag: "<audiobook_sha>"`.

**Stateless `[t - 10s, t]` STT refinement.** When `POST
/v1/books/{id}/audio/locate` is called with `refine_stt: true`, the
server slices `[max(seg.AudioStart, t - 10s), t]` from the local `.m4b`
so the end of the spoken snippet aligns with `t`, transcribes it via the
configured Whisper STT endpoint under a 2-request concurrency semaphore
and per-user rate limit, and requires `confidence >= 0.6` and a
non-overlapping runner-up margin `>= 0.15` within `seg.Href`.

**Listening session accounting (`OriginAudiobook`).** `POST /v1/sessions`
accepts `"mode": "reading" | "listening"`, storing `"listening"`
sessions with `OriginAudiobook`. `MeasuresReadingPace(OriginAudiobook)`
is `false`, so listening time counts toward total time, daily activity,
and streaks without skewing visual reading pace.

**Web Reader player.** The player opens in mini mode and expands without
blocking interaction with the book. Above 700px it can be dragged by its
handle, moved with arrow keys, or docked to an edge. Narrower viewports use
a bottom dock that reserves space for the mini controls and keeps the
reading footer visible. Size and desktop placement are browser preferences;
opening the reader never starts playback. Moving or minimizing the player
does not change the stream, page-follow mode, or listening session. Closing
it stops playback and returns to reading accounting.
