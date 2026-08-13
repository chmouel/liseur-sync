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

const profile = mkdtempSync(join(tmpdir(), 'smoke-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`,
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
    if (!/favicon|\/v1\//.test(msg.params.entry.url || '')) consoleErrors.push(msg.params.entry.text + ' @ ' + (msg.params.entry.url || ''));
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
await new Promise((r) => setTimeout(r, 6000));

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

// The engine renders into an iframe it creates. That frame is
// same-origin — which is how it measures the document in order to
// paginate it — so unlike the previous renderer, its contents can be
// inspected from here.
const probe = `(() => {
  const frame = document.querySelector('#reader-view iframe');
  const doc = frame && frame.contentDocument;
  const body = doc && doc.body;
  return JSON.stringify({
    status: document.getElementById('reader-status')?.textContent,
    chapter: document.getElementById('reader-chapter')?.textContent,
    progress: document.getElementById('reader-progress-text')?.textContent,
    title: document.getElementById('reader-title-text')?.textContent,
    hasFrame: !!frame,
    sandbox: frame ? frame.getAttribute('sandbox') : null,
    text: body ? body.innerText.slice(0, 60) : '',
    colour: body ? getComputedStyle(body).color : '',
    scrollLeft: doc ? doc.documentElement.scrollLeft : -1,
    ran: doc ? !!doc.documentElement.dataset.publicationRan : null,
  });
})()`;

const diag = JSON.parse(await evalIn(probe));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('the engine rendered a chapter', diag.hasFrame && diag.text.length > 10,
  `frame=${diag.hasFrame} text=${JSON.stringify(diag.text)}`);
check('the title came out of the publication', diag.title === 'Moby-Dick', diag.title);
check('reader knows the spine', diag.chapter === 'Chapter 1 of 2', diag.chapter);

// The publication's own stylesheet is a separate zip entry. The engine
// rewrites the link to a blob URL, which the page CSP has to permit.
check('publication stylesheet was applied',
  diag.colour.replace(/\s/g, '') === 'rgb(17,34,51)', diag.colour);

// The publication's script must not have run. It sets a data attribute
// on the documentElement; the sandbox has no allow-scripts, so it
// cannot. This is the promise the vendored engine had to keep.
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
let lastText = diag.text;
for (let i = 0; i < 10; i++) {
  await evalIn(`document.getElementById('reader-next').click()`);
  await new Promise((r) => setTimeout(r, 900));
  const now = JSON.parse(await evalIn(probe));
  seen.push({ page: i + 2, chapter: now.chapter, progress: now.progress, head: now.text.slice(0, 24) });
  if (now.text !== lastText) lastText = now.text;
}
console.log('page turns:', JSON.stringify(seen, null, 1));

const distinct = new Set(seen.map((p) => p.head + '|' + p.progress)).size;
check('the book pages past page 2', distinct >= 4,
  `${distinct} distinct pages in 10 turns`);
check('the reader reaches the second chapter',
  seen.some((p) => p.chapter === 'Chapter 2 of 2'),
  seen.map((p) => p.chapter).join(' '));

// Going back has to work too, or the reader is a one-way trip.
await evalIn(`document.getElementById('reader-prev').click()`);
await new Promise((r) => setTimeout(r, 900));
const back = JSON.parse(await evalIn(probe));
check('the book pages backwards',
  back.chapter !== seen[seen.length - 1].chapter ||
  back.progress !== seen[seen.length - 1].progress,
  `${seen[seen.length - 1].progress} -> ${back.progress}`);

// The browser refusing to run the publication's script is not a fault,
// it is the whole point, and it is reported as a console error. Assert
// it happened and then stop counting it.
const blockedScript = consoleErrors.filter((e) => /Blocked script execution/.test(e));
const unexpected = consoleErrors.filter((e) => !/Blocked script execution/.test(e));
check('the browser refused to run the publication', blockedScript.length > 0);

if (unexpected.length) {
  console.log('console errors:\n  ' + unexpected.join('\n  '));
}
check('no console errors', unexpected.length === 0);

ws.close();
proc.kill();
process.exit(fail.length ? 1 : 0);
