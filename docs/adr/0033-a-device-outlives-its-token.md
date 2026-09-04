# ADR-0033: A device outlives its token

- **Status:** Accepted; implemented
- **Date:** 2026-09-04
- **Amends:** [ADR-0016](0016-token-self-introspection.md), whose phase 3
  (`account_id`) ships alongside this

## Context

Every token minted through `POST /v1/tokens` draws a fresh `device_id`.
The op log stamps that id on each op and session, and the store compares
it when a replayed `op_id` or `session_id` decides between `duplicate` and
`conflict`. The Android client derives those ids from a key of its own
that never changes, and relies on the server calling a byte-identical
replay a duplicate: that is how a push whose answer was lost is simply
sent again, with no table of requests in flight.

The two agree until the credential lapses. A user who reconnects mints a
new token, the phone gets a new `device_id`, and every record it stored
before the lapse but never saw acknowledged now replays under a
different device. An op comes back `conflict` and the book stays dirty; a
session comes back `409` and, with the old client, took its whole batch
down with it. Nothing was wrong on either side. They just disagreed about
what a device was.

The web reader had already met this: `MintReaderToken` inherits the
browser's previous device id so that a tab reopened after an hour is not
a new head in the op log.

## Decision

`POST /v1/tokens` accepts an optional `device_id`. The mint honours it
when some token of the same account — live, expired or revoked — already
carries that id; otherwise it answers `400` with `code: unknown_device`.
A deleted predecessor is gone on purpose and is not a source of identity.

The store keeps comparing `device_id` in `sameOp` and `sameSession`. Two
phones at the same revision of the same book are two positions and must
not silence each other; that rule is worth more than the convenience of
a device-free replay.

`GET /v1/token` gains `account_id`, so a client can tell a replaced
credential from a different account and keep its cursor and mirrors
through a reconnect.

## Consequences

A device id is a label in the op log, not a credential. Anyone who can
log in to an account can make a new token carry any device id that
account has ever had; that collapses two physical devices into one in
`GET /v1/heads` and in the statistics, and it crosses no account
boundary. Two live tokens may share one id.

Only a login-authenticated mint can ask for inheritance. A bearer secret
pasted from elsewhere carries whatever device id it was minted with, and
a client that reconnects that way still sees `conflict`/`409` for
records it never got an answer for. Those are then handled item by item
and never dropped, but they are not duplicates. A rotation authenticated
by the old device token itself would close that gap and is future work.

An older server ignores the field and mints a fresh id. A client detects
that by comparing the returned `device_id` with the one it sent.

## Acceptance criteria

- A revoked predecessor's id is inherited; an op pushed on the old token
  replays as `duplicate` on the new one; `GET /v1/heads` shows one device.
- An id no token of the account ever carried, and an id from another
  account, are refused with `400 unknown_device`.
- Two live tokens sharing one id both authenticate and both report it.
- `docs/openapi.yaml` and `docs/integrating.md` describe the field, the
  error, the old-server fallback and the label-not-credential rule.
