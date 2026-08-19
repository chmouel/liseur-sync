# ADR-0026: Credential enrolment does not weaken account authentication

- **Status:** Accepted
- **Date:** 2026-08-19
- **Depends on:** [ADR-0013](0013-admin-panel.md)
- **Amends:** ADR-0013's definition of high-impact administration, and
  sections 7.1, 8.2 and 8.3 of [DESIGN.md](../DESIGN.md)

## Context

The server has two ways to establish durable access that do not meet the
security rules around them.

The first is kosync open registration. Stock KOReader sends one value in its
`password` field and later authenticates with a key derived from that value by
MD5. The normal liseur-sync flow treats the value as a short-lived pairing
code: it creates one revocable device credential for an existing account, and
it is never the account password.

With `open_registration` enabled, the same value instead becomes both the new
account's password and the kosync device secret. The database then contains an
Argon2id password hash and a SHA-256 hash of the MD5-derived device key. An
attacker with the database can test password guesses against the latter with
two fast hashes, bypassing the cost that Argon2id exists to impose. The outer
SHA-256 protects the stored device credential from direct use; it does not
make a human password expensive to guess.

There is no way to repair that mode while keeping its contract. The protocol
supplies one secret, but an account password and a device credential must be
independent secrets. Inventing an account password would leave the new user
unable to sign in, while returning a second secret would no longer be stock
kosync registration.

The second gap is in the administration panel. ADR-0013 requires the acting
administrator to re-enter their password before a high-impact mutation. That
check is independently rate-limited by account and client address so that a
stolen browser session is not enough to take over another account or mint a
credential for it.

Creating an account and creating an invite currently require an administrator
session and its CSRF token, but not the administrator's password. CSRF stops a
different site from driving the browser; it does not constrain somebody who
possesses the session, because that person can load the form and receive its
CSRF token. Either action lets such an attacker establish access that survives
revocation of the stolen session. An invited or administrator-created account
can then sign in and mint any non-admin scope for itself.

## Decision

### Kosync enrols a device; it never creates an account

Remove open registration. `POST /adapter/kosync/users/create` always consumes
a valid, unexpired, single-use pairing code and creates one kosync device slot
for the account that issued it. Supplying an account password to that route
never creates an account and never creates a device credential.

Remove `open_registration`, `LISEUR_OPEN_REGISTRATION`, `Config.OpenRegistration`
and `kosync.Server.OpenReg` rather than leaving a disabled branch behind. A
TOML file that still names `open_registration` fails startup through the
existing unknown-key check. The obsolete environment variable has no effect.
The example configuration, deployment guide and administration overview stop
advertising the setting.

Account creation remains available through the first-run setup, the
administrator CLI, the administration panel and invite registration. A user
created by any of those paths pairs KOReader afterwards, so the account
password and device credential never share input material.

This project has not shipped and the production instance never enabled open
registration, so there are no affected credentials to migrate. If evidence of
an instance that used the mode appears before implementation, every kosync
device created through it must be revoked: removing the route does not remove
the fast verifier already stored for such a device.

### Creating durable access requires password re-verification

Creating an account or an invite from the administration panel goes through
the existing `reauth` gate before it changes the store or generates a secret.
The form carries `admin_password`, distinct from the new user's password, and
both account and client-address rate-limit budgets are spent on every
re-verification attempt.

The order is deliberate:

1. Validate the session and CSRF token.
2. Re-verify the acting administrator and consume the two rate-limit budgets.
3. Validate action-specific input.
4. Create the account or generate and store the invite.

A missing, wrong or rate-limited administrator password creates nothing. Each
attempt is logged with the acting administrator, the action and its outcome;
passwords, invite codes and hashes are never logged. Random-number and store
errors are handled rather than discarded, so a failed operation cannot render
an empty or unusable secret as though it succeeded.

Revoking an invite remains session-and-CSRF protected without password
re-verification. Revocation reduces authority and cannot establish access, so
pricing it like credential creation would add friction without protecting the
boundary this decision addresses.

## Consequences

- A stock KOReader can no longer create a liseur-sync account by itself. The
  account must exist first, then the device is enrolled with a pairing code.
- No stored kosync verifier is derived from an account password. A database
  compromise therefore cannot use the legacy adapter to bypass Argon2id's
  password-guessing cost.
- An administrator performs one additional password check when creating an
  account or invite. This matches password reset and cross-user credential
  creation, and makes the panel's re-verification boundary coherent.
- A stolen administrator session and its CSRF token can still perform ordinary
  session-authorized work, but cannot turn that session into a new durable
  login. Knowledge of the administrator's password is a different compromise
  and is outside the protection re-verification can provide.
- No route, database schema or native API shape changes. The kosync pairing
  route keeps its wire shape; only the unsafe optional meaning is removed.

## Implementation phases

**Phase 1 — Pairing-only kosync.** Remove the open-registration branch and its
configuration surface. Update DESIGN.md, SECURITY.md, the example configuration
and deployment documentation to say that account registration is invite-only
and kosync creates device credentials only. Extend adapter and configuration
tests to pin the removal.

**Phase 2 — Close the administration gap.** Add administrator-password fields
to the create-account and create-invite forms, put both handlers behind
`reauth`, handle secret-generation and store failures, and extend the audit
log. Add focused web tests for CSRF, missing and wrong passwords, both limiter
budgets, successful creation and absence of partial state.

Both phases are implemented.

## Acceptance criteria

- No code path derives a kosync device credential from an account password.
- `POST /adapter/kosync/users/create` with a username and account password is
  refused even when the username does not exist. A valid pairing code still
  creates exactly one device for its existing account and remains single-use.
- `open_registration` in TOML is an unknown-key startup error, and
  `LISEUR_OPEN_REGISTRATION` cannot enable any behavior.
- Creating an account or invite without the acting administrator's correct
  password creates no user, invite or secret and records the refused action.
- Account- and client-address re-verification budgets are enforced
  independently on both creation paths. A rate-limited request performs no
  action even when it later carries the correct password.
- Correct CSRF and administrator credentials still create an account or
  single-use invite, and the returned secret is shown exactly once and never
  logged.
- Revoking an invite continues to require an administrator session and CSRF
  token, but not password re-verification.
- The full race-enabled suite passes against SQLite and PostgreSQL, and the
  secure-transport, route-scope and tenant-isolation matrices remain green.
