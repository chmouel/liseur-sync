// Reopening a book where it was: drives real Chromium over CDP through
// the one thing readerbrowser.mjs never does, a page reload.
//
// The Go side files a position before the browser opens. This opens the
// reader on it and checks the section it lands in (and, when asked, that
// it is not the section's first page — the quoted passage, not the
// progression, decided). It then reads on, waits for the page to be
// acknowledged, reloads like F5, and checks the reader comes back to the
// same page. Neither open may push a position: a restored page is never
// published.
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const chrome = process.env.SMOKE_CHROME;
const url = process.env.SMOKE_URL;
const cookie = process.env.SMOKE_COOKIE;
const host = process.env.SMOKE_HOST;
// The spine index the seeded position names.
const expectSection = Number(process.env.RELOAD_SECTION);
// Whether the seeded position must reopen past the section's first page.
const expectInside = process.env.RELOAD_INSIDE === '1';
// Whether to read on and reload.
const doReload = process.env.RELOAD_RELOAD === '1';

const profile = mkdtempSync(join(tmpdir(), 'reload-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-sandbox', '--window-size=800,600',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });
const removeProfile = () => {
  try { rmSync(profile, { recursive: true, force: true, maxRetries: 3 }); } catch { /* best effort */ }
};
process.on('exit', () => { proc.kill(); removeProfile(); });
const finish = async (code) => {
  proc.kill();
  await new Promise((resolve) => {
    const guard = setTimeout(resolve, 4000);
    proc.once('close', () => { clearTimeout(guard); resolve(); });
  });
  removeProfile();
  process.exit(code);
};
process.on('uncaughtException', (e) => { console.error(e); finish(1); });
process.on('unhandledRejection', (e) => { console.error(e); finish(1); });

const wsURL = await new Promise((resolve, reject) => {
  let buf = '';
  const timer = setTimeout(() => reject(new Error('no devtools url: ' + buf)), 20000);
  proc.stderr.on('data', (d) => {
    buf += d;
    const m = buf.match(/ws:\/\/[^\s]+/);
    if (m) { clearTimeout(timer); resolve(m[0]); }
  });
});
const ws = new WebSocket(wsURL);
await new Promise((resolve) => ws.addEventListener('open', resolve, { once: true }));
let nextID = 0;
const pending = new Map();
const consoleErrors = [];
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
    return;
  }
  if (msg.method === 'Runtime.exceptionThrown') {
    consoleErrors.push(JSON.stringify(msg.params.exceptionDetails).slice(0, 400));
  }
});
function send(method, params = {}, sessionId) {
  const id = ++nextID;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error('CDP timeout: ' + method)); }, 15000);
    pending.set(id, {
      resolve: (v) => { clearTimeout(timer); resolve(v); },
      reject: (e) => { clearTimeout(timer); reject(e); },
    });
    ws.send(JSON.stringify({ id, method, params, sessionId }));
  });
}
const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => send(m, p, sessionId);
await S('Page.enable');
await S('Runtime.enable');
await S('Network.enable');
const [name, value] = cookie.split('=');
await S('Network.setCookie', { name, value, domain: host.split(':')[0], path: '/' });

// Every document this tab loads — including the one after the reload —
// records the positions it pushes and which of them the server took.
await S('Page.addScriptToEvaluateOnNewDocument', { source: `(() => {
  const original = window.fetch;
  window.__pushed = []; window.__delivered = [];
  window.fetch = async function (input, init = {}) {
    const url = typeof input === 'string' ? input : input.url;
    const ops = url.endsWith('v1/ops') && init.body ? (JSON.parse(init.body).ops || []) : [];
    for (const op of ops) window.__pushed.push({ op_id: op.op_id, progression: op.progression });
    const resp = await original.call(this, input, init);
    if (ops.length && resp.ok) for (const op of ops) window.__delivered.push(op.op_id);
    return resp;
  };
})()` });

const evalIn = async (expr) => {
  const r = await S('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error('eval threw: ' + JSON.stringify(r.exceptionDetails));
  return r.result.value;
};
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(expression, description, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await evalIn(expression)) return;
    await pause(50);
  }
  throw new Error('Timed out waiting for ' + description);
}
const opened = `(() => {
  const view = document.querySelector('readium-view');
  return Number.isFinite(view?.lastLocation?.fraction) &&
    view?.renderer?.getContents?.().some(({ doc }) => doc?.body) &&
    document.getElementById('reader-status')?.textContent === '';
})()`;
const where = async () => JSON.parse(await evalIn(`(() => {
  const l = document.querySelector('readium-view').lastLocation;
  return JSON.stringify({
    fraction: l.fraction, section: l.section?.current, sectionFraction: l.sectionFraction,
    page: document.getElementById('reader-page')?.textContent,
  });
})()`));

const fail = [];
const check = (name, ok, extra = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${extra ? ' — ' + extra : ''}`);
  if (!ok) fail.push(name);
};

await S('Page.navigate', { url });
await waitFor(opened, 'the publication to open');
// The open settles: a restore that walks a quote arrives a beat later.
await pause(1500);
const first = await where();
check('the seeded position reopens in its section', first.section === expectSection,
  JSON.stringify(first));
if (expectInside) {
  // The seeded progression says the start of the chapter; the anchor
  // names its fortieth paragraph of sixty. Landing past the middle can
  // only be the anchor's doing.
  check('the anchor, not the progression, chose the page', first.sectionFraction >= 0.5,
    JSON.stringify(first));
}
check('opening pushed no position', (await evalIn('window.__pushed.length')) === 0);

if (doReload) {
  // Read on the way a reader does — a key, then pages — so the new page
  // is this device's own and is filed.
  await evalIn(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' })); true`);
  for (let i = 0; i < 2; i++) {
    await evalIn("document.getElementById('reader-next').click()");
    await pause(700);
  }
  // The page on screen must be the one the server took, or the reload
  // is asked to restore a page that was never filed.
  await waitFor(`window.__pushed.length > 0 &&
    window.__delivered.includes(window.__pushed.at(-1).op_id) &&
    window.__pushed.at(-1).progression === document.querySelector('readium-view').lastLocation.fraction`,
  'the page on screen to be acknowledged');
  await pause(500);
  const before = await where();
  check('reading on moved the page', before.fraction > first.fraction, JSON.stringify(before));

  await S('Page.reload', { ignoreCache: false });
  await pause(500);
  await waitFor(opened, 'the publication to reopen after the reload', 20000);
  await pause(1500);
  const after = await where();
  check('the reload reopens on the page being read',
    Math.abs(after.fraction - before.fraction) < 0.005,
    `before ${JSON.stringify(before)} after ${JSON.stringify(after)}`);
  await pause(2000);
  check('reopening pushed no position', (await evalIn('window.__pushed.length')) === 0,
    await evalIn('JSON.stringify(window.__pushed)'));
}

check('no uncaught errors', consoleErrors.length === 0, consoleErrors.join(' | '));
ws.close();
await finish(fail.length ? 1 : 0);
