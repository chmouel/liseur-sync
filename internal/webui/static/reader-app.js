// The reader page's controller: open the book with epub.js, put it on
// screen, and keep the reading position in step with every other Liseur
// client.
//
// The rendering engine is vendored (ADR-0007). Pagination inside a
// chapter is the part a reader lives or dies by, and a hand-written one
// got it wrong in a way that stopped the book two pages in. epub.js is
// what other self-hosted readers use, for the same reason.
//
// Sync is unchanged and deliberately so: this file talks to the same
// /v1 routes Android and desktop use, with the short-lived token from
// POST /ui/reader/token. It gets no special treatment from the server.

'use strict';

(function () {
  const el = document.getElementById('reader-config');
  // Every URL is relative, computed server-side, so the reader keeps
  // working when the UI is served under a stripped proxy subpath.
  const cfg = {
    bookID: el.dataset.book,
    csrf: el.dataset.csrf,
    tokenURL: el.dataset.tokenUrl,
    downloadURL: el.dataset.downloadUrl,
    apiBase: el.dataset.apiBase,
    detached: el.dataset.detached === '1',
    handed: null,
  };

  // On the separate reader origin (ADR-0007 phase 3) there is no session
  // and no CSRF token, because there is no cookie on this hostname at
  // all. The credential was handed over in the URL fragment, which the
  // browser sent to nobody; the addresses it works against were in the
  // query, checked by the server, and are already in the page.
  if (cfg.detached) {
    cfg.handed = new URLSearchParams(location.hash.slice(1)).get('t');
    // Out of the address bar, out of the history entry, out of anything
    // the user might paste to somebody. It stays in this closure, which
    // is where a credential belongs.
    history.replaceState(null, '', location.pathname + location.search);
  }
  const stage = document.getElementById('reader-view');
  const status = document.getElementById('reader-status');
  const progressBar = document.getElementById('reader-progress-bar');
  const progressText = document.getElementById('reader-progress-text');
  const chapterText = document.getElementById('reader-chapter');
  const titleText = document.getElementById('reader-title-text');

  let book = null;
  let rendition = null;
  let workID = null;
  let token = null;
  let tokenExpiry = 0;
  let pending = null;
  let here = null;

  function say(message, isError) {
    status.textContent = message;
    status.classList.toggle('problem', !!isError);
    status.hidden = !message;
  }

  // ------------------------------------------------------------ auth

  // credential keeps a live reader token. It re-mints rather than
  // refreshes, because there is no refresh token to steal: the session
  // cookie is the thing that proves the browser may ask.
  async function credential() {
    if (token && Date.now() < tokenExpiry - 60000) return token;
    if (cfg.detached) {
      // Nothing on this origin can prove who the reader is, so there is
      // no re-minting here: the token was handed over once and when it
      // is gone the reader has to be opened from the library again. That
      // is the price of a hostname with no session on it.
      if (!cfg.handed) throw new Error('this reading session has expired; open the book from your library again');
      token = cfg.handed;
      // Once it has been refused there is no second one to ask for, so
      // the next attempt reports that plainly instead of looping.
      cfg.handed = null;
      tokenExpiry = Date.now() + 86400000;
      return token;
    }
    const resp = await fetch(cfg.tokenURL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ csrf: cfg.csrf }),
    });
    if (!resp.ok) throw new Error('could not obtain a reading credential');
    const got = await resp.json();
    token = got.token;
    tokenExpiry = Date.parse(got.expires_at) || (Date.now() + 3600000);
    return token;
  }

  // api retries once on 401, which is the whole of the token lifecycle a
  // client has to implement: expired means ask again, not sign in again.
  async function api(path, options = {}, retry = true) {
    const secret = await credential();
    const resp = await fetch(cfg.apiBase + path, {
      ...options,
      headers: {
        ...(options.headers || {}),
        Authorization: 'Bearer ' + secret,
      },
    });
    if (resp.status === 401 && retry) {
      token = null;
      return api(path, options, false);
    }
    return resp;
  }

  // ------------------------------------------------------------ sync

  async function resolveWork() {
    const resp = await api('v1/books/' + encodeURIComponent(cfg.bookID) + '/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    if (!resp.ok) return null;
    return (await resp.json()).work_id || null;
  }

  async function lastPosition() {
    if (!workID) return null;
    const resp = await api('v1/works/' + encodeURIComponent(workID) + '/positions?limit=1');
    if (!resp.ok) return null;
    const ops = (await resp.json()).ops || [];
    return ops.length ? ops[0] : null;
  }

  // bookTitle is read from the package document rather than from the
  // catalog, so the page says what the file says even when the two have
  // drifted.
  function bookTitle() {
    return (book && book.packaging && book.packaging.metadata &&
      book.packaging.metadata.title) || '';
  }

  // locatorFor builds the Readium Locator the sync protocol carries. The
  // server stores it verbatim and never reads it, so the shape is a
  // promise to the other clients rather than to the server.
  //
  // The CFI goes in `fragments`, which is where Readium puts a format's
  // own pointer, and `totalProgression` is beside it because that is the
  // one field every client can act on: a phone that has never heard of a
  // CFI still opens in the right place.
  function locatorFor(location) {
    const start = location.start || {};
    return {
      href: start.href || '',
      type: 'application/xhtml+xml',
      title: bookTitle(),
      locations: {
        fragments: start.cfi ? [start.cfi] : [],
        progression: typeof start.percentage === 'number' ? start.percentage : 0,
        totalProgression: totalProgression(location),
        position: typeof start.index === 'number' ? start.index + 1 : undefined,
      },
    };
  }

  // totalProgression prefers the percentage epub.js computes once it has
  // measured the book, and falls back to the spine position until then.
  // Reporting the fallback is better than reporting nothing: a rough
  // fraction still resumes on another device, and measuring a large book
  // takes a moment.
  function totalProgression(location) {
    const start = location.start || {};
    if (book.locations && book.locations.length() && start.cfi) {
      const measured = book.locations.percentageFromCfi(start.cfi);
      if (typeof measured === 'number' && !isNaN(measured)) return measured;
    }
    const count = (book.spine && book.spine.length) || 1;
    const index = typeof start.index === 'number' ? start.index : 0;
    const within = typeof start.percentage === 'number' ? start.percentage : 0;
    return Math.min(1, (index + within) / count);
  }

  // push records where we are. Failure is deliberately quiet: losing a
  // position update is a smaller harm than an error banner over the page
  // every time a laptop lid closes, and the next page turn retries.
  async function push() {
    if (!workID || !here) return;
    const locator = locatorFor(here);
    try {
      await api('v1/ops', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ops: [{
            op_id: opID(),
            work_id: workID,
            client_ts: new Date().toISOString(),
            progression: locator.locations.totalProgression,
            locator: locator,
          }],
        }),
      });
    } catch (err) {
      /* offline: the next page turn will carry the position instead */
    }
  }

  // opID is a v4 UUID. crypto.randomUUID exists only in a secure
  // context, and the reader origin may be plain HTTP on a LAN, so the
  // bytes are drawn directly — getRandomValues has no such restriction
  // — rather than letting sync fail quietly where TLS is absent.
  function opID() {
    if (crypto.randomUUID) return crypto.randomUUID();
    const b = crypto.getRandomValues(new Uint8Array(16));
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const hex = [...b].map((n) => n.toString(16).padStart(2, '0')).join('');
    return [hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16),
      hex.slice(16, 20), hex.slice(20)].join('-');
  }

  function schedulePush() {
    clearTimeout(pending);
    pending = setTimeout(push, 1500);
  }

  function cfiOf(op) {
    const fragments = (op && op.locator && op.locator.locations &&
      op.locator.locations.fragments) || [];
    for (const fragment of fragments) {
      if (typeof fragment === 'string' && fragment.indexOf('epubcfi(') === 0) {
        return fragment;
      }
    }
    return null;
  }

  // startFrom decides where to open. It prefers what the writing client
  // actually said — a CFI from this reader, or the resource another one
  // named — and falls back to the fraction every client agrees on, which
  // is why a book started on a phone opens in roughly the right place
  // here.
  function startFrom(op) {
    if (!op) return undefined;
    const cfi = cfiOf(op);
    if (cfi) return cfi;
    const locations = (op.locator && op.locator.locations) || {};
    const fraction = typeof locations.totalProgression === 'number'
      ? locations.totalProgression : op.progression;
    if (typeof fraction === 'number' && fraction > 0 && book.locations.length()) {
      return book.locations.cfiFromPercentage(Math.min(0.999, fraction));
    }
    if (op.locator && op.locator.href) return op.locator.href;
    return undefined;
  }

  // ---------------------------------------------------------- render

  function paint(location) {
    here = location;
    const fraction = totalProgression(location);
    progressBar.style.width = (fraction * 100).toFixed(1) + '%';
    progressText.textContent = Math.round(fraction * 100) + '%';
    const start = location.start || {};
    if (typeof start.index === 'number' && book.spine && book.spine.length) {
      chapterText.textContent =
        'Chapter ' + (start.index + 1) + ' of ' + book.spine.length;
    }
  }

  function turn(direction) {
    if (!rendition) return undefined;
    return direction > 0 ? rendition.next() : rendition.prev();
  }

  document.getElementById('reader-next').addEventListener('click', () => turn(1));
  document.getElementById('reader-prev').addEventListener('click', () => turn(-1));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowRight' || e.key === 'PageDown') turn(1);
    if (e.key === 'ArrowLeft' || e.key === 'PageUp') turn(-1);
  });
  window.addEventListener('beforeunload', () => {
    clearTimeout(pending);
    push();
  });

  // ------------------------------------------------------------ open

  (async function start() {
    try {
      say('Fetching the book…');
      // Same-origin the cookie is enough and is what the UI download
      // route expects; detached there is no cookie, so the book comes
      // from the API with the bearer token like any other client.
      const resp = cfg.detached
        ? await api('v1/books/' + encodeURIComponent(cfg.bookID) + '/download')
        : await fetch(cfg.downloadURL, { credentials: 'same-origin' });
      if (!resp.ok) throw new Error('this book could not be downloaded');
      const buf = await resp.arrayBuffer();

      say('Opening…');
      book = ePub(buf);
      await book.ready;

      const title = bookTitle();
      if (title) {
        titleText.textContent = title;
        document.title = title + ' · liseur-sync';
      }

      rendition = book.renderTo(stage, {
        width: '100%',
        height: '100%',
        spread: 'none',
        flow: 'paginated',
        // The publication's own scripts never run. epub.js renders into
        // a same-origin iframe so that it can measure the document and
        // paginate it, and this is what keeps that from meaning the book
        // can act: with no allow-scripts there is nothing to execute.
        allowScriptedContent: false,
        allowPopups: false,
      });
      rendition.on('relocated', (location) => {
        paint(location);
        schedulePush();
      });

      // Sync is best-effort: a book still opens on a server that has
      // lost its work mapping, it just opens at the beginning.
      let op = null;
      try {
        workID = await resolveWork();
        op = await lastPosition();
      } catch (err) {
        /* read on without sync */
      }

      // Locations are what turn a fraction into a place. They are
      // generated before the first paint only when there is a fraction
      // to act on and no CFI, because measuring a large book takes a
      // moment and a blank screen is a worse first impression than a
      // percentage that arrives late.
      const needsMeasure = !!op && !cfiOf(op);
      if (needsMeasure) {
        try {
          await book.locations.generate(1024);
        } catch (err) {
          /* open at the beginning rather than not at all */
        }
      }

      await rendition.display(startFrom(op));
      say('');

      if (!needsMeasure) {
        book.locations.generate(1024).then(() => {
          if (here) paint(here);
        }).catch(() => { /* progress stays approximate */ });
      }
    } catch (err) {
      say(err.message || 'this book could not be opened', true);
    }
  })();
})();
