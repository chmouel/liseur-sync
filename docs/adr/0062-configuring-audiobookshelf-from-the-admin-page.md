# ADR-0062: Configuring Audiobookshelf from the admin page

- **Status:** Accepted
- **Date:** 2026-11-03
- **Depends on:** [ADR-0013](0013-admin-panel.md),
  [ADR-0055](0055-following-an-audiobook-on-audiobookshelf.md),
  [ADR-0061](0061-several-mirrors.md)

## Context

ADR-0055 gave the Audiobookshelf integration a `[audiobookshelf]`
section and an environment override for every field, and said the API
key stays in configuration. An operator could only turn the integration
on by editing a file on the server and restarting, which is the same
work as not having a form.

The mirror already has the opposite arrangement (ADR-0061): an admin
page that reads the file on every request, rewrites one section
surgically, proves the result before replacing anything, and tells the
navigator a restart is needed. Its credential rule is the one that
matters here: an environment variable may own a secret, is used to test
a connection, and is never copied into the file.

## Decision

**The `[audiobookshelf]` section has an administration page, written by
the same rules as the mirror's, and startup keeps reading the file.**

- **Only the settings that are a decision are on the form:** enabled,
  server URL, local account and API key. Polling, timeout, overlap,
  active-days and the speech-to-text settings stay in the file or the
  environment, unchanged by a save.
- **An enabled save is tested before anything is written.** The local
  account must exist, and one `/api/me` round trip must succeed with the
  key that would actually be used. A disabled save needs neither a
  reachable server nor a key, so pointing at a server that is down never
  blocks turning the integration off.
- **The URL is cleaned whatever the state.** A saved address never
  carries a username, password, query or fragment. The https rule is
  only held against an enabled peer, because a disabled one is never
  spoken to. The page strips those parts again before showing a file
  value, including in the form, because the file it reads need not be
  the one the running process started with.
- **The file keeps the address as typed, and startup normalises it
  once.** Saving writes what the operator entered rather than what
  validation made of it. The peer identity the links, rejections and
  imported sessions are keyed by is derived from the address startup
  stored, so writing a normalised value would trim the address again on
  the next save-and-reload and move existing history out from under
  itself. The form is prefilled with the saved address including its
  trailing slashes for the same reason: re-saving an unchanged section
  must write the same bytes.
- **The environment owns the key it is given.** `LISEUR_ABS_API_KEY`
  overrides the file key, is used for the test, and is never written
  back. A variable set but empty refuses an enabled save rather than
  falling back to a stale key. A blank key field on the form keeps the
  key the file already holds.
- **A non-secret `LISEUR_ABS_ENABLED`, `LISEUR_ABS_BASE_URL` or
  `LISEUR_ABS_ACCOUNT` makes the page read-only** and refuses a save:
  the next start would override the values, and reporting success for
  values nothing reads is worse than saying who owns them. Variables
  the form does not expose do not close it.
- **The file is rewritten surgically and proved.** Only the
  `[audiobookshelf]` table moves; the rest of the file keeps its bytes
  and comments. The result is decoded, compared against the original
  with the section cleared, and held to the environment a start reads —
  `insecure_http` included — but to the audiobookshelf rules only. The
  write follows a symlink and renames into place. Its mode follows what
  it is about to hold: a file with a stored credential in it is made
  owner-only (`0600`), a secretless one keeps the permissions it had,
  and a new file is created `0600` — including when the save found
  nothing to change and only tightened a credential the file already
  held. What counts as a stored credential, and why an unreadable one is
  refused rather than reported secret-free, is ADR-0061's definition: it
  is the same check for both saves. A file that does not parse is
  refused rather than repaired, and the refusal names its line and
  column without repeating the source literal the decoder quoted. Mirror
  and Audiobookshelf saves serialise on one lock, so two tabs cannot
  each rewrite a body read before the other wrote.
- **No hot reload.** The page reads the file, so a saved setting can be
  edited while the running process keeps what it booted with; it says
  when the saved configuration differs from the running one.

## Consequences

- An administrator can turn the integration on, point it at a server and
  rotate its key from the browser, with a restart and nothing else.
- The page needs a writable configuration file, and is a page rather
  than the only way in: the file and the environment still work, and a
  deployment that started without a writable file is told so cleanly.
- No migration, no API or protocol change, and no second
  Audiobookshelf. The credential is still stored in configuration, and
  is still never logged, rendered or returned.
- A save can fail because somebody else's server is down. That is the
  point: a setting that would not work is not written as though it had.

## Acceptance criteria

- An enabled save with a rejected key, an unknown local account or an
  unreachable server writes nothing and leaves one audit line.
- A disabled save writes with no reachable peer and no key.
- A blank key field keeps the saved key; typing one replaces it; an
  environment key is used for the test and never appears in the file,
  the page, a redirect or an error.
- A file with a mirror, a list entry, STT settings and unrelated keys
  keeps all of them, its mode and its symlink through a save.
- A forged CSRF token, a reader and a rate-limited reauth all write
  nothing; an accepted save redirects relatively to the page.
- The page shows the credential's source and the running status beside
  the saved setting, and says a restart is required when the effective
  saved configuration — the file with the environment overlaid — differs
  from the one the process started with.
