# ADR-0045: A prompt about the passage in front of you

**Status:** Accepted
**Scope:** Now

## Context

A reader who highlights a passage and wants to ask a chat assistant
about it has to retype where they are: which book, which chapter, how
far in — and *how far in* is the important one, because it is the only
way to say *do not spoil the rest*. All of it is already on the screen.
The footer computes the percentage, the page `n of m` and the chapter
label on every relocate; the selection is sitting in the iframe.

The thing being asked for is not an integration. Nothing needs to be
called, no key needs storing, and the server has no business knowing
which assistant a reader prefers or what they ask it. What is missing is
one paste.

## Decision

**The reader can copy a filled-in prompt about the passage it has
selected, from text the account wrote itself, and the server never
interprets that text.**

- **The template lives on the account**, in one new `users` column,
  edited on the settings page. It is written once on a keyboard and used
  on a phone. The alternative — `localStorage` on each device — would
  make the feature something a reader sets up again on every browser
  they read in, which is most of them.
- **It is empty by default, and an empty template means no button.**
  That is the whole of the opt-in: there is no checkbox to keep in sync
  with a text box that would then be either ignored or secretly
  authoritative. A reader who has nothing to say gets a reader that
  looks exactly as it does today.
- **No selection, no button.** The prompt is *about a passage*; without
  one there is nothing to copy. The button is also absent for the same
  reason it is absent with no template — the bar is quiet until there is
  something to press.
- **`{placeholders}`, substituted in the browser.** `{title}`,
  `{author}`, `{series}`, `{chapter}`, `{page}`, `{pages}`, `{percent}`
  and `{text}`. A known placeholder with nothing behind it becomes an
  empty string, never the word `undefined`: a book with no series is not
  a book with a hole in the sentence. An unrecognized `{word}` is left
  exactly as written, so a reader's own braces survive their prose.
- **The server stores a string and nothing else.** It is capped at 4000
  characters at the handler edge — a long refusal on the settings page,
  never a truncation and never a 5xx — and it is never parsed, rendered
  as markup, or sent anywhere.

### Three ways in, because the reader runs in three places

The same-origin reader page knows the account, so it carries the
template as a data attribute and asks for nothing. The detached reader
origin (ADR-0007) has no session and no user, so it reads
**`GET /v1/me`** once the bearer token is in hand — a new route under
`library-read`, the scope a browser reader already carries, answering
for the token's own account and never a path parameter. The offline
reader has no network at all, so every successful read of the template
writes it to `localStorage` under the account's id, and the offline
reader uses that copy.

`GET /v1/me` is deliberately about the *account* where `GET /v1/token`
is about the *credential*. It carries the timezone too, which a client
drawing its own day boundaries has wanted for a while.

### Copying

`navigator.clipboard.writeText` is absent on a plain-HTTP LAN origin and
can be refused anywhere, so a failure opens a dialog with the prompt in
a textarea, already selected. A reader on `http://nas.local:8080` gets
the feature; they just press one more key.

## Consequences

- One column, one route, one static module and one button. No reading
  state moves, no op is written, and nothing about sync changes.
- The migration adds a defaulted column, so an existing deployment reads
  exactly what it read before; `docs/deployment.md` needs no warning
  (the distinction issue #13 taught us to make).
- The template is per account, not per book or per client, so a reader
  who wants a different prompt for non-fiction rewrites the one they
  have. That is the trade for not building a prompt library.
- Substitution happens in the browser, so the values are the ones the
  reader is actually looking at — including a page number that exists
  only because this reader paginated the chapter. Nothing is fabricated:
  a book with no page table yields an empty `{page}`, in keeping with
  the rule against inventing pagination.
- A client on another platform can offer the same button from the same
  text; `GET /v1/me` is not reader-specific.

## Implementation and acceptance

- [x] `reader_prompt_template` appended as a migration to both backends,
      on `store.User`, written through a `store.UserSettings` struct,
      covered in the shared store suite (default empty, round-trip,
      cleared by a settings write that omits it).
- [x] A textarea, the placeholder legend and a "Use the example prompt"
      button on the settings page, with the length refusal flashed
      rather than truncated, and its one line of script in `ui.js`
      because `/ui` is `script-src 'self'`.
- [x] `GET /v1/me` in the scope table, tested for isolation between two
      accounts, `401` unauthenticated and `403` without `library-read`.
- [x] `reader-prompt.js` as a pure module, unit-tested under
      `node --test`, including the unknown-placeholder and
      missing-value rules.
- [x] Reader-page coverage that the account's template is rendered for
      its owner, never for another account, and never into the offline
      shell.
- [x] Browser coverage of the button's appearance and its fallback
      dialog, run opt-in with `LISEUR_CHROME`.
