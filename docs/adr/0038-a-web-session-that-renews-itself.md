# ADR-0038: A web session that renews itself

- **Status:** Accepted; implemented
- **Date:** 2026-09-10

## Context

A browser session lasted seven days and never moved. The expiry was
written once, at sign-in, into both the `auth_sessions` row and the
cookie, and nothing extended it. So the owner of a personal server was
signed out every week, forever, and the only remedy on offer was to
raise the constant.

Seven days is a defensible number for an application people sign in to.
It is the wrong shape for this one. Reading is a habit: the same browser
comes back most days for months, and the sign-in it is asked for adds no
security — the account is not more protected by a password typed on a
schedule into a machine that was already trusted yesterday.

## Decision

A web session lasts `web_session_ttl_days` (default 180) of **disuse**,
and slides. Every authenticated request may push the expiry out to a
fresh window, in the row and in the cookie, so a browser somebody reads
in is never signed out, and one abandoned on a borrowed laptop still
lapses.

Three bounds keep it from being an unbounded credential:

- **The write is throttled.** A request extends the session only when
  the new expiry would be at least 24 hours beyond the stored one, so a
  session costs at most one `UPDATE` per day however many pages it
  loads. It needs no new column and no migration.
- **The extension only moves forward, and never revives.** The `UPDATE`
  is guarded on `revoked_at IS NULL` and on the row not having lapsed at
  the time of the request, so a page view racing a sign-out cannot bring
  the session back. It is best-effort: a store that refuses leaves the
  session exactly as valid as it was, because a page must not fail over
  a housekeeping write.
- **Renewal happens behind authentication only**, in the one middleware
  every signed-in route passes through. The signed-out visitor and the
  login page never touch it.

There is no "keep me signed in" checkbox. A checkbox asks every reader,
on every device, to make a judgement they have no way to make, and the
honest default was going to be "checked" anyway. One setting the
operator chooses once is a smaller thing than a question asked forever.

## Consequences

A stolen session cookie is worth more than it was: up to half a year if
the thief keeps using it, rather than a week. The mitigations are the
ones that already existed and are unchanged — `HttpOnly`,
`SameSite=Strict`, `Secure` unless `insecure_http`, no `Path` widening,
sign-out revoking reader tokens across every session of the account, and
the admin operations (disable, password reset, revoke-all) that cut
every session at once. Every high-impact admin action still re-verifies
the password, which matters more now that the cookie behind it is
long-lived.

Lowering the setting does not shorten sessions already issued; they keep
the window they were minted with until they lapse. `Housekeep` is
unchanged and still deletes expired and revoked sessions — the rows just
live longer. On upgrade nobody is signed out: an existing seven-day
session is simply extended on its next request.

## Acceptance criteria

- A fresh sign-in's cookie and row both expire one configured window
  out, and the setting is what decides it.
- A request on a session more than a day from its last extension renews
  both cookie and row, keeping the same secret; a session renewed
  moments ago is left untouched, with no `Set-Cookie` at all.
- A revoked or lapsed session is never extended, at the store layer, on
  either backend.
- `web_session_ttl_days` is refused outside 1..3650 at startup, and
  `docs/deployment.md` states the lifetime, the sliding behaviour, and
  that lowering it does not shorten sessions already issued.
