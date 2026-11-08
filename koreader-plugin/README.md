# Liseur Sync for KOReader

`liseur.koplugin` is a KOReader client for a Liseur Sync server. It
browses and downloads the catalogue, and it syncs reading positions
through the server's native API, so a book read on a Kindle and in
Liseur on a phone ends up at the same place.

It runs alongside KOReader's own OPDS browser and Progress sync plugin.
Only one plugin should write reading positions, though, and the handover
below covers that.

## What it does

- **Library**: folders, series (in reading order), authors, tags,
  search within a folder, book details, paged lists.
- **Downloads**: a book is streamed into a hidden temporary file in the
  download folder, checked against the catalogue's size and SHA-256, and
  only then renamed into place. A name that is taken gets ` (2)`, ` (3)`
  and so on. The check happens just before the rename; e-reader FAT
  filesystems have no rename that refuses to replace, so a file created
  in that instant by something else could still be replaced. Removing a download
  is a plain file deletion in KOReader and never touches the server.
- **Continue reading**: books on this device that the plugin has synced,
  newest first, with the other devices' place when it differs.
- **Position sync**: on page turns the position is saved locally and sent
  within about 30 seconds when the device is online, even while pages keep
  turning. Closed books with offline changes are reconciled and sent when
  connectivity returns; conflicting positions wait for your choice on open.
  Opening a book, waking
  the device and *Sync now* read the server first. When another device is
  somewhere else the plugin asks, *Go to …* or *Keep this position*; it
  never moves the page by itself.
- **Position history** for the open book, a way back to the furthest
  point reached, and a way to forget a furthest point that was a mistake.
- **Reading summary** from the server's statistics.

## Setup

1. Copy the `liseur.koplugin` directory (or unzip the release ZIP) into
   KOReader's `plugins/` directory and restart KOReader.
2. Create a device token on the server, with the permissions you want:
   `library-read` to browse and download, `sync` for positions,
   `read-insights` for the reading summary. A token with fewer
   permissions still works for the rest.
3. In KOReader: *Tools → Liseur Sync → Settings*. Set the server URL
   (`https://…`, including any path prefix), paste the token and pick a
   download folder. The first action that needs your account asks the
   server who the token is, so *Check connection* is only for seeing that
   identity and its permissions, or for trying again after a change.

HTTPS is required, except for `http://localhost`. The plugin verifies
the server's certificate and host name itself and refuses to connect
when the device has no CA bundle, rather than connecting unverified (see
[Compatibility](#compatibility)). If the device keeps its CA bundle
somewhere unusual, set *Certificate bundle* to that file.

Network requests and downloads run in cancellable subprocesses so the
interface stays responsive. Cancelling a position upload keeps its exact
request for retry, since the server may already have accepted it.

The token is stored in `settings/liseur.lua`, made readable only by
KOReader where the filesystem allows it. It never appears in the plugin's
status screens.

## Handing position sync over from Progress sync

1. In *Progress sync*, turn off *Automatically keep documents in sync*.
   Leave its server settings alone; they are what you fall back to.
2. Restart KOReader.
3. *Liseur Sync → Settings → Position sync*. The plugin refuses while
   Progress sync is still automatic, and once you confirm, it starts
   writing only after the next restart, so the two never write in the
   same session.

To go back, turn *Position sync* off and turn Progress sync's automatic
sync on again. Unsent positions stay on the device and are sent if you
switch the plugin back on later. Positions this plugin sends carry
KOReader's own location, which the server's kosync adapter hands back
verbatim, so a device still on Progress sync lands on the same paragraph.

## How positions are kept apart and safe

- State lives in `settings/liseur/<server>__<account>/`, one directory
  per server, path prefix and account. A different account never sees or
  sends another account's state.
- A book is identified by the file's SHA-256, never by its path, its
  title, or KOReader's partial MD5. State is kept per file, so two files
  that share a partial MD5 (KOReader's fingerprint only samples parts of
  a file) keep separate positions, revisions and uploads: reading one
  never clears what the other still owes. The partial MD5 only names a
  cache that leads to a file's own state; once it has been seen for two
  different files it is no longer used to match anything. A file the
  server matches to more than one work is shown as unclear and not
  synced.
- The SHA-256 is read from the whole file when a book is opened and again
  before each position upload, not taken from a cached size and mtime:
  a replacement that keeps both still holds different bytes, and its
  reading must not reach the old work. Opening a large book therefore
  costs one full read, the same one a download already pays to verify
  its bytes.
- A file whose bytes cannot be read is not synced at all: no state is
  written for it and the reader is told why. Reading the file again is
  what settles it, and an attempt that failed is never kept as if it had
  answered.
- Every upload is written to disk before it is sent and resent with the
  same bytes and id until the server answers, so a timeout cannot produce
  a second position. An upload prepared under a different device token is
  paused, not replayed; *This book → Resend paused upload* sends the
  position again as this device. A token the server refuses for lack of a
  permission pauses uploads, with their bytes kept, until *Check
  connection* succeeds again — a refusal is not retried, *Sync now* does
  not override the pause, and the check sends what was waiting.
- Position work outlives the reader that made it. A retry deadline, an
  upload frozen without one, and reading that was never uploaded are all
  found on disk by the plugin the file manager builds at startup, which
  then drains them; a device that is offline wakes nothing and waits for
  the connection. A book that can never be resolved is tried once, not
  once a second.
- The decision between this device and the server follows Liseur
  Android's three-way merge: whoever moved away from the last agreed
  position wins, both moving is a question for you, and neither the
  further position nor the newer clock wins on its own.
- When the server's change feed has been compacted past the plugin's
  cursor, the plugin rebuilds from the server's current heads without
  losing anything waiting on the device.

## Not in this version

- **Highlights and notes.** Syncing them needs a verified mapping
  between KOReader's xpointer ranges and the locators Liseur Android and
  the web reader use. Until that mapping is tested both ways, the plugin
  does not send or apply any.
- **Exact positions from Liseur Android or the web reader.** Those carry
  Readium locators, not KOReader locations, so the plugin offers them by
  percentage and says so. Positions from other KOReader devices on the
  same file are exact.
- **Statistics upload.** KOReader's statistics keep reaching the server
  through the existing statistics plugin and its capability URL. Moving
  that upload into this plugin needs the same device identity to avoid
  counting history twice, and is not done yet.
- **KOReader's HTTP proxy.** A proxied request would hand this device's
  token to the proxy, so the plugin refuses to send anything while one is
  configured and says so; there is no CONNECT tunnelling.
- Uploading books, deleting server books, personal series edits,
  translation caches and settings backup.

## Compatibility

Checked against KOReader v2026.07.2 source (commit `9192014d`,
koreader-base `6e4bc81a`) and run against the published v2026.07.1
Linux build; v2026.07.2 has no published Linux build.

It has also run on a Kindle Paperwhite 5 with KOReader v2026.07.2. That
run found a crash: opening a book's detail dialog died with "attempt to
call local '_'", because a book-detail line shadowed the gettext
function `_` with a loop variable. The shadow is fixed and a spec now
refuses every binding of `_` other than `local _ = require("gettext")`,
but **the fixed build has not been deployed to the Kindle again** — the
install was rolled back, so the device is not running this code.

KOReader v2026.07.2 bundles stock LuaSec 1.3.2, whose `https` defaults
to `verify = "none"` and checks no host name. Later koreader-base adds
both. The plugin does its own TLS setup on either, which the smoke test
checks against badssl.com's wrong-host, self-signed and expired
certificates.

## Server routes used

`GET /v1/token`; `POST /v1/works/resolve`, `POST /v1/books/{id}/resolve`;
`POST /v1/ops`, `GET /v1/changes`, `GET /v1/heads`,
`GET /v1/works/{id}/positions`, `POST /v1/works/{id}/furthest/forget`;
`GET /v1/folders`, `GET /v1/folders/{folder}/books`,
`GET /v1/folders/{folder}/search`, `GET /v1/entities/{kind}`,
`GET /v1/entities/{kind}/{entity}/books`, `GET /v1/books/{id}`,
`GET /v1/books/{id}/download`; `GET /v1/insights/summary`. Positions go
out with `progression`, KOReader's location in `foreign_pos` and the
file's SHA-256 in `edition_sha`, never a `locator`.

## Development

```
make koplugin-test                                  # pure-Lua specs, luajit only
KOREADER_DIR=/path/to/koreader make koplugin-integration
make koplugin-package                               # dist/liseur.koplugin.zip
```

`spec/run.lua` runs the `*_spec.lua` files with a small busted-compatible
runner and stand-ins for KOReader's `json`. The integration run builds the
server, seeds a throwaway SQLite instance with a generated EPUB and
drives the modules, then a real `ReaderUI`, through an unpacked KOReader
(`KOREADER_DIR` is the directory holding `reader.lua`, for example
`lib/koreader` of the `koreader-linux-x86_64` release).
`spec/koreader_smoke.lua` loads the plugin inside KOReader without a
server and, given an HTTPS URL, exercises certificate checks.

Module names carry a `liseur_` prefix because KOReader puts every
plugin directory on one shared `package.path`.

The plugin is MIT licensed like the server. It uses KOReader's runtime
APIs and copies no KOReader or BookOrbit code.
