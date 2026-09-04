# ADR-0016: Token self-introspection

- **Status:** Accepted
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

The first Android integration exposed a second question: whether a newly
presented token belongs to the account the client already mirrored. A token's
`device_id` cannot answer it because every ordinary token mint creates a new
device identity. Treating that value as an account identity makes reconnecting
with a replacement token look like an account switch, so the client discards
its catalog mirror and sync cursor and performs a full replay. The replay is
safe and idempotent, but unnecessary.

## Decision

`GET /v1/token` answers for the bearer that presented it.

It returns that token's `id`, `device_id`, `name` and `scopes`, plus the
account's stable opaque `account_id`. The token fields use the same names
`GET /v1/tokens` already uses for the same things. `scope` continues to appear
alongside `scopes` for a singleton set for as long as ADR-0006's compatibility
window lasts.

`account_id` is an identifier for comparison, not a profile. It is stable
across every token minted for one account and differs between accounts. A
client compares it only together with the canonical server URL, keeps its
catalog mirror and sync cursor when both match, and treats a mismatch as an
account switch. It must not display the value or infer user-facing identity
from it. The username is deliberately not returned for this purpose: it is a
human-facing name rather than an immutable identity.

The route returns nothing else. Not the secret, not its hash, not the other
tokens on the account, and no account profile. The opaque account identifier
adds no authority and reveals no resource the bearer could not already reach;
it only lets a client recognize the same authority after credential rotation.

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

The one thing a call does write is the token's `last_used` timestamp, which
every bearer route already writes on every authenticated request: it is a
property of authenticating, not of this route. The rule is that nothing about
the credential's *authority* changes — scopes, device binding, expiry,
revocation and the secret itself are all exactly as they were. Access
bookkeeping moving is what makes a "last seen" column in the devices page
honest, and suppressing it here would make this route the one way to use a
credential without leaving a trace.

### It is not for OPDS

OPDS clients authenticate with HTTP Basic and have no use for this route.
It is a native API route under bearer auth like the rest of `/v1`.

## Consequences

Clients can degrade honestly: hide upload without `library-manage`, skip the
insights screen without `read-insights`, and tell the user which token they
pasted rather than which error they got.

Clients can also replace or re-paste a token without mistaking a credential
rotation for an account switch. A real account switch still clears account-
scoped mirrors and cursors before synchronization resumes.

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
3. **Stable account identity.** Return `account_id` from `GET /v1/token`,
   document its comparison rules, and cover replacement tokens for the same
   account and tokens belonging to different accounts. Implemented: the
   value is the account's opaque user id, pinned by
   `TestTokenAccountIDIsStableAcrossTokens`.

## Acceptance criteria

- A token with any scope set can read its own record; its token fields match
  what `GET /v1/tokens` reports for the same token.
- Two tokens minted for one account return the same `account_id`; tokens for
  different accounts return different values. Minting a replacement token
  does not require a client to discard that account's catalog mirror or sync
  cursor.
- The response never contains a token secret, a secret hash, or any token
  other than the caller's.
- A revoked or expired token gets 401, and an absent token gets 401.
- The route appears in `TestScopeEnforcement` in `internal/api`, which is
  this repository's scope matrix, asserting that both a `sync`-only and a
  `read-insights`-only token can read it where each is refused the other's
  routes. It is not in the open-route set.
- No credential authority changes through it — not scopes, device binding,
  expiry, revocation or the secret — and a bearer still cannot change its own
  scopes. The `last_used` timestamp that authenticating writes on every
  bearer route is not authority.
