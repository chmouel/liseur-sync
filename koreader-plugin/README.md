# Liseur Sync for KOReader

Connects KOReader to a Liseur Sync server. Browse your library, download books
onto the device, and keep your place in step with Liseur on a phone and the web
reader in a browser.

## What it does

- Shows your library: folders, series in reading order, authors, tags, search.
- Downloads books into a folder you choose, and reuses a copy you already have
  only while it still matches the one on the server.
- Keeps a continue reading list of what you have on this device, with where the
  other devices are.
- Saves your position as you turn pages, sends it when the device is online,
  and catches up on what you read offline.
- Asks before it moves you. If another device has read further, it offers that
  place and leaves the page alone unless you take it.
- Remembers the furthest point you reached, and lets you forget it when it was
  a mistake. The reading summary comes from the server.

## Setup

1. Install the plugin. It is in [KOReader's App
   Store](https://github.com/omer-faruq/appstore.koplugin) as *Liseur Sync*.
   Otherwise take `liseur.koplugin.zip` from the [latest plugin
   release](https://github.com/chmouel/liseur-sync/releases?q=plugin-) and unzip
   it into KOReader's `plugins/` folder, or copy this folder there. Restart
   KOReader.
2. On the server, create a device token and choose what it may do: browse and
   download, sync positions, or read your statistics. A token with fewer rights
   still handles the rest.
3. In KOReader: *Tools → Liseur Sync → Settings*. Type the server address,
   paste the token, and pick a folder for downloads. *Check connection* shows
   which account the token belongs to.

### A server on your own network

If the server runs on your home network, enter its plain address, for example
`http://192.168.1.20:8585` or `http://shelf.local:8585`. The server has to allow
this too: set `insecure_http = true` in its configuration.

Anyone on that network can read plain http traffic, including your token, so
keep https for a server you reach from outside. The plugin checks the
certificate itself and refuses to connect when it cannot.

## Coming from KOReader's Progress sync

Both write positions, so use one.

1. In *Progress sync*, turn off *Automatically keep documents in sync*, and
   leave its server settings as they are.
2. Restart KOReader.
3. In *Liseur Sync → Settings*, turn *Position sync* on.

To go back, turn *Position sync* off and switch Progress sync's automatic sync
on again. Places this plugin sent stay on the server, where Progress sync finds
them.

## How your reading is kept

Your reading stays on your account. Another account never sees it, and a book
is recognised by what is inside the file rather than its name, so renaming it
changes nothing.

A position is saved on the device before it is sent, and sent again until the
server confirms it, so a connection that drops halfway loses nothing and cannot
count twice. If two devices have both moved, the plugin asks which place to
keep.

## What it does not do yet

- Highlights and notes.
- The exact place from Liseur or the web reader, which arrives as a percentage.
  Another KOReader reading the same file lands exactly.
- Uploading reading statistics, which KOReader's own statistics plugin still
  does.
- Working through an HTTP proxy, since that would hand the proxy your token.
- Uploading or deleting books on the server, series edits, translations, and
  settings backup.

## Compatibility

Works with KOReader v2026.07 and later, and is used on a Kindle Paperwhite 5.

The plugin is MIT licensed like the server.
