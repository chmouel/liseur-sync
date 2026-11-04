# ADR-0063: Speech-to-text has its own section and admin page

- **Status:** Accepted
- **Date:** 2026-11-04
- **Depends on:** [ADR-0057](0057-native-watched-folder-audiobooks.md),
  [ADR-0062](0062-configuring-audiobookshelf-from-the-admin-page.md)
- **Amends:** ADR-0062, which kept the speech-to-text settings off the
  Audiobookshelf form

## Context

The transcription settings (`stt_url`, `stt_api_key`, `stt_model`) were
added under `[audiobookshelf]`, with `LISEUR_ABS_STT_*` variables. The
native audiobook player (ADR-0057) uses them too, so a deployment that
never ran Audiobookshelf still had to configure it through that section.

ADR-0062 called them operational and left them off the form. They are
not like polling or timeouts: they decide whether a reader's audio
leaves the server and to whom, and they need a credential.

## Decision

**The settings move to a `[speech_to_text]` section with its own admin
page, written by the ADR-0062 rules.**

- The section holds `enabled` (default `true`), `url`, `api_key` and
  `model`, overridden by `LISEUR_STT_ENABLED`, `LISEUR_STT_URL`,
  `LISEUR_STT_API_KEY` and `LISEUR_STT_MODEL`. Defaults are unchanged:
  Groq and `whisper-large-v3-turbo`, inactive until a Groq key is set.
- **The old names are refused, not read.** A file carrying
  `audiobookshelf.stt_*`, or an environment carrying `LISEUR_ABS_STT_*`,
  stops the server with the new name for each one. A key that quietly
  stopped loading would only turn transcription off. The Audiobookshelf
  page also refuses to save such a file, since rewriting its table would
  drop them.
- **An enabled save is tested first** by transcribing one second of
  silence with the address, key and model that would be used. A disabled
  save is not tested. Enabling the Groq default without a key is refused.
- The credential, environment, rewrite and restart rules are ADR-0062's:
  `LISEUR_STT_API_KEY` is used for the test and never written; the
  non-secret variables make the page read-only; the table is rewritten
  surgically, proved and left `0600`; a blank key field keeps the saved
  key, and a separate box removes it.

## Consequences

- A deployment using the old names must rename them before the new image
  starts. `docs/deployment.md` lists the mapping.
- The Audiobookshelf restart check no longer covers transcription; the
  new page has its own.
- No migration and no API change: `stt_enabled` in the audio info
  response keeps its meaning.
