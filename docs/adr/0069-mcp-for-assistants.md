# ADR-0069: A read-only MCP endpoint for assistants

- **Status:** Accepted
- **Date:** 2026-11-21
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0029](0029-a-folder-somebody-can-read.md),
  [ADR-0059](0059-deriving-a-position-across-protocols.md)

## Context

Readers want to ask an assistant about their own reading. They want to
know who a character is, what happened in the last chapter they read,
what they highlighted in a book, or how much they read this month. ChatGPT and
other assistants reach outside data through the Model Context Protocol
(MCP), and a remote MCP server is authorized with OAuth 2.1.

The easy versions of this are unsafe. A static API token pasted into an
assistant never expires and cannot be scoped to what the assistant
needs. An open Dynamic Client Registration endpoint lets anyone on the
internet mint a client and phish a reader with a consent screen naming
it. A server that builds its issuer from the `Host` header can be
tricked into minting tokens for someone else's audience. A tool that
quotes a book hands untrusted text to a model that can act on it.

## Decision

The server exposes an MCP endpoint at `/mcp` behind a built-in OAuth
authorization server, off by default.

The endpoint is stateless Streamable HTTP. Each `POST /mcp` carries one
JSON-RPC request and gets one JSON response. There are no sessions, no
server-initiated stream and no batches. `GET` and `DELETE` answer 405. A
request with an `Origin` header must come from the configured public
origin. With `[mcp] enabled = false` the endpoint, the token endpoint,
the consent page and both metadata documents do not exist (404).

Authorization is OAuth 2.1, authorization code with PKCE `S256` only.

- The issuer and the protected resource come from `[mcp] public_url`,
  never from a request. The resource is `<public_url>/mcp`. A code and
  every token are bound to it (RFC 8707), and the authorization response
  carries `iss` (RFC 9207).
- No client registers itself. A client is either an allowlisted Client
  ID Metadata Document URL (ChatGPT's
  `https://chatgpt.com/oauth/client.json` is one switch on the admin
  page), or a client an administrator registered by hand with exact
  redirect URIs, public or with a secret shown once. No
  `registration_endpoint` is advertised.
- A metadata document is fetched over HTTPS only. Redirects are refused,
  and so is any address that is not public, checked at dial time. The
  fetch is bounded to 64 KiB and 5 s and cached for 15 minutes. Its
  `client_id` must equal its URL. Redirect URIs match exactly. Metadata
  clients must permit the public `none` token authentication method;
  signed client assertions are not supported.
- Codes are single use and live 60 s. Replaying one revokes the grant
  it produced. Access tokens live an hour. Refresh tokens rotate on
  every use, and reusing a spent one revokes the whole grant. Every
  secret is stored as its SHA-256 hash.
- These tokens are a separate class with their own prefix. `/mcp`
  accepts nothing else, and no other route accepts them.
- Consent happens in the web UI, behind the cookie login and its CSRF
  token. The page names the client, the host it was verified at and the
  host the browser will return to. The reader can untick any scope.
  The page cannot be framed, and its CSP allows the form to reach only
  this server and the client's redirect origin.
- A reader sees and disconnects their own connected apps. An
  administrator sees and cuts every connection, and removing a client
  removes its grants. A disabled account's tokens stop working on the
  next request.

Scopes are read-only and specific to MCP: `mcp:library`,
`mcp:reading`, `mcp:insights`, `mcp:vocabulary` and `mcp:text`.
`tools/list` shows only the tools the grant allows, and `tools/call`
refuses the rest. No tool writes.

Every tool runs the existing store reads with the grant's user ID, so
reading state stays per user and the catalog stays bounded by folder
grants. No tool output names a filesystem path, a download link or a
credential.

Book text (`mcp:text`) is the whole of any book the reader may open.
The server keeps no spoiler guard: the assistant can read any chapter.
`find_mentions` can limit a search to a position, which may be estimated;
`read_at` returns a window around a position that can include later text.
Text is extracted from the same EPUB index
the web reader uses, as plain text per reading-order resource, and
cached per content digest. A place in the book is found in this order:
the reader's Readium locator taken in this file, then a KOReader XPath
for this file (ADR-0059), then the whole-book progression. Each
result says which of these located it.

Book text is bounded per call (`text_bytes_per_call`, 16 KiB) and per
grant per hour (`text_bytes_per_hour`, 1 MiB). That is enough to follow
a character through a novel and slow enough that copying a library out
is impractical. Every text result carries a notice that the quoted
text is material to read, never instructions. Tool descriptions are
static server strings, and nothing a book or folder says reaches them.

The server runs no model. It finds and returns text; the assistant
builds character notes, summaries and timelines from it, and the server
stores nothing the assistant derives.

## Consequences

- ChatGPT connects with one switch on the admin page and a URL. A custom
  client needs an administrator to register it.
- Migrations add four tables (`oauth_clients`, `oauth_grants`,
  `oauth_codes`, `oauth_tokens`) and assign a registration identifier
  to each client. Existing connections remain available.
- The hourly text budget is in memory per process. A restart resets it.
  So does a second replica, which keeps its own.
- An assistant with `mcp:text` can read any granted book. A reader who
  does not want that unticks the scope at consent.

## Acceptance criteria

- The shared store suite covers clients, single-use codes, grants,
  rotation and reuse revocation, revocation by user, disabled accounts
  and cascade on client removal, on SQLite and PostgreSQL.
- `internal/oauth` tests cover PKCE, code replay, redirect and resource
  mismatches, confidential clients, metadata documents and their fetch
  limits.
- `internal/api` tests run the code flow through the real token
  endpoint. They cover the 404 when disabled and the token class
  separation in both directions. They cover tools following scopes,
  folder grants, no path in output, and positions read through a
  Readium locator, a KOReader XPath and an annotation. They also cover
  the per-call cap and the hourly budget.
- `internal/webui` tests cover consent (login and return, CSRF, scope
  narrowing, deny), the reader's and the administrator's pages, and the
  HTTPS rule on every new route.
