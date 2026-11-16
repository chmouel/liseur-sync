# ADR-0067: Additional explicit reading choices

- **Status:** Accepted
- **Date:** 2026-11-16
- **Depends on:** [ADR-0066](0066-explicit-reading-status.md)

Readers can explicitly choose `want_to_read`, `reading`, `paused`, or
`did_not_finish`, in addition to the choices in ADR-0066. The newest act
by server sequence wins over the position. These choices remain in effect
when a position arrives, including when a reader opens or continues a
paused or unfinished book. `automatic` withdraws the choice and resumes
position-based resolution. An act changes neither the locator, sessions,
nor furthest position; the existing unread action still resets position.

The web library and OPDS offer shelves for want to read, paused and did
not finish. Explicit reading belongs on the reading shelf even without a
position. These statuses are private to the reader like all status acts.

This extends the existing wire enum without a schema migration. Older
servers reject the new writes, and older clients may not understand the
new values. Deploy the server first and upgrade all clients for an account
before using these choices. Clients must retain unknown choices without
substituting a different status. Other providers remain limited to what
their own protocols can represent.
