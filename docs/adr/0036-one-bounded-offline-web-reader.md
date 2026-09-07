# ADR-0036: One bounded offline web reader

**Status:** Accepted  
**Scope:** Later  
**Amends:** [ADR-0007](0007-web-reader.md), [ADR-0030](0030-web-reader-reading-sessions.md), and [ADR-0028](0028-annotation-sync.md)

## Context

The browser reader is useful on phones and tablets, but a browser session
normally loses access to publication resources as soon as the network or
the reader token is unavailable. Installing one Home Screen app per book
would repeat the same shell and synchronization problems for every title.
The server also has a configured separate reader origin in which the
publication frame is intentionally isolated from the authenticated UI.

An offline client must not turn a bearer token or a personalized HTML page
into a cacheable credential. It must preserve the existing publication
sanitization, position/session accounting, annotation revisions, folder
authorization, and work/edition identity rules. iOS can evict web storage
and does not provide a dependable background upload guarantee for a
suspended Home Screen app.

## Decision

Provide one same-origin, multi-book installable web app in the `/ui/offline/`
namespace. The library offers an explicit download action for each visible
book; the installed app presents a local shelf rather than a copy of the
whole catalog. The app is generic and contains no account, book, cover,
token, CSRF, or personalized server-rendered data.

The service worker is scoped only to `/ui/offline/`. It caches a versioned,
explicit allowlist of generic shell and reader assets and handles top-level
offline navigations. It does not cache API responses, credentials,
publication bytes, or ordinary UI pages. Private publication resources and
reading state are stored in an account- and deployment-partitioned
IndexedDB database. Publications use digest-pinned, generation-based
snapshots and become visible only after a complete download is committed.

The worker revision hashes the embedded shell assets and rendered generic
reader. A new version installs its complete asset set before waiting for
existing readers to close. Each deployment has its own shell-cache namespace;
activation removes only that deployment's older generations. Controlled
navigations and modules come from the same generation, with a fresh matching
CSP nonce materialized for each reader navigation.

The app uses the existing Readium publication pipeline for local resources.
It persists positions, session checkpoints and finalized sessions before
attempting delivery, and retries those immutable payloads in the
foreground. Offline reading continues when a token expires; synchronization
requires a fresh credential for the same account and device identity.
There is no promise of upload while the app is suspended or closed.

Highlights, notes and bookmarks are authored against publication-relative
locators and use the existing annotation compare-and-set protocol.
Conflicts retain both local and server copies and require an explicit
choice. Removing a book removes only its publication bytes; pending reading
state and mutations remain available for later synchronization. Explicit
logout warns about unsynchronized changes and then clears private local
data.

Local mutations validate a persistent account generation inside their
IndexedDB transaction. Logout invalidates that generation before clearing
private data and notifies other windows; a late acknowledgement or download
cannot restore the signed-out account's data. Annotation acknowledgements
settle only the submitted mutation and preserve newer local intent.

A per-account/book Web Lock prevents concurrent readers from restoring the
same session checkpoint. Offline reading refuses to start without that
ownership mechanism. Successfully uploaded sessions retain local fingerprint
tombstones to reject reuse of their immutable IDs until account cleanup.
The shelf can drain queued changes after publication bytes have been removed.

When `reader_origin` is configured, the same-origin offline surface and its
library controls are not registered. The isolated reader remains
online-only; this preserves the deployment's origin boundary instead of
creating a second cross-origin authentication flow.

## Consequences

The feature requires an initial online launch in the installed app to sign
in and download books. Safari storage and an installed Home Screen app are
not assumed to share cookies or IndexedDB. Storage persistence is requested
when available, but installation is not a guarantee against quota
exhaustion, user clearing, application removal, or platform eviction.

A device that is offline may continue to read bytes it already downloaded
even after a server-side grant or token is revoked. Revocation prevents
future server access; it cannot erase disconnected local storage. Operators
and users must treat downloaded books as another local copy.

There are no native API or database changes in this ADR. The existing
server-side operation, session, work identity, and annotation semantics
remain authoritative when queued records reconnect.

## Implementation and acceptance

- [x] Serve a generic relative shell, manifest, icons and bounded worker.
- [x] Download complete digest-pinned publication graphs into IndexedDB.
- [x] Open saved publications without a live token.
- [x] Persist positions, session checkpoints, sessions and annotations
      before foreground synchronization.
- [x] Reconcile annotation revisions and tombstones with explicit choices.
- [x] Keep separate-reader-origin deployments online-only.
- [ ] Verify Home Screen installation, cold offline launch, storage behavior,
      safe-area layout, text selection and lifecycle behavior on supported
      iOS versions.

Until the final item is exercised on a real iOS device, documentation must
describe iOS support as subject to the platform's web-app storage and
lifecycle limits rather than as native-app durability.
