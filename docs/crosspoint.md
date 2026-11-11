# Use CrossPoint on your Xteink with liseur-sync

A step-by-step version with screenshots is on the
[CrossPoint setup page](https://chmouel.github.io/liseur-sync/crosspoint/).

You can connect an Xteink X3 or X4 running CrossPoint to liseur-sync to
download books, share your reading position with other readers, and record
reading time.

This guide starts with ordinary reading-position sync, then explains how to
install the optional reading-statistics plugin. You can install the plugin
over Wi-Fi without removing the reader's SD card.

If your reading position already syncs, go straight to
[Enable reading statistics](#enable-reading-statistics).

## Before you start

Have these ready:

- Your liseur-sync website address and an account you can sign into.
- Your Xteink running CrossPoint, with a working Wi-Fi connection.
- A computer to download the plugin and transfer its files.

For measured reading statistics, your CrossPoint firmware must support
reading-session events, called `reader.session` in its documentation.
Having a Plugins menu alone does not confirm this. Check your firmware's
release notes or ask its maintainer if you are unsure. Older firmware that
only supports `reader.exit` cannot send the required statistics.

Your liseur-sync Devices page must also have a **CrossPoint reading
statistics** section. If it is missing, ask whoever runs your server whether
it needs updating.

### Which password goes where?

There are separate credentials for different parts of the setup:

- Your account password signs you into the liseur-sync website.
- A pairing code connects CrossPoint Sync to your account. You enter it as
  the password on your Xteink, and reuse that same code when downloading the
  statistics plugin for that device.
- A library token lets CrossPoint browse and download your books. You only
  need this for the optional book-catalog setup below.

Save your pairing code in a password manager, labelled with the device name.
After pairing, the code remains that device's sync password even though it
has already been used to register the device. The server cannot show you
the original code again.

## Connect reading-position sync

Skip this section if your Xteink already syncs with your liseur-sync account.

### Generate a pairing code

1. Sign into liseur-sync in your computer's browser.
2. Open **Devices**.
3. Under **KOReader (kosync)**, click **Generate pairing code**.
4. Save the code somewhere private before leaving the page.

The KOReader heading also applies to CrossPoint. Both use the same sync
connection.

### Enter the settings on your Xteink

On the reader, open **Settings > Connectivity > CrossPoint Sync** and enter:

| Setting | What to enter |
| --- | --- |
| Sync Server URL | Your liseur-sync address followed by `/adapter/kosync` |
| Server Type | **KoSync** |
| Document Matching | **Binary** |
| Username | A new device name, such as `my-xteink` |
| Password | The pairing code you just saved |

For example, if your website is `https://reading.example.com`, the Sync
Server URL is `https://reading.example.com/adapter/kosync`. Replace the
example address with your own.

If liseur-sync is served below a path prefix, this URL is still rooted at
`/adapter/kosync` on the same host, and the statistics plugin posts there
too: the proxy has to route `/adapter/kosync*` through without stripping
it (see [deployment.md](deployment.md#serving-below-a-path-prefix)).

Press **Sign Up** on the reader. This pairs your device with your existing
liseur-sync account. Leave ordinary CrossPoint Sync enabled, and leave
**Sync Clippings** off.

Choose **Binary** even if **Filename** is the default. Binary matching lets
the server recognise the book file across devices.

## Download books from your liseur-sync library

This is optional for installing the plugin, but it helps your reading
position and statistics attach to the same books you see on the website.

1. On liseur-sync's **Devices** page, create a token with **library-read**
   permission. Give it a name you will recognise, such as `Xteink library`.
2. Copy the token secret when the website shows it, and keep it private.
3. On the Xteink, open **Settings > Connectivity > OPDS Servers** and add a
   server.
4. Enter your website address followed by `/opds/v1.2` as the server URL.
5. Enter the literal word `token` as the username.
6. Enter the token secret as the password.
7. Open **OPDS Browser** from Home, then browse and download a book. Once
   plugins are enabled, **OPDS Browser** moves into **Home > Plugins**.

For the example website above, the catalog URL would be
`https://reading.example.com/opds/v1.2`.

OPDS is the reader's book-catalog browser. Its login is separate from
CrossPoint Sync, so use the library token here rather than your pairing code.

Keep the downloaded EPUB unchanged. **Optimize EPUB**, conversion, or other
rewriting can make the reader's copy appear to be a different book. Plain
file transfer is fine when it preserves the original file.

## Enable reading statistics

The statistics plugin records counted reading time and sends it to the
**Insights** page on liseur-sync. It runs in the background once installed.

> Downloading the plugin switches this device to measured statistics.
> Your older estimates stay, but future position updates stop producing
> time estimates. Install and enable the plugin immediately afterward.
> Leaving it disabled or offline does not switch estimates back on.

### Download your configured plugin

1. On your computer, sign into liseur-sync and open **Devices**.
2. Expand **CrossPoint reading statistics**.
3. Find the device name you used in CrossPoint Sync.
4. Enter that device's saved pairing code in **existing KoSync pairing
   password**.
5. Click **Enable statistics & download plugin**.
6. Extract the downloaded `liseur-stats.zip` file on your computer.

You will get a folder named `liseur-stats` containing `device.json`,
`config.json` and `README.txt`. These files are already configured for your
server and device. You do not need to edit them.

Keep the ZIP and `config.json` private. They contain a credential that lets
the plugin send data as your device.

If you have lost the pairing code, follow
[Replace a lost pairing code](#replace-a-lost-pairing-code) before continuing.
Generating a new code by itself does not change an existing device's password.

### Install over Wi-Fi

You can transfer the plugin directly to the reader's storage. You do not
need an SD card connected to your computer, or a separate card reader.

1. Start **File Transfer** on your Xteink.
2. Connect your computer to the same network as the reader. If the reader
   creates its own Wi-Fi network, connect your computer to that network.
3. Open the web address shown on the reader in your computer's browser.
   This is the Xteink's transfer page, separate from your liseur-sync website.
4. Open **Files** or **File Manager**, depending on your firmware's wording.
5. Go to the top level of the reader's storage. Create a folder named
   `plugins` if it does not already exist.
6. Open `plugins` and create a folder inside it named `liseur-stats`.
7. Open `liseur-stats` and upload the three files from the extracted folder
   on your computer: `device.json`, `config.json` and `README.txt`.

The finished layout should be:

```text
plugins/
  liseur-stats/
    device.json
    config.json
    README.txt
```

The transfer page may never use the words “SD card”. Its file manager is
showing the reader's storage. The “top level”, also called the root, is the
location above folders such as `Books`.

Extract the ZIP before uploading. Avoid creating an extra folder level such
as `/plugins/liseur-stats/liseur-stats/`.

### Install with an SD card reader instead

Use this method if you prefer copying files directly or your firmware's
transfer page does not provide folder management.

1. Power off the Xteink before removing its SD card.
2. Connect the card to your computer using a card reader.
3. Open the card and create a `plugins` folder at its top level if needed.
4. Copy the whole extracted `liseur-stats` folder into `plugins`.
5. Safely eject the card, put it back into your Xteink and start the reader.

Use the same folder layout shown above.

### Turn the plugin on

1. Exit File Transfer if it is still running.
2. On the Xteink, open **Settings > Connectivity > Plugins** and enable
   plugins.
3. Refresh or restart plugins if your firmware offers that option.
   Otherwise, restart the reader.
4. Check **Home > Plugins** for **Liseur reading statistics**.
5. Close and reopen your book to start a fresh reading session.

Keep ordinary **CrossPoint Sync** enabled with **Binary** matching. The
plugin adds reading-time reporting alongside position sync.

The liseur-sync Devices page will show **Measured statistics enabled**.
That confirms the server has activated it for the device. To confirm the
reader is actually uploading, do the reading test below.

## Check that it works

1. Open a book you downloaded from liseur-sync.
2. Read for a few minutes, turning pages forward normally. Spend at least
   a few seconds on each page.
3. Close the book or put the reader to sleep. CrossPoint prepares a reading
   summary when you leave the book or sleep.
4. Give it a chance to upload using a saved, working Wi-Fi connection.
5. Open **Insights** on your liseur-sync website and check for the reading
   time you just recorded.

Start this test after installing the plugin and reopening the book.
Sessions that started before you enabled statistics are ignored, and old
reading time is not reconstructed.

### What counts as reading time?

CrossPoint counts time on a page when you make an ordinary forward page
turn. A page needs at least two seconds of reading time to count, and one
page contributes at most 30 minutes. A session needs at least ten counted
seconds before CrossPoint sends a summary.

Backward page turns, chapter skips and failed turns do not count. Leaving
a book open does not continuously add reading time. The total can therefore
be lower than the time between opening and closing a book.

### Reading without Wi-Fi

You can read offline. CrossPoint queues summaries and tries to upload them
when it can connect. It may briefly connect to saved Wi-Fi while going to
sleep if the battery is at least 20%.

The reader has a small upload queue. If it fills up during a long period
offline, queued records can be lost. Connect regularly when you want to
keep your statistics complete. liseur-sync does not invent time to replace
missing records.

## Replace a lost pairing code

If you cannot retrieve the password from your reader or your password
manager, pair CrossPoint again under a new device name. Your books, saved
positions and existing statistics remain in your liseur-sync account.

1. On liseur-sync's **Devices** page, click **Generate pairing code**.
2. Save this new code in your password manager.
3. On the Xteink, open **Settings > Connectivity > CrossPoint Sync**.
4. Change **Username** to a new name, such as `my-xteink-new`.
5. Change **Password** to the new pairing code.
6. Keep your server address, **KoSync** and **Binary** settings, then press
   **Sign Up**.
7. On the website, find the new device name under **CrossPoint reading
   statistics** and use that same new code to download its plugin.
8. Install the new plugin files, replacing the old files if you previously
   installed the plugin. Restart and reopen your book.
9. Once the new pairing works, revoke the old device entry on **Devices**.

The code to keep is the one generated in step 1. You use it as both the
reader's new sync password and the password for downloading its configured
plugin. Your library token and OPDS setup can stay as they are.

## Troubleshooting

### “Username is already registered”

That device name is already paired. If you are replacing a lost password,
choose a new name and use a fresh pairing code. Revoking an old device does
not make its name available for registration again.

### The plugin download says the password does not match

Use the pairing code that you entered as the password for the selected
device name on your Xteink. Check that you picked the right device entry.

Your website account password and your library token will not work here.
If the original pairing code is lost, use the replacement steps above.

### There is no Plugins menu, or the plugin does not appear

Check that your firmware supports plugins and `reader.session`. After
enabling plugins, restart the reader and check **Home > Plugins** again.

Check the uploaded files. `device.json` must be directly inside
`/plugins/liseur-stats/`. A ZIP left on the reader, or a second nested
`liseur-stats` folder, will not give you that layout.

### I cannot find “SD card” on the transfer webpage

Look for **Files** or **File Manager**. You are already browsing the reader's
storage. Go up to its top level to create the `plugins` folder. You do not
need to attach a card to your computer when using Wi-Fi transfer.

### Position sync works, but no reading time appears

Check that the plugin is installed and enabled, not just downloaded.
Reopen the book and repeat the reading test with ordinary forward turns.
Connect to working Wi-Fi and check that the reader's battery is sufficiently
charged if you rely on uploading during sleep.

If you changed the device's sync pairing, download and install a plugin for
the new pairing too. The old plugin still carries the old device credential.

Older firmware may support position sync without supporting reading-session
events. Confirm `reader.session` support with your firmware's documentation.

### The catalog says “Failed to parse feed”

Check the OPDS URL ends in `/opds/v1.2`, the username is `token`, and the
password is a valid token with **library-read** permission. Ask your server
administrator to check that your account has access to the book folder.
CrossPoint can show this message for access problems as well as feed errors.

### The same book appears separately or does not share its position

Check that CrossPoint Sync uses **Binary** matching. Download the original
EPUB from liseur-sync and avoid optimizing or converting it. A changed copy
can be recognised as a different book, even when its title is identical.

### I disabled the plugin and new statistics stopped

Enabling measured statistics stops future time estimates for that device.
Disabling or removing the plugin does not restore those estimates. Re-enable
the plugin and start a fresh book session to resume measured reporting.
Your previously recorded statistics remain.

## Further reading

- [CrossPoint integration details](integrating.md#crosspoint)
- [CrossPoint firmware plugin documentation](https://github.com/crosspoint-reader/crosspoint-reader/blob/develop/docs/sd-plugins.md)
- [CrossPoint reading-session support](https://github.com/crosspoint-reader/crosspoint-reader/blob/2ec01c9/docs/plugin-events.md#readersession-contract)
