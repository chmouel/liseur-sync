# ADR-0007: Web reader

- **Status:** Proposed
- **Date:** 2026-08-12
- **Depends on:** [ADR-0005](0005-upload-and-ingestion.md),
  [ADR-0006](0006-catalog-api-and-opds.md)

## Context

A browser reader completes the self-hosted product, but an EPUB is an archive
of publisher-controlled HTML, CSS, images, fonts, and sometimes scripts.
Serving that content on the authenticated UI origin would let a malicious
book attack cookies, CSRF state, and API access.

Browser reading must also use the same position and session semantics as
Android and desktop rather than create a privileged web-only sync path.

## Decision

The web reader is the final server feature, after ingestion and catalog APIs
are stable.

### Renderer

Vendor a FOSS renderer with no CDN dependency. The first implementation will
evaluate foliate-js (MIT) as the preferred engine; epub.js remains the
fallback if foliate-js cannot be integrated without importing an unsuitable
build pipeline. The chosen revision and license are committed with the
vendored assets.

### Content isolation

The authenticated shell may live at `/ui/read/{book_id}`, but publication
documents do not execute as ordinary same-origin UI documents.

- Render book documents in sandboxed iframes without `allow-same-origin`.
- Apply a strict content security policy that blocks external network access
  and script escalation.
- Use blob/document isolation supported by the chosen renderer.
- Offer a separately configured content origin as the hardened deployment
  mode.
- Serve extracted assets with fixed MIME types and `nosniff`; never pass
  publisher SVG or HTML through as a cover.

The browser security model and CSP are integration-tested with a hostile EPUB
fixture that attempts cookie, parent DOM, storage, and network access.

### Authentication and device identity

The reader is an ordinary native API client. An authenticated UI session may
transactionally mint short-lived tokens with `sync` and `library-read`,
bound to `web:<session-id>:<token-id>` devices.

- A `web_session_tokens(session_id, token_id)` relation stores only token
  IDs; secrets remain hashed in the token table.
- Each loaded reader document receives its own secret once into memory, never
  local storage, session storage, or a cookie. Reloading or opening another
  tab mints another linked token and does not revoke tokens used by other
  tabs.
- Tokens have a short expiry and a bounded active count per UI session.
  Expired tokens are pruned before minting. If the bound is reached, minting
  fails clearly rather than silently revoking an active tab.
- A live tab refreshes its token before expiry and revokes its previous token
  only after the replacement is ready in that tab.
- Logout, session expiry, explicit session revocation, and password change
  revoke all linked tokens before deleting or invalidating sessions.
- The devices UI groups linked tokens as one web session while retaining
  individual revocation and audit records.

### Sync and statistics

Positions use the native `/v1/ops` and `/v1/changes` APIs. The web locator
shape must round-trip the same Readium-compatible locator used by Liseur;
progression alone is not considered lossless.

Reading stretches are submitted through `/v1/sessions`, with idle handling
and deterministic idempotency matching native clients. The server gives its
own reader no privileged access to another user's work, session, or catalog
mapping.

## Consequences

Sandboxing may limit publisher scripting and some unusual EPUB features.
Security wins over perfect compatibility; unsupported active content is
reported rather than silently granted more privilege.

A short-lived browser token adds lifecycle work but keeps the web reader on
the same authorization and audit path as every other client.

## Implementation phases

1. Renderer spike and recorded engine decision.
2. Isolated content-serving layer and hostile-book security suite.
3. Reader shell, navigation, typography, and accessibility.
4. Device-token lifecycle, position sync, and reading sessions.

## Acceptance criteria

- Publication content cannot read UI cookies, storage, CSRF data, or the
  parent DOM and cannot make arbitrary network requests.
- CSP, sandbox attributes, MIME, and `nosniff` headers are asserted by tests.
- Multiple tabs can read and sync concurrently without revoking each other.
- Per-tab refresh and collective session revocation are atomic, and active
  token bounds are enforced without evicting an unknown live tab.
- A position set in web, Android, or desktop opens at the same logical
  location in the others, subject to edition compatibility.
