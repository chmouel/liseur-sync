# Liseur Sync for KOReader: developer notes

How `liseur.koplugin` works inside, and what to check before changing it.
[README.md](README.md) is for people installing and using the plugin; this
file is for people editing it.

## Where state lives

`settings/liseur/<server>__<account>/`, one directory per server, path
prefix and account. A different account never sees or sends another
account's state.

The server address and the device token live one level up, in
`settings/liseur.lua`, which is made readable only by KOReader where the
filesystem allows it. A token typed in by hand is trimmed and refused if it
holds whitespace; one imported from a file is the first non-empty line that
is not a comment, refused for the same reasons.

A book is identified by the file's SHA-256, never by its path, its title,
or KOReader's partial MD5. State is kept per file, so two files that share
a partial MD5 (KOReader's fingerprint only samples parts of a file) keep
separate positions, revisions and uploads: reading one never clears what
the other still owes. The partial MD5 only names a cache that leads to a
file's own state, and once it has been seen for two different files it is
no longer used to match anything. A file the server matches to more than
one work is shown as unclear and not synced.

The SHA-256 is read from the whole file when a book is opened and again
before each position upload, not taken from a cached size and mtime: a
replacement that keeps both still holds different bytes, and its reading
must not reach the old work. Opening a large book therefore costs one full
read, the same one a download already pays to verify its bytes.

A file whose bytes cannot be read is not synced at all: no state is
written for it and the reader is told why. Reading the file again is what
settles it, and an attempt that failed is never kept as if it had
answered.

## How a position gets there

Every upload is written to disk before it is sent and resent with the same
bytes and id until the server answers, so a timeout cannot produce a
second position. An upload prepared under a different device token is
paused, not replayed; *This book → Resend paused upload* sends the
position again as this device. A token the server refuses for lack of a
permission pauses uploads, with their bytes kept, until *Check connection*
succeeds again. A refusal is not retried, *Sync now* does not override the
pause, and the check sends what was waiting.

Position work outlives the reader that made it. A retry deadline, an
upload frozen without one, and reading that was never uploaded are all
found on disk by the plugin the file manager builds at startup, which then
drains them. A device that is offline wakes nothing and waits for the
connection. A book that can never be resolved is tried once, not once a
second.

The decision between this device and the server follows Liseur Android's
three-way merge: whoever moved away from the last agreed position wins,
both moving is a question for the user, and neither the further position
nor the newer clock wins on its own.

When the server's change feed has been compacted past the plugin's cursor,
the plugin rebuilds from the server's current heads without losing
anything waiting on the device.

## Plain http is decided by name shape

`parseBase` in `liseur_api.lua` allows `http://` only for a host that
cannot be a public name: loopback, a dotted quad in a private or
link-local range, an IPv6 link-local or unique-local literal, a single
label, or a `.local`, `.lan`, `.home.arpa` name.

The rule looks at the host as written and never resolves it. A lookup
would let any public name answer with `192.168.1.x` and be treated as
local. The range checks also run on a strict dotted quad rather than a
prefix, because `10.example.org` and `172.16.example.org` are valid public
names that begin with digits a private range also uses.

The server side of the same trade is `insecure_http = true`; without it
the server refuses credential traffic over plain http whatever the plugin
allows.

## Server routes used

`GET /v1/token`; `POST /v1/works/resolve`, `POST /v1/books/{id}/resolve`;
`POST /v1/ops`, `GET /v1/changes`, `GET /v1/heads`,
`GET /v1/works/{id}/positions`, `POST /v1/works/{id}/furthest/forget`;
`GET /v1/folders`, `GET /v1/folders/{folder}/books`,
`GET /v1/folders/{folder}/search`, `GET /v1/entities/{kind}`,
`GET /v1/entities/{kind}/{entity}/books`, `GET /v1/books/{id}`,
`GET /v1/books/{id}/download`; `GET /v1/insights/summary`.

Positions go out with `progression`, KOReader's location in `foreign_pos`
and the file's SHA-256 in `edition_sha`, never a `locator`. A change to any
of these routes, or to those fields, is a change to this plugin.

## Tests

```
make koplugin-test                                  # pure-Lua specs, luajit only
KOREADER_DIR=/path/to/koreader make koplugin-integration
make koplugin-package                               # dist/liseur.koplugin.zip
```

`spec/run.lua` runs the `*_spec.lua` files with a small busted-compatible
runner and stand-ins for KOReader's `json`. The integration run builds the
server, seeds a throwaway SQLite instance with a generated EPUB and drives
the modules, then a real `ReaderUI`, through an unpacked KOReader
(`KOREADER_DIR` is the directory holding `reader.lua`, for example
`lib/koreader` of the `koreader-linux-x86_64` release). `spec/koreader_smoke.lua`
loads the plugin inside KOReader without a server and, given an HTTPS URL,
exercises certificate checks against badssl.com's wrong-host,
self-signed and expired certificates.

Module names carry a `liseur_` prefix because KOReader puts every plugin
directory on one shared `package.path`.

## Releasing

Every push to `main` that touches the shipped plugin (`liseur.koplugin/*.lua`,
`package.sh`, the licence) runs the specs, packages the ZIP with a version
stamped into `_meta.lua`, and publishes the tag `plugin-0.<commits touching the
plugin>` with that ZIP attached. A docs or spec edit publishes nothing: its ZIP
would be the previous one with a new version stamp. The version is never bumped
by hand.

Three details come from KOReader's App Store rather than from preference.
It finds a repository by the `koreader-plugin` topic or a name holding
`.koplugin`. It parses a version out of a tag by stripping a leading `v`,
`release`, `version` or `plugin` and then reading digits, which is why the
tag is `plugin-0.12` and not `koplugin-v0.12`. Its install dialog reads
`/releases/latest` only, so a plugin release has to be the repository's
latest one. The consequence: while the plugin lives in this repository, a
server release must not claim that pointer, or the plugin needs its own
repository.

The workflow is idempotent. A rerun that finds an identical ZIP does
nothing, a release left without its asset gets the ZIP uploaded, and a
different ZIP under the same tag fails instead of replacing published
code. A change to the workflow file itself publishes nothing, since it is
not in the trigger paths; start it from Actions when you need to try one.

## Compatibility notes

Checked against KOReader v2026.07.2 source (commit `9192014d`,
koreader-base `6e4bc81a`) and run against the published v2026.07.1 Linux
build; v2026.07.2 has no published Linux build.

It has also run on a Kindle Paperwhite 5 with KOReader v2026.07.2. That
run found a crash: opening a book's detail dialog died with "attempt to
call local '_'", because a book-detail line shadowed the gettext function
`_` with a loop variable. The shadow is fixed and a spec now refuses every
binding of `_` other than `local _ = require("gettext")`, but the fixed
build has not been deployed to the Kindle again: the install was rolled
back, so the device is not running this code.

KOReader v2026.07.2 bundles stock LuaSec 1.3.2, whose `https` defaults to
`verify = "none"` and checks no host name. Later koreader-base adds both.
The plugin does its own TLS setup on either, which the smoke test checks.

## Licence

The plugin is MIT licensed like the server.
