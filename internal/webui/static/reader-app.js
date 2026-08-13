// The reader page's controller: fetch the book, put it on screen, and
// keep the reading position in step with every other Liseur client.
//
// It talks to the same /v1 routes Android and desktop use, with the
// short-lived token from POST /ui/reader/token (ADR-0007). It gets no
// special treatment from the server, which is the point: if this file
// can do it, so can anybody's client.

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
    nonce: el.dataset.nonce,
  };
  const view = document.getElementById('reader-frame');
  const status = document.getElementById('reader-status');
  const progressBar = document.getElementById('reader-progress-bar');
  const progressText = document.getElementById('reader-progress-text');
  const chapterText = document.getElementById('reader-chapter');

  let epub = null;
  let workID = null;
  let token = null;
  let tokenExpiry = 0;
  let place = { index: 0, fraction: 0 };
  let pending = null;
  let seeking = null;

  // The nonce comes from the server because the page's own policy
  // carries it too: a srcdoc document inherits the framing page's CSP
  // as well as declaring its own, so the chapter's script has to
  // satisfy both.
  const nonce = cfg.nonce;

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

  // api retries once on 401, which is the whole of the token lifecycle
  // a client has to implement: expired means ask again, not sign in
  // again.
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

  // push records where we are. Failure is deliberately quiet: losing a
  // position update is a smaller harm than an error banner over the
  // page every time a laptop lid closes, and the next scroll retries.
  async function push() {
    if (!workID || !epub) return;
    const locator = locatorFor(epub, place.index, place.fraction);
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
      /* offline: the next scroll will carry the position instead */
    }
  }

  function schedulePush() {
    clearTimeout(pending);
    pending = setTimeout(push, 1500);
  }

  // ---------------------------------------------------------- render

  async function show(index, fraction) {
    if (index < 0 || index >= epub.spine.length) return;
    place = { index: index, fraction: 0 };
    view.srcdoc = await epub.document(index, nonce);
    seeking = fraction > 0 ? fraction : null;
    paint();
  }

  function paint() {
    const total = (place.index + place.fraction) / epub.spine.length;
    progressBar.style.width = (total * 100).toFixed(1) + '%';
    progressText.textContent = Math.round(total * 100) + '%';
    chapterText.textContent = 'Chapter ' + (place.index + 1) + ' of ' + epub.spine.length;
  }

  window.addEventListener('message', (event) => {
    if (event.source !== view.contentWindow) return; // not our sandbox
    const msg = event.data || {};
    if (msg.type === 'ready' && seeking !== null) {
      view.contentWindow.postMessage({ type: 'seek', fraction: seeking }, '*');
      seeking = null;
      return;
    }
    if (msg.type === 'progress') {
      place.fraction = msg.fraction;
      paint();
      schedulePush();
    }
  });

  function turn(direction) {
    // At the end of a chapter, turning the page means the next one.
    if (direction > 0 && place.fraction >= 0.999) return show(place.index + 1, 0);
    if (direction < 0 && place.fraction <= 0.001) return show(place.index - 1, 0.999);
    view.contentWindow.postMessage({ type: 'page', direction: direction }, '*');
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
      epub = await Epub.open(buf);

      // Sync is best-effort: a book still opens on a server that has
      // lost its work mapping, it just opens at the beginning.
      try {
        workID = await resolveWork();
        const last = await lastPosition();
        if (last) {
          place = placeFromLocator(epub, last.locator, last.progression);
        }
      } catch (err) {
        /* read on without sync */
      }

      await show(place.index, place.fraction);
      say('');
    } catch (err) {
      say(err.message || 'this book could not be opened', true);
    }
  })();
})();
