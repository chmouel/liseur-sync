# ADR-0050: Settings stay on the device

- **Status:** Accepted
- **Date:** 2026-10-05
- **Scope:** Now
- **Amends:** [DESIGN §5.6](../DESIGN.md#56-device-settings)

## Context

`GET`/`PUT /v1/me/settings` held one map of reader preferences per
account: typeface, theme, margins, highlight colours. Every device of
the account pulled it and pushed to it, and the server picked a winner
by the client's change time. The goal was that a second device would
not have to be set up from scratch.

In use the sharing was the problem. A phone and a tablet are read
differently: a margin, a font size or a theme picked for one screen is
rarely right on the other, and a change made on one moved the other the
next time it synced. The machinery needed to make last-writer-wins
behave across devices (client change stamps, a collector watching every
preference, a baseline per account, a clock cap) was large for a
feature that readers mostly wanted switched off.

## Decision

**Settings belong to the device that wrote them.** The server still
stores them, keyed by `(user_id, device_id, key)` in `device_settings`,
where the device is the authenticated token's. A device reads and writes
only its own rows; another device of the same account never sees them.
What the server keeps is a copy a device can restore from. That
happens when it signs in again keeping its `device_id`, or when its
local preferences were lost. It is never a channel between devices.

The wire stays the same: a map of `{value, updated_at}` with the
newer-wins upsert. `updated_at` now only orders one device's own
requests, so a late retry cannot undo a newer write. Both responses
gain `"scope": "device"`. A client that keeps settings local can then
tell this server from an older one whose map is the whole account's,
and adopt nothing from the older kind.

`ops.settings_max_per_account` keeps its name, because the config loader
refuses unknown keys and renaming it would break existing files. It now
caps each device.

Revoking a token does not delete the device's settings, for the same
reason it does not delete the device's ops and sessions. A device
signing in again may keep its `device_id`, and its settings are still
there when it does. That only works while the server still lists a
token for the device, because a token request naming a `device_id`
the server no longer knows is refused. So once housekeeping purges a
device's last token, it deletes that device's settings as well: nothing
could ever read them again. This also keeps the row count tied to
devices that exist, since the per-device cap alone would let a client
that keeps minting new devices pile up rows without bound.

**The migration drops the account-wide rows.** They name no device, so
there is no owner to hand them to. Each device uploads its own settings
again on its next sync. This changes what an existing deployment holds,
so `docs/deployment.md` says so.

## Consequences

- A new device starts with its own defaults; nothing is copied from the
  account's other devices.
- Liseur's
  [ADR-0041](https://github.com/chmouel/liseur/blob/main/docs/adr/0041-settings-stay-on-the-device.md)
  is the client side: it drops the change tracker and pushes what
  changed. It pulls only to restore a device that has no record of
  syncing with that account.
- A device that gets a new `device_id` (a fresh install signing in
  without its old one) starts with an empty server copy. That is the
  cost of keying by device, and it is what the change is for.
