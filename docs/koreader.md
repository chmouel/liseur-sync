# Use KOReader with liseur-sync

KOReader can browse and download your library over OPDS, save your reading
position with its built-in *Progress sync*, and send reading time to the
server through the KoInsight plugin. OPDS and Progress sync are built into
KOReader, so only KoInsight is a plugin, and the setup downloads it for you.
One download from the Devices page sets up all three on a reader. A
[step-by-step page with screenshots](https://chmouel.github.io/liseur-sync/koreader/)
covers the same ground for a less technical reader.

## What you need

- An account on your liseur-sync server.
- A reader running KOReader.
- Wi-Fi on the reader for its first start after the setup.

On **Settings → Profile & security**, both **kosync** and **statistics
plugin** must be on for your account. The **Set up KOReader** box on the
Devices page tells you when one of them is off.

## Set up a reader

1. Sign into liseur-sync in a browser and open **Settings**, then **Devices &
   integrations**. The **E-readers** tab opens first.
2. In **Set up KOReader**, type the reader's name, such as `Kobo Libra`, and
   press **Download KOReader setup**. Save the ZIP somewhere private.
3. Unzip it. Quit KOReader completely and copy
   `patches/1-liseur-setup.lua` into the `patches/` folder in KOReader's
   directory, creating the folder if needed. On a Kobo that is
   `.adds/koreader/patches/`, and the hidden `.adds` folder may need "show
   hidden files" turned on.
4. Turn on Wi-Fi and start KOReader. The patch runs once and configures
   everything.
5. Delete the ZIP from your computer. Delete `1-liseur-setup.lua` from the
   reader once `plugins/koinsight.koplugin` exists there.

The patch keeps unrelated settings. It replaces any Progress sync account
already on the reader, adds a *Liseur Sync* entry to the OPDS catalogs
(replacing an older one for the same server), turns on automatic Progress
sync and sets KoInsight to sync on suspend. When the OPDS Plus plugin is
installed it also adds the OPDS 2.0 catalog to that plugin's own list.

KoInsight comes from its archived repository at one fixed commit, and each
file is checked against a known checksum before it is installed. A file that
does not match is refused. If the reader was offline, the patch tries again
at the next start, so keep it until the plugin is in place.

### Check it

1. In the file browser's menu, open **OPDS catalog** and then *Liseur Sync*.
   Your library should list.
2. Download a book, read a few pages and close it. Progress sync sends the
   position to the server.
3. Open **Tools → KoInsight → Synchronize data**. Reading time then appears
   on the Insights page.

## What the download contains

- `patches/1-liseur-setup.lua`, the file to copy across.
- `credentials.txt`, every value the patch writes, for a manual setup.
- `README.txt`, the short version of these steps.

It creates three logins named after the reader: an API token with the
`library-read` scope (the OPDS password), a Progress sync device with a
generated username and password, and a KoInsight upload URL that only the
statistics plugin uses. None of them is your account password. Anyone who has
the ZIP can read your library and send reading data as you, so download a
separate setup for each reader.

To remove a reader, revoke all three entries with its name on the Devices
page: the API token on the **Apps** tab, the Progress sync device and the
statistics device on the **E-readers** tab. Revoking one leaves the other two
working.

## Manual setup

Use this when patches are not available, for example KOReader from F-Droid on
Android. Download the setup as above and enter the values from
`credentials.txt` on the reader.

**Progress sync.** Open a book, then **Tools → Progress sync**. Choose
**Custom sync server** and enter the value ending in `/adapter/kosync`. Choose
**Register / Login**, enter the username and password and press **Login**.
Tick **Automatically keep documents in sync** if you want it, and keep
**Binary** under **Document matching method**.

**OPDS.** In the file browser's menu open **OPDS catalog** and add a catalog
with the URL ending in `/opds/v1.2`, the username `token` and the API token as
the password.

**KoInsight.** Install `koinsight.koplugin` from the
[KoInsight project](https://github.com/Ko-Insight/KoInsight) (its `plugins/`
folder) into KOReader's `plugins/` folder. Then open **Tools → KoInsight →
Set server URL** and enter the KoInsight URL without a trailing slash.

## Browsing with covers

The built-in OPDS catalog is a plain list of titles. The optional
[OPDS Plus](https://github.com/greywolf1499/opds_plus.koplugin) plugin
replaces that browser with one that shows covers, a grid view and series
details. Nothing in this guide needs it. OPDS Plus also reads the JSON
catalog at the URL ending in `/opds/v2`, which carries series positions and
shows the folders, shelves and newest books as separate rows. If OPDS Plus
is installed, the setup file adds that catalog to it as **Liseur Sync**,
with the same login; install the plugin before deleting the setup file, or
add the URL by hand. To sync new books through it, use the URL ending in
`/opds/v2/new`.

## Progress sync without a setup file

Generate a pairing code under **KOReader (kosync)** on the Devices page. On
the reader, set the custom sync server as above, choose **Register / Login**,
use any device name as the username and the pairing code as the password, and
press **Register**. The code works once and expires after 15 minutes.

## Troubleshooting

If login fails, check that the *Custom sync server* address ends in
`/adapter/kosync` and uses `https`. The server refuses credentials over plain
`http` unless `insecure_http` is on, see [deployment](deployment.md).

If positions do not match another reader, remember that Progress sync matches
a book by the file's content, so both readers need the same file. Keep
**Binary** matching.

If there is no KoInsight menu, the reader was offline at the first start. Turn
on Wi-Fi and restart KOReader, or install the plugin by hand as above.

If the patch did not run, KOReader ignores `patches/` when the file
`patches/.patches_disabled` exists, and some builds ship with patches off. Use
the manual setup.
