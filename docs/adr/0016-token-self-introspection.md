# ADR-0016: Token self-introspection

- **Status:** Proposed
- **Date:** 2026-08-15
- **Depends on:** [ADR-0006](0006-catalog-api-and-opds.md)

## Context

ADR-0006 gave tokens a set of scopes, so what a client may do now varies per
token: catalog reading, sync, uploading and insights are separate grants a
user can combine.

A client cannot find out which combination it holds. `GET /v1/tokens`
enumerates the account's tokens and is authenticated with the one-hour login
credential, which a client has only during setup and does not keep. The
common case is a token pasted into an app — from the web UI, from another
device, from a password manager — with no login credential anywhere near it.

Such a client has two options today: assume the widest scope set and let the
user discover the truth as 403s on buttons that should not have been there,
or assume the narrowest and hide features the user paid for. Both are worse
than asking.

## Decision

`GET /v1/token` answers for the bearer that presented it.

It returns that token's `id`, `device_id`, `name` and `scopes` — the same
field names `GET /v1/tokens` already uses for the same things, so a client
parses one token shape. `scope` continues to appear alongside `scopes` for a
singleton set for as long as ADR-0006's compatibility window lasts.

It returns nothing else. Not the secret, not its hash, not the other tokens
on the account, not the user's identity beyond what the caller already
proved. A token describes itself; the account is a different question with a
different credential.

The route requires a valid, unrevoked, unexpired token and no particular
scope. A token asking what it is learns nothing it did not already possess —
it is holding the credential — so requiring a scope would only mean that the
narrowest tokens, the ones whose limits most need discovering, are the ones
that cannot ask. It is declared in the scope table in
`internal/api/routes.go` as authenticated with no scope requirement, not
added to the open-route set: it is authenticated like every other route
except `/healthz`, login, invite registration and adapter pairing.

Singular `/v1/token` against plural `/v1/tokens` is deliberate: one is the
token you are, the other is the tokens you have. They are different
resources with different credentials, and the URL should say so.

### It cannot change anything

`GET` only. ADR-0006's rule that a bearer cannot expand its own scopes is
untouched — expansion still requires a fresh login credential through
`PATCH /v1/tokens/{id}`. Introspection tells a client where the wall is; it
does not move it.

### It is not for OPDS

OPDS clients authenticate with HTTP Basic and have no use for this route.
It is a native API route under bearer auth like the rest of `/v1`.

## Consequences

Clients can degrade honestly: hide upload without `library-manage`, skip the
insights screen without `read-insights`, and tell the user which token they
pasted rather than which error they got.

A revoked token learns it is revoked as a 401 from this route like any
other, which is the answer it needed.

The endpoint is one more surface that reflects a credential's own claims. It
adds no new authorization decision, so it adds no new way to get one wrong —
provided the tests pin the fact that it never reveals a second token.

## Implementation phases

1. **The route.** `GET /v1/token` in `internal/api`, declared in the scope
   table, returning the calling token's public fields.
2. **Contract.** `docs/openapi.yaml` and the client-facing section of
   `docs/integrating.md`, in the same commit — including the sentence that
   tells a client to call this before drawing a menu.

## Acceptance criteria

- A token with any scope set can read its own record; the response matches
  what `GET /v1/tokens` reports for the same token.
- The response never contains a token secret, a secret hash, or any token
  other than the caller's.
- A revoked or expired token gets 401, and an absent token gets 401.
- The route appears in the scope matrix test in `internal/api` and is not in
  the open-route set that the open-route regression test guards.
- No mutation is possible through it, and a bearer still cannot change its
  own scopes.
