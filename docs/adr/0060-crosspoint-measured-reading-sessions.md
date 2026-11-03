# ADR-0060: CrossPoint measured reading sessions

- **Status:** Accepted
- **Date:** 2026-11-03
- **Amends:** [DESIGN §6.3](../DESIGN.md#63-sessions-inferred-for-position-only-devices)

## Context

Progress uploads cannot measure time spent reading. CrossPoint provides a
`reader.session` plugin event with counted forward-page dwell and a stable
retry ID. Its compact `end_time` removes idle gaps; the envelope's `event.ts`
is the actual emission time. Counting both that event and inferred sessions
from the same device would overstate reading.

## Decision

An events-only plugin posts to `/adapter/kosync/sessions`, authenticated by
the existing kosync pairing slot. No firmware change or new credential is
required. Devices verifies the original pairing password and provides a ZIP
containing the manifest, private configuration and installation instructions.

Downloading explicitly activates a fixed, idempotent per-slot cutoff. Older
inference remains. Progress received at or after the cutoff is excluded from
both inference selection and transactional revalidation, and no longer needs
inference-specific compaction protection. Events starting before the cutoff
are acknowledged without counting. An offline or disabled plugin never
silently returns the slot to inference.

Events become ordinary native sessions with `origin: kosync`, the same
`kosync:<slot>` device identity, binary fingerprint provenance and measured
`active_ms`. `started_at` is the trusted first counted dwell; `ended_at` is
actual emission, which determines the account-local statistics day. Compact
end is validated against start plus counted duration, not stored as wall time.
No page number is fabricated. An unknown digest creates a pending work as
ordinary kosync does. An edition is attached only when exactly one granted
catalog file matches and its edition belongs to that work.

A receipt keyed by account, slot and event ID stores a canonical payload hash
and outcome in the same transaction as the session. It is checked before
mutable identity resolution and survives session rollup. Receipts follow
sessions on splits and works on merges; deleting the entire work removes its
receipts with the rest of its reading graph. Pre-cutoff receipts have no work.
An identical retry returns its original outcome; changed payloads conflict.

## Consequences

The plugin ZIP carries a password-equivalent secret and must remain private.
Slot revocation and account/adapter disabling gate both kinds of upload.
CrossPoint counts only successful forward turns and may lose its small offline
outbox. Measured statistics can therefore undercount; the server does not
fabricate replacements. EPUB optimization changes identity and is unsupported.

## Implementation and acceptance

Server, store migrations, plugin generation, Devices setup and protocol docs
are implemented together. Shared backend tests cover cutoff races, retention,
identity changes, conservative edition attribution and replay. Adapter and UI
tests cover authentication, validation, CSRF and the configured ZIP. Deployment
and a real-device reading/upload check remain separate delivery steps.

Firmware contract: [plugin-events.md at 2ec01c9](https://github.com/crosspoint-reader/crosspoint-reader/blob/2ec01c9/docs/plugin-events.md).
