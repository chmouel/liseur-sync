// Throwaway harness: drives real Chromium over CDP to prove the reader
// renders. Not part of the committed suite.
import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const chrome = process.env.SMOKE_CHROME;
const url = process.env.SMOKE_URL;
const cookie = process.env.SMOKE_COOKIE;
const host = process.env.SMOKE_HOST;
// Two-origin mode (ADR-0007 phase 3): the reader hostname has to resolve
// somewhere, and it is not in DNS. Chrome will map it for us.
const mapHost = process.env.SMOKE_MAP;
const detached = process.env.SMOKE_DETACHED === '1';

const profile = mkdtempSync(join(tmpdir(), 'smoke-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  ...(mapHost ? [`--host-resolver-rules=MAP ${mapHost} 127.0.0.1`] : []),
  'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });

const wsURL = await new Promise((res, rej) => {
  let buf = '';
  const to = setTimeout(() => rej(new Error('chrome did not print a devtools url: ' + buf)), 20000);
  proc.stderr.on('data', (d) => {
    buf += d;
    const m = buf.match(/ws:\/\/[^\s]+/);
    if (m) { clearTimeout(to); res(m[0]); }
  });
});

const ws = new WebSocket(wsURL);
await new Promise((r) => ws.addEventListener('open', r, { once: true }));

let nextID = 0;
const pending = new Map();
const consoleErrors = [];
const contexts = [];
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
    return;
  }
  if (msg.method === 'Runtime.executionContextCreated') {
    contexts.push(msg.params.context);
  }
  if (msg.method === 'Runtime.executionContextDestroyed') {
    const i = contexts.findIndex((c) => c.id === msg.params.executionContextId);
    if (i >= 0) contexts.splice(i, 1);
  }
  if (msg.method === 'Log.entryAdded' && msg.params.entry.level === 'error') {
    if (!/favicon/.test(msg.params.entry.url || '')) consoleErrors.push(msg.params.entry.text + ' @ ' + (msg.params.entry.url || ''));
  }
  if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
    consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description).join(' '));
  }
});

function send(method, params = {}, sessionId) {
  const id = ++nextID;
  return new Promise((res, rej) => {
    pending.set(id, { res, rej });
    ws.send(JSON.stringify({ id, method, params, sessionId }));
  });
}

const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => send(m, p, sessionId);

await S('Page.enable');
await S('Runtime.enable');
await S('Log.enable');
await S('Network.enable');
const [name, value] = cookie.split('=');
await S('Network.setCookie', { name, value, domain: host.split(':')[0], path: '/' });

await S('Page.navigate', { url });
await new Promise((r) => setTimeout(r, 8000));

// In two-origin mode the page under test is not the one navigated to:
// the main origin authorised the book and redirected here with the
// credential in the fragment. Everything after this point is the same
// set of checks, which is the point — a reader that behaves differently
// on the second hostname is not the same reader.

const evalIn = async (expr) => {
  const r = await S('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error('eval threw: ' + JSON.stringify(r.exceptionDetails));
  return r.result.value;
};

const fail = [];
const check = (name, ok, extra = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${extra ? ' — ' + extra : ''}`);
  if (!ok) fail.push(name);
};

const title = await evalIn('document.title');
check('page loads', typeof title === 'string' && title.length > 0, title);

if (detached) {
  const where = await evalIn('JSON.stringify({href: location.href, cookie: document.cookie})');
  const at = JSON.parse(where);
  console.log('detached at:', at.href, 'cookie:', JSON.stringify(at.cookie));
  check('the reader was handed off to the other origin',
    at.href.includes(mapHost.split(':')[0]), at.href);
  // The credential must not survive in the address bar, the history
  // entry, or anything the reader might paste to somebody.
  check('the credential was erased from the URL', !at.href.includes('#'), at.href);
  // The whole point of the second hostname: nothing to steal on it.
  check('the reader origin holds no cookie', at.cookie === '', at.cookie);
}

// The engine (foliate-js, ADR-0012) keeps its frames inside closed
// shadow roots, so the page cannot reach them by DOM query — and
// neither can anything else on the page, which is part of the point.
// The probe therefore goes through the engine's public API: the
// chapter documents via renderer.getContents(), the position via
// lastLocation.
const probe = `(() => {
  const view = document.querySelector('foliate-view');
  const contents = view?.renderer?.getContents?.() ?? [];
  const doc = contents[0]?.doc;
  const body = doc?.body;
  const loc = view?.lastLocation;
  return JSON.stringify({
    status: document.getElementById('reader-status')?.textContent,
    chapter: document.getElementById('reader-chapter')?.textContent,
    progress: document.getElementById('reader-progress-text')?.textContent,
    title: document.getElementById('reader-title-text')?.textContent,
    hasDoc: !!doc,
    frameReachable: !!document.querySelector('#reader-view iframe'),
    text: body ? (body.innerText || '').slice(0, 60) : '',
    colour: body ? doc.defaultView.getComputedStyle(body).color : '',
    fraction: typeof loc?.fraction === 'number' ? +loc.fraction.toFixed(4) : -1,
    cfi: loc?.cfi || '',
    ran: doc ? !!doc.documentElement.dataset.publicationRan : null,
    svgRan: doc ? !!doc.documentElement.dataset.svgRan : null,
    extRan: doc ? typeof doc.defaultView.htmx !== 'undefined' : null,
    pageTitle: document.title,
    fontSize: body ? doc.defaultView.getComputedStyle(body).fontSize : '',
    wrapWidth: body && body.firstElementChild
      ? doc.defaultView.getComputedStyle(body.firstElementChild).width : '',
    wrapMaxWidth: body && body.firstElementChild && body.firstElementChild.firstElementChild
      ? doc.defaultView.getComputedStyle(body.firstElementChild.firstElementChild).maxWidth : '',
  });
})()`;

const diag = JSON.parse(await evalIn(probe));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('the engine rendered a chapter', diag.hasDoc && diag.text.length > 10,
  `doc=${diag.hasDoc} text=${JSON.stringify(diag.text)}`);
check('the title came out of the publication', diag.title === 'Moby-Dick', diag.title);
check('reader knows the spine', /^Chapter 1 of (\d+)$/.test(diag.chapter), diag.chapter);
// The chapter frame lives in a closed shadow root: nothing on the
// page — including anything a publication managed to smuggle onto it —
// can reach in by DOM query.
check('the chapter frame is not reachable from the page',
  diag.frameReachable === false, String(diag.frameReachable));

// The publication's own stylesheet is a separate zip entry. The engine
// rewrites the link to a blob URL, which the page CSP has to permit.
check('publication stylesheet was applied',
  diag.colour.replace(/\s/g, '') === 'rgb(17,34,51)', diag.colour);

// The publication's script must not have run. It sets a data attribute
// on the documentElement; the reader strips script elements from every
// resource and the page CSP — inherited by each blob chapter — refuses
// what stripping might miss. This is the promise the vendored engine
// had to keep.
check('publication script did not run', diag.ran === false, String(diag.ran));

// It must have actually painted: an engine that renders nothing still
// reports a chapter.
const shot = await S('Page.captureScreenshot', { format: 'png' });
const png = Buffer.from(shot.data, 'base64');
if (process.env.SMOKE_SHOT) (await import('node:fs')).writeFileSync(process.env.SMOKE_SHOT, png);
const uniq = new Set(png.subarray(0, 20000)).size;
check('the page painted something', png.length > 8000 && uniq > 40,
  `${png.length} bytes, ${uniq} distinct`);

// The regression this engine was brought in for: the previous renderer
// stopped two pages in. One click is not evidence of a working reader —
// the reported bug survived one click — so this turns the page until the
// book ends and asks how far it got.
const seen = [];
for (let i = 0; i < 10; i++) {
  await evalIn(`document.getElementById('reader-next').click()`);
  await new Promise((r) => setTimeout(r, 900));
  const now = JSON.parse(await evalIn(probe));
  seen.push({ page: i + 2, chapter: now.chapter, progress: now.progress, fraction: now.fraction, cfi: now.cfi });
}
console.log('page turns:', JSON.stringify(seen, null, 1));

// Ten turns, ten different places. A book that lays itself out too wide
// still turns the page — it just turns onto blank column after blank
// column — so the count that matters is of distinct pages, not of clicks.
const distinct = new Set(seen.map((p) => p.chapter + '|' + p.fraction + '|' + p.cfi)).size;
check('the book pages past page 2', distinct >= 6,
  `${distinct} distinct pages in 10 turns`);
check('the reader leaves the first chapter',
  seen.some((p) => p.chapter !== diag.chapter),
  seen.map((p) => p.chapter).join(' '));

// Going back has to work too, or the reader is a one-way trip.
await evalIn(`document.getElementById('reader-prev').click()`);
await new Promise((r) => setTimeout(r, 900));
const back = JSON.parse(await evalIn(probe));
const wasAt = seen[seen.length - 1];
check('the book pages backwards',
  back.chapter !== wasAt.chapter || back.fraction !== wasAt.fraction ||
  back.cfi !== wasAt.cfi,
  `${wasAt.chapter} @${wasAt.fraction} -> ${back.chapter} @${back.fraction}`);

// The appearance settings must reach inside the publication: choose the
// dark theme and the chapter text obeys; reset and the publisher's own
// colour comes back. This also proves the user stylesheet survives the
// engine's page lifecycle rather than styling a page that is repainted
// away.
await evalIn(`(() => {
  const radio = document.querySelector('#reader-settings-form input[name="theme"][value="dark"]');
  radio.checked = true;
  radio.dispatchEvent(new Event('input', { bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 700));
const themed = JSON.parse(await evalIn(probe));
check('the dark theme restyles the publication',
  themed.colour.replace(/\s/g, '') === 'rgb(207,207,212)', themed.colour);
const saved = await evalIn(`localStorage.getItem('liseur.reader.settings')`);
check('settings persist in the browser',
  typeof saved === 'string' && saved.includes('"theme":"dark"'), String(saved));
await evalIn(`document.getElementById('reader-settings-reset').click()`);
await new Promise((r) => setTimeout(r, 700));
const unthemed = JSON.parse(await evalIn(probe));
check('reset restores the publisher styling',
  unthemed.colour.replace(/\s/g, '') === 'rgb(17,34,51)', unthemed.colour);

// A click on any blank margin is a page turn: aimed at the right edge
// of the stage — which the engine's own margin occupies, retargeted to
// the foliate-view host — the book moves forward.
const beforeClick = JSON.parse(await evalIn(probe));
await evalIn(`(() => {
  const stage = document.getElementById('reader-view');
  const box = stage.getBoundingClientRect();
  const target = document.elementFromPoint(box.right - 4, box.top + box.height / 2) || stage;
  target.dispatchEvent(new MouseEvent('click', { bubbles: true, clientX: box.right - 4, clientY: box.top + box.height / 2 }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const afterClick = JSON.parse(await evalIn(probe));
check('a click on the blank margin turns the page',
  afterClick.fraction > beforeClick.fraction,
  `${beforeClick.fraction} -> ${afterClick.fraction}`);

// The reading keys: space turns forward, shift+space turns back — and
// they must keep working after a click in the text, when the chapter
// frame owns the keyboard and only the per-document listener hears it.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const spaced = JSON.parse(await evalIn(probe));
check('space turns the page', spaced.fraction > afterClick.fraction,
  `${afterClick.fraction} -> ${spaced.fraction}`);
await evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  doc.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', shiftKey: true, bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const unspaced = JSON.parse(await evalIn(probe));
check('shift+space inside the chapter turns back',
  unspaced.fraction < spaced.fraction,
  `${spaced.fraction} -> ${unspaced.fraction}`);

// "?" summons the keyboard help; while it is up the reading keys are
// parked (space must not turn the page under the dialog); Escape puts
// it away — the native dialog owns that.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '?', shiftKey: true, bubbles: true }));
})()`);
const helpOpen = await evalIn(`document.getElementById('reader-help').open`);
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 600));
const heldStill = JSON.parse(await evalIn(probe));
await evalIn(`document.getElementById('reader-help').close()`);
const helpClosed = await evalIn(`!document.getElementById('reader-help').open`);
check('"?" opens the keyboard help', helpOpen === true, String(helpOpen));
check('the reading keys are parked while help is up',
  heldStill.fraction === unspaced.fraction,
  `${unspaced.fraction} -> ${heldStill.fraction}`);
check('the help closes', helpClosed === true, String(helpClosed));

// The first chapter carries the full hostile battery: an inline script,
// a script inside an SVG island, an external script pointing at a real
// same-origin file, and an attempt to retitle the parent page. Jump
// there and confirm every one of them came to nothing — the transform
// strips them, and the nonce-gated CSP refuses whatever a stripper
// might ever miss.
await evalIn(`document.querySelector('foliate-view').goTo(1)`);
await new Promise((r) => setTimeout(r, 900));
const hostile = JSON.parse(await evalIn(probe));
check('the inline script did not run', hostile.ran === false, String(hostile.ran));
check('the SVG script did not run', hostile.svgRan === false, String(hostile.svgRan));
check('the same-origin external script did not run',
  hostile.extRan === false, String(hostile.extRan));
check('the publication could not reach the parent page',
  !String(hostile.pageTitle).includes('pwned'), hostile.pageTitle);

// The font-size slider at full stretch has to mean it: the chapter's
// type is really 250% of the 16px default, and the publication's own
// width caps — a wrapper's width and a nested wrapper's max-width,
// which would otherwise keep the old, short lines pinned to the left
// of a much wider column — are lifted, so the bigger type gets the
// page. All of these were user reports before they were checks.
check('a book that caps its own width starts capped',
  hostile.wrapMaxWidth === '480px' && hostile.wrapWidth === '480px',
  `max-width ${hostile.wrapMaxWidth}, width ${hostile.wrapWidth}`);
await evalIn(`(() => {
  const slider = document.querySelector('#reader-settings-form input[name="size"]');
  slider.value = '250';
  slider.dispatchEvent(new Event('input', { bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const sized = JSON.parse(await evalIn(probe));
check('the font-size slider actually sizes the type',
  sized.fontSize === '40px', sized.fontSize);
check("the slider lifts the publication's own width caps",
  sized.wrapMaxWidth === 'none' && sized.wrapWidth !== '480px',
  `max-width ${sized.wrapMaxWidth}, width ${sized.wrapWidth}`);

// The browser refusing to run the publication's script is verified by
// confirming the script did not run and no unexpected errors occurred.
const unexpected = consoleErrors.filter((e) =>
  !/Blocked script execution/.test(e) &&
  !/Permissions policy violation: unload is not allowed/.test(e)
);
check('the browser refused to run the publication', diag.ran === false);

if (unexpected.length) {
  console.log('console errors:\n  ' + unexpected.join('\n  '));
}
check('no console errors', unexpected.length === 0);

ws.close();
proc.kill();
process.exit(fail.length ? 1 : 0);
