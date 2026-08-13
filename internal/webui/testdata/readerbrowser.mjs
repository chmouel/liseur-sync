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

// Record what the sandbox says to the page. The frame has an opaque
// origin, so CDP cannot script it; its own postMessages are the
// evidence that its script ran under the nonce.
await S('Page.addScriptToEvaluateOnNewDocument', {
  source: `window.__msgs = []; window.addEventListener('message', (e) => { window.__msgs.push(e.data); });`,
});

await S('Page.navigate', { url });
await new Promise((r) => setTimeout(r, 4000));

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

const diag = JSON.parse(await evalIn(`JSON.stringify({
  status: document.getElementById('reader-status')?.textContent,
  chapter: document.getElementById('reader-chapter')?.textContent,
  progress: document.getElementById('reader-progress-text')?.textContent,
  srcdocLen: (document.getElementById('reader-frame')?.srcdoc || '').length,
  frames: window.frames.length,
  msgs: window.__msgs,
  hasCSS: (document.getElementById('reader-frame')?.srcdoc || '').includes('rgb(17, 34, 51)'),
})`));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('a chapter is on screen', diag.frames === 1 && diag.srcdocLen > 500, `frames=${diag.frames} srcdoc=${diag.srcdocLen}`);
check('reader knows the spine', diag.chapter === 'Chapter 1 of 2', diag.chapter);

// The publication's own stylesheet has to survive the trip: it is a
// separate zip entry, folded into the chapter document because a
// sandboxed frame with no network cannot go and fetch it.
check('publication stylesheet was inlined', diag.hasCSS, String(diag.hasCSS));

// The chapter's injected script only runs if the nonce satisfies both
// its own meta CSP and the page CSP it inherits. This is the assertion
// that no unit test can make.
check('chapter script ran under the nonce',
  diag.msgs.some((m) => m && m.type === 'ready'), JSON.stringify(diag.msgs));

// It must have actually painted: a frame that renders nothing still
// reports ready.
const shot = await S('Page.captureScreenshot', { format: 'png' });
const png = Buffer.from(shot.data, 'base64');
if (process.env.SMOKE_SHOT) (await import('node:fs')).writeFileSync(process.env.SMOKE_SHOT, png);
const uniq = new Set(png.subarray(0, 20000)).size;
check('the page painted something', png.length > 8000 && uniq > 40,
  `${png.length} bytes, ${uniq} distinct`);

// Turning the page has to move the reader and report back.
await evalIn(`document.getElementById('reader-next').click()`);
await new Promise((r) => setTimeout(r, 1500));
const after = JSON.parse(await evalIn(`JSON.stringify({
  chapter: document.getElementById('reader-chapter')?.textContent,
  progress: document.getElementById('reader-progress-text')?.textContent,
  msgs: window.__msgs.map((m) => m && m.type),
})`));
console.log('after page turn:', JSON.stringify(after));
check('turning the page moves the reader',
  after.msgs.some((t) => t === 'progress') || after.progress !== diag.progress,
  `${diag.progress} -> ${after.progress}`);

if (consoleErrors.length) {
  console.log('console errors:\n  ' + consoleErrors.join('\n  '));
}
check('no console errors', consoleErrors.length === 0);

ws.close();
proc.kill();
process.exit(fail.length ? 1 : 0);
