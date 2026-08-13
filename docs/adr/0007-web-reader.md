# ADR-0007: Web reader

- **Status:** Deferred
- **Date:** 2026-08-12
- **Depends on:** [ADR-0005](0005-upload-and-ingestion.md),
  [ADR-0006](0006-catalog-api-and-opds.md)

## Context

A browser reader would complete the self-hosted product, but it is the
largest and most dangerous feature on the list. An EPUB is an archive of
publisher-controlled HTML, CSS, images, fonts, and sometimes scripts.
Serving that on the authenticated UI origin would let a malicious book
attack cookies, CSRF state, and API access.

It is also the feature the product needs least. Liseur on Android and
desktop already read EPUBs and sync positions; a user who can download from
the catalog can already read the book. Nothing in
[ADR-0001](0001-content-server.md)'s four-step definition of the first
release requires a browser reader.

## Decision

**Not now.** No renderer is vendored and no reader routes exist until the
catalog API and OPDS are stable and in real use.

That condition has since been met: the catalog API, OPDS, metadata
editing, search and duplicate reporting are all built, and this is the
next feature on ADR-0001's list. What is still missing is the decision
itself — renderer, isolation, and token lifecycle — which this ADR says
must be made deliberately rather than inherited from a placeholder. Until
that replacement is written, "not now" still stands.

Two constraints are recorded now, because they bind features that come
earlier:

- **Publication content never executes on the authenticated UI origin.**
  When a reader is built it renders inside sandboxed iframes without
  `allow-same-origin`, under a policy that blocks external network access,
  with a separately configured content origin as the hardened deployment
  mode. This is why cover extraction (ADR-0004) transcodes to a bounded
  raster format and serves fixed MIME types with `nosniff` instead of
  passing publisher SVG or HTML through: that rule is needed as soon as the
  server serves any extracted asset, reader or not.
- **The reader is an ordinary API client.** It uses `/v1/ops`,
  `/v1/changes`, and `/v1/sessions` exactly as Android and desktop do, with
  short-lived scoped tokens bound to a web device identity, and gets no
  privileged access to another user's work, session, or catalog mapping.
  Positions must round-trip the same Readium-compatible locator the other
  clients use; progression alone is not lossless.

How those tokens are minted, refreshed, and revoked across tabs is a real
design problem with several defensible answers. It is deliberately left open
rather than settled years before anyone writes the code.

## Consequences

Users who want to read in a browser must wait, or use a Liseur client. In
exchange, the MVP does not carry the attack surface of rendering hostile
publisher markup, and the token-lifecycle design is made when there is a
reader to test it against.

## Acceptance criteria

When this ADR is revisited, it must be replaced by a full decision covering
renderer choice, content isolation, and token lifecycle. Until then the only
binding criterion is:

- No extracted publication asset is served in a way that lets publisher
  markup execute against the authenticated UI origin.
