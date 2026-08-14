// The reader page's controller: open the book with foliate-js, put it
// on screen, and keep the reading position in step with every other
// Liseur client.
//
// The rendering engine is vendored (ADR-0007, renderer revised by
// ADR-0012). foliate-js paginates with CSS multi-column inside the
// frame's own viewport, so there is nothing for this file to measure
// and no sizing hook to get wrong — the failure mode that ended both
// the hand-written renderer and epub.js.
//
// Sync is unchanged and deliberately so: this file talks to the same
// /v1 routes Android and desktop use, with the short-lived token from
// POST /ui/reader/token. It gets no special treatment from the server.

import './vendor/foliate/view.js';

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
  // the user might paste to somebody. It stays in this module, which
  // is where a credential belongs.
  history.replaceState(null, '', location.pathname + location.search);
}
const stage = document.getElementById('reader-view');
const status = document.getElementById('reader-status');
const progressBar = document.getElementById('reader-progress-bar');
const progressText = document.getElementById('reader-progress-text');
const chapterText = document.getElementById('reader-chapter');
const titleText = document.getElementById('reader-title-text');

let view = null;
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
// drifted. foliate-js keeps a title either as a string or as a
// language map, and either way one string comes out.
function bookTitle() {
  const raw = view && view.book && view.book.metadata && view.book.metadata.title;
  if (!raw) return '';
  if (typeof raw === 'string') return raw;
  const values = Object.values(raw);
  return values.length ? String(values[0]) : '';
}

// locatorFor builds the Readium Locator the sync protocol carries. The
// server stores it verbatim and never reads it, so the shape is a
// promise to the other clients rather than to the server.
//
// The CFI goes in `fragments`, which is where Readium puts a format's
// own pointer, and `totalProgression` is beside it because that is the
// one field every client can act on: a phone that has never heard of a
// CFI still opens in the right place.
//
// `location` here is what foliate-js hands the relocate event: a total
// fraction, the section index, and a CFI. The engine estimates the
// fraction from section sizes it already knows, so there is no separate
// "generate locations" pass and no moment when progress is unknown.
function locatorFor(location) {
  const section = location.section || {};
  const index = typeof section.current === 'number' ? section.current : 0;
  const sections = (view.book && view.book.sections) || [];
  return {
    href: (sections[index] && sections[index].id) || '',
    type: 'application/xhtml+xml',
    title: bookTitle(),
    locations: {
      fragments: location.cfi ? [location.cfi] : [],
      progression: sectionProgression(location),
      totalProgression: typeof location.fraction === 'number' ? location.fraction : 0,
      position: index + 1,
    },
  };
}

// sectionProgression recovers the fraction within the current section
// from the total fraction and the section boundaries, because Readium's
// `progression` is within-resource and foliate-js reports the total.
function sectionProgression(location) {
  const fractions = view.getSectionFractions ? view.getSectionFractions() : [];
  const section = location.section || {};
  const index = typeof section.current === 'number' ? section.current : 0;
  const lo = fractions[index];
  const hi = fractions[index + 1];
  const total = typeof location.fraction === 'number' ? location.fraction : 0;
  if (typeof lo !== 'number' || typeof hi !== 'number' || hi <= lo) return 0;
  return Math.max(0, Math.min(1, (total - lo) / (hi - lo)));
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
      // keepalive lets the final flush outlive the page: without it a
      // position pushed from beforeunload is cancelled mid-flight.
      keepalive: true,
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
// here. The return value is whatever view.init can resolve: a CFI
// string, an href, or a { fraction } object.
function startFrom(op) {
  if (!op) return null;
  const cfi = cfiOf(op);
  if (cfi) return cfi;
  const locations = (op.locator && op.locator.locations) || {};
  const fraction = typeof locations.totalProgression === 'number'
    ? locations.totalProgression : op.progression;
  if (typeof fraction === 'number' && fraction > 0) {
    return { fraction: Math.min(0.999, fraction) };
  }
  if (op.locator && op.locator.href) return op.locator.href;
  return null;
}

// ------------------------------------------------------ appearance

// Reader appearance, Komga-style: theme, font, size, spacing, layout.
// All of it is a browser preference — stored in localStorage, applied
// through the engine's user stylesheet and layout attributes, never
// sent to the server. The default for everything is "what the
// publisher said": a fresh reader renders the book exactly as shipped,
// and every override below exists only once the user asks for it.
const SETTINGS_KEY = 'liseur.reader.settings';
const SETTINGS_DEFAULTS = Object.freeze({
  theme: 'original',
  font: 'publisher',
  size: 100,
  spacing: '0',
  justify: false,
  hyphenate: false,
  flow: 'paginated',
  columns: 'auto',
  margin: 'normal',
});
const THEMES = {
  light: { bg: '#ffffff', fg: '#1b1b1f', link: '#1a63c4', scheme: 'light' },
  sepia: { bg: '#f6ecd9', fg: '#5b4636', link: '#8a5a2b', scheme: 'light' },
  dark: { bg: '#202124', fg: '#cfcfd4', link: '#8ab4f8', scheme: 'dark' },
  black: { bg: '#000000', fg: '#ababae', link: '#7aa2d8', scheme: 'dark' },
};
const FONTS = {
  serif: 'Georgia, "Times New Roman", "Liberation Serif", serif',
  sans: 'system-ui, -apple-system, "Segoe UI", Roboto, "Liberation Sans", sans-serif',
};
const MARGINS = { narrow: '16px', normal: '48px', wide: '72px' };
// The column gap follows the margin choice: "narrow" should mean the
// text gets the window, not just that the outer edge moved.
const GAPS = { narrow: '3%', normal: '7%', wide: '11%' };

let settings = loadSettings();

function loadSettings() {
  try {
    const stored = JSON.parse(localStorage.getItem(SETTINGS_KEY));
    return { ...SETTINGS_DEFAULTS, ...(stored || {}) };
  } catch (err) {
    return { ...SETTINGS_DEFAULTS };
  }
}

function saveSettings() {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
  } catch (err) {
    /* private browsing: the settings last as long as the page */
  }
}

// chapterCSS builds the user stylesheet the engine injects after the
// publication's own. Only what differs from the defaults appears, so
// with everything at "publisher" the sheet is one safety rule and the
// book's own design is untouched — which the browser test relies on
// when it reads the publication's colour back out of the page.
function chapterCSS(s) {
  const rules = ['pre { white-space: pre-wrap !important; }'];
  const theme = THEMES[s.theme];
  if (theme) {
    rules.push(
      `html { color-scheme: ${theme.scheme}; }`,
      `html, body { background: ${theme.bg} !important; color: ${theme.fg} !important; }`,
      `body * { background-color: transparent !important; color: ${theme.fg} !important; }`,
      `a:any-link { color: ${theme.link} !important; }`,
    );
  }
  if (FONTS[s.font]) {
    rules.push(`body, body :not(pre):not(code):not(kbd):not(samp) { font-family: ${FONTS[s.font]} !important; }`);
  }
  const size = Number(s.size);
  if (size && size !== 100) {
    // Both rules matter: scaling the root handles publications that
    // size text in rem/em, and forcing the body back to 1rem overrides
    // the ones that pin it in px — which would otherwise ignore the
    // slider entirely.
    rules.push(
      `html { font-size: ${size}% !important; }`,
      'body { font-size: 1rem !important; }',
    );
  }
  const spacing = Number(s.spacing);
  if (spacing) {
    rules.push(`p, li, blockquote, dd { line-height: ${spacing} !important; }`);
  }
  if (s.justify) {
    rules.push(
      'p, li, blockquote, dd { text-align: justify; }',
      '[align="left"] { text-align: left; } [align="right"] { text-align: right; }',
      '[align="center"] { text-align: center; }',
    );
  }
  if (s.hyphenate) {
    rules.push('p, li, blockquote, dd { -webkit-hyphens: auto; hyphens: auto; }');
  }
  return rules.join('\n');
}

function applySettings() {
  document.body.dataset.readerTheme = settings.theme;
  if (!view || !view.renderer) return;
  const renderer = view.renderer;
  renderer.setAttribute('flow', settings.flow === 'scrolled' ? 'scrolled' : 'paginated');
  renderer.setAttribute('max-column-count', settings.columns === 'auto' ? '2' : settings.columns);
  renderer.setAttribute('margin', MARGINS[settings.margin] || MARGINS.normal);
  renderer.setAttribute('gap', GAPS[settings.margin] || GAPS.normal);
  // The line-length cap grows with the type: a bigger font on a wide
  // window should mean a wider column with the same characters per
  // line, not the same 720px ribbon with more empty page around it.
  const scale = (Number(settings.size) || 100) / 100;
  renderer.setAttribute('max-inline-size', Math.round(720 * Math.max(1, scale)) + 'px');
  if (renderer.setStyles) renderer.setStyles(chapterCSS(settings));
}

const settingsPanel = document.getElementById('reader-settings');
const settingsForm = document.getElementById('reader-settings-form');
const sizeOut = document.getElementById('reader-size-out');

function syncSettingsForm() {
  if (!settingsForm) return;
  for (const field of settingsForm.elements) {
    if (!field.name || !(field.name in settings)) continue;
    if (field.type === 'radio') field.checked = field.value === String(settings[field.name]);
    else if (field.type === 'checkbox') field.checked = !!settings[field.name];
    else field.value = String(settings[field.name]);
  }
  if (sizeOut) sizeOut.textContent = settings.size + '%';
}

function readSettingsForm() {
  const data = new FormData(settingsForm);
  settings = {
    theme: String(data.get('theme') || SETTINGS_DEFAULTS.theme),
    font: String(data.get('font') || SETTINGS_DEFAULTS.font),
    size: Number(data.get('size')) || SETTINGS_DEFAULTS.size,
    spacing: String(data.get('spacing') || SETTINGS_DEFAULTS.spacing),
    justify: data.has('justify'),
    hyphenate: data.has('hyphenate'),
    flow: String(data.get('flow') || SETTINGS_DEFAULTS.flow),
    columns: String(data.get('columns') || SETTINGS_DEFAULTS.columns),
    margin: String(data.get('margin') || SETTINGS_DEFAULTS.margin),
  };
  if (sizeOut) sizeOut.textContent = settings.size + '%';
}

if (settingsForm) {
  syncSettingsForm();
  settingsForm.addEventListener('input', () => {
    readSettingsForm();
    saveSettings();
    applySettings();
  });
  document.getElementById('reader-settings-reset').addEventListener('click', () => {
    settings = { ...SETTINGS_DEFAULTS };
    syncSettingsForm();
    saveSettings();
    applySettings();
  });
  document.addEventListener('click', (e) => {
    if (settingsPanel.open && !settingsPanel.contains(e.target)) {
      settingsPanel.open = false;
    }
  });
}
// The stage should match the chosen theme before the book arrives, so
// a dark reader does not open with a white flash.
applySettings();

// ---------------------------------------------------------- render

function paint(location) {
  here = location;
  const fraction = typeof location.fraction === 'number' ? location.fraction : 0;
  progressBar.style.width = (fraction * 100).toFixed(1) + '%';
  progressText.textContent = Math.round(fraction * 100) + '%';
  const section = location.section || {};
  if (typeof section.current === 'number' && section.total) {
    chapterText.textContent =
      'Chapter ' + (section.current + 1) + ' of ' + section.total;
  }
}

// stripScripts removes the publication's own code from every resource
// before the engine turns it into a blob URL. The page CSP is what
// actually stops a book's script from running — a blob document
// inherits this page's policy, and script-src has no hole in it — so
// this is the second fence, and it also keeps the console clear of the
// browser announcing refusals.
function stripScripts(book) {
  if (!book || !book.transformTarget) return;
  book.transformTarget.addEventListener('data', (event) => {
    const detail = event.detail;
    if (/\b(x-)?(javascript|ecmascript)\b/.test(detail.type || '')) {
      detail.data = '';
      return;
    }
    if (/\b(xhtml\+xml|html)\b/.test(detail.type || '')) {
      detail.data = Promise.resolve(detail.data).then((data) =>
        typeof data === 'string'
          ? data.replace(/<script\b[\s\S]*?(?:<\/script\s*>|\/>)/gi, '')
          : data);
    }
  });
}

function turn(direction) {
  if (!view) return undefined;
  return direction > 0 ? view.goRight() : view.goLeft();
}

document.getElementById('reader-next').addEventListener('click', () => turn(1));
document.getElementById('reader-prev').addEventListener('click', () => turn(-1));
document.addEventListener('keydown', (e) => {
  // Arrow keys inside the settings panel adjust its controls, not the
  // book; Escape puts the panel away from the keyboard.
  if (settingsPanel && settingsPanel.contains(e.target)) {
    if (e.key === 'Escape') settingsPanel.open = false;
    return;
  }
  if (e.key === 'Escape' && settingsPanel && settingsPanel.open) {
    settingsPanel.open = false;
    return;
  }
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
    const blob = await resp.blob();

    say('Opening…');
    view = document.createElement('foliate-view');
    stage.append(view);
    view.addEventListener('relocate', (e) => {
      paint(e.detail);
      schedulePush();
    });
    await view.open(new File([blob], 'book.epub', { type: 'application/epub+zip' }));
    stripScripts(view.book);
    // The renderer exists once the book is open; settings applied here
    // shape the very first page rather than repainting it.
    applySettings();

    const title = bookTitle();
    if (title) {
      titleText.textContent = title;
      document.title = title + ' · liseur-sync';
    }

    // Sync is best-effort: a book still opens on a server that has
    // lost its work mapping, it just opens at the beginning.
    let op = null;
    try {
      workID = await resolveWork();
      op = await lastPosition();
    } catch (err) {
      /* read on without sync */
    }

    await view.init({ lastLocation: startFrom(op) });
    say('');
  } catch (err) {
    say((err && err.message) || 'this book could not be opened', true);
  }
})();
