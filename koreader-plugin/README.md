# Liseur Sync for KOReader

`liseur.koplugin` connects KOReader to a Liseur Sync server. It browses your
shelf, downloads books onto the device, and keeps your reading position in step
with Liseur on a phone and with the web reader in a browser.

It runs beside KOReader's own OPDS browser and Progress sync plugin. Two
plugins writing positions in the same book will disagree, so use one of
them; the handover below takes a minute.

## What it does

The library lists your folders, series in reading order, authors and tags, and
searches inside a folder. Books download into a folder you pick. A copy already
the device holds is reused only while its bytes still match the server's copy, so
a file you replaced by hand is fetched again.

Continue reading lists the books this device has synced, newest first, and says
where another device is when that differs.

Positions save on every page turn and reach the server within about half a
minute, while you keep reading. Changes made offline go out when the device is
online again. Opening a book, waking the device, or asking for a sync reads the
server first; when another device has moved elsewhere the plugin asks whether to
go there or stay, and it never turns the page by itself.

Each book also has its position history, a way back to the furthest point
reached, and a way to forget that point when it was a mistake. The reading
summary comes from the server's statistics.

## Setup

1. Install the plugin. It is in [KOReader's App
   Store](https://github.com/omer-faruq/appstore.koplugin) as *Liseur Sync*.
   Otherwise take `liseur.koplugin.zip` from the [latest plugin
   release](https://github.com/chmouel/liseur-sync/releases?q=plugin-) and unzip
   it into KOReader's `plugins/` directory, or copy this directory there.
   Restart KOReader.
2. Create a device token on the server, with the permissions you want:
   `library-read` to browse and download, `sync` for positions, `read-insights`
   for the reading summary. A token with fewer permissions still works for
   everything else.
3. In KOReader: *Tools → Liseur Sync → Settings*. Set the server URL (including
   any path prefix), paste the token and pick a download folder. The first
   action that needs your account asks the server who the token is, so *Check
   connection* is only for seeing that identity, or for trying again after a
   change.

The plugin does its own certificate and host name checks. A device without a CA
bundle is refused rather than connected unverified; if the bundle lives
somewhere unusual, point *Certificate bundle* at the file.

### Plain http on your own network

https is the default. When the server sits on the same network, `http://` is
accepted for an address or name that cannot belong to anyone else: `10.0.0.5`,
`192.168.1.20`, `172.16.x`, link-local and ULA IPv6 addresses, `.local`, `.lan`
and `.home.arpa` names, and single words like `http://shelf:8585` that your
router resolves.

The server has to agree. Set `insecure_http = true` in its configuration, which
exists for exactly this case.

Plain http means anyone on that network can read the traffic, including the
token and your positions. That is a fair trade on a home LAN and a bad one on a
network you do not control, and it is the reason a name that could be a public
site still needs https.

## Moving over from Progress sync

1. In *Progress sync*, turn off *Automatically keep documents in sync*. Leave
   its server settings alone; they are what you fall back to.
2. Restart KOReader.
3. *Liseur Sync → Settings → Position sync*. The plugin refuses while Progress
   sync is still automatic, and once you confirm it starts writing after the
   next restart, so the two never write in the same session.

To go back, turn *Position sync* off and turn Progress sync's automatic sync on
again. Positions this plugin sent are KOReader's own locations, kept by the
server, so a device still on Progress sync lands on the same paragraph.

## How your reading is kept

State is stored per server and per account, so one account never sees or sends
another's reading.

A book is identified by the contents of the file, not its name or its folder,
and a file whose contents cannot be read is not synced at all: the plugin says
so instead of guessing.

A position is written to disk before it is sent and retried with the same
request until the server answers, so a connection that drops halfway cannot
produce two positions. An upload paused over a permission or a token stays
paused, with its bytes kept, until *Check connection* succeeds.

When two devices have both moved, the plugin asks. It does not pick the newer
clock or the further page on your behalf.

## Not in this version

- Highlights and notes, which need a verified mapping between KOReader's
  positions and the ones Liseur and the web reader use.
- Exact positions from Liseur or the web reader. Those arrive as a percentage
  and are offered as one. Another KOReader device reading the same file is
  exact.
- Statistics upload. KOReader's statistics plugin already reaches the server.
- KOReader's HTTP proxy. A proxied request would hand the token to the proxy,
  so the plugin refuses to send anything while one is configured.
- Uploading or deleting books on the server, series edits, translations, and
  settings backup.

## Compatibility

Tested against KOReader v2026.07.2 and its published Linux build for v2026.07.1,
and run on a Kindle Paperwhite 5.

KOReader v2026.07.2 bundles a LuaSec that verifies nothing by default; later
builds verify certificates and host names. The plugin sets up TLS itself on
either, which the test suite checks against deliberately broken certificates.

## Development

[DEVELOPER.md](DEVELOPER.md) has the internals: where state lives, how a
position reaches the server, the routes it calls, the tests, and what a
release involves. The short version:

```
make koplugin-test                                  # pure-Lua specs, luajit only
KOREADER_DIR=/path/to/koreader make koplugin-integration
make koplugin-package                               # dist/liseur.koplugin.zip
```

Every push to `main` that touches the plugin's own Lua, its packaging or the
licence publishes a release with the ZIP attached. Docs and specs publish
nothing, and nobody bumps a version by hand.

The plugin is MIT licensed like the server. It copies no KOReader or BookOrbit
code.
