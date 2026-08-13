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
  };
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
            op_id: crypto.randomUUID(),
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
      const resp = await fetch(cfg.downloadURL, { credentials: 'same-origin' });
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
