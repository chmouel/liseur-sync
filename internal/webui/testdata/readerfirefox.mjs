// Firefox harness for the reader, driven over WebDriver BiDi.
//
// Chromium is not the only browser that has to lay a book out, and the
// two disagree about exactly the things this reader depends on: how a
// sandboxed frame inherits a policy, and what a blocked script reports.
// This runs the same checks as the Chromium harness against Firefox, so
// "works here" means something.
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const firefox = process.env.SMOKE_FIREFOX || 'firefox';
const url = process.env.SMOKE_URL;
const cookie = process.env.SMOKE_COOKIE;
const host = process.env.SMOKE_HOST;
const detached = process.env.SMOKE_DETACHED === '1';
const readerHost = process.env.SMOKE_READER_HOST || '';

// A browser that never answers must not become a ten-minute test. The
// watchdog reports the last thing that was asked of it, which is the
// only useful thing to know about a hang.
let step = 'starting firefox';
const at = (s) => { step = s; };
setTimeout(() => {
  console.error('firefox harness stuck at: ' + step);
  process.exit(2);
}, 90000);

const profile = mkdtempSync(join(tmpdir(), 'ffsmoke-'));
// Firefox will not resolve a made-up hostname, and unlike Chromium it
// has no resolver-rules flag. A proxy autoconfig that sends everything
// to the loopback listener is the equivalent.
if (readerHost) {
  const port = readerHost.split(':')[1];
  const pac = `function FindProxyForURL(u, h) { return "PROXY 127.0.0.1:${port}"; }`;
  writeFileSync(join(profile, 'proxy.pac'), pac);
  writeFileSync(join(profile, 'user.js'), [
    'user_pref("network.proxy.type", 2);',
    `user_pref("network.proxy.autoconfig_url", "file://${join(profile, 'proxy.pac')}");`,
    'user_pref("network.proxy.allow_hijacking_localhost", true);',
  ].join('\n'));
}

const proc = spawn(firefox, [
  '--headless', '--no-remote', '--profile', profile,
  '--remote-debugging-port=0', 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });

const wsURL = await new Promise((res, rej) => {
  let buf = '';
  const to = setTimeout(() => rej(new Error('firefox printed no bidi url: ' + buf)), 30000);
  proc.stderr.on('data', (d) => {
    buf += d;
    const m = buf.match(/ws:\/\/[^\s]+/);
    if (m) { clearTimeout(to); res(m[0]); }
  });
});

at('opening the bidi socket');
// Firefox prints the endpoint, but a session is only created on the
// /session path; the bare endpoint answers with a non-101 and the
// socket hangs open-less forever.
const ws = new WebSocket(wsURL + '/session');
ws.addEventListener('error', (e) => {
  console.error('bidi socket failed: ' + (e.message || 'unknown'));
  process.exit(2);
});
await new Promise((r) => ws.addEventListener('open', r, { once: true }));

let nextID = 0;
const pending = new Map();
const consoleErrors = [];
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? rej(new Error(JSON.stringify(msg))) : res(msg.result);
    return;
  }
  if (msg.method === 'log.entryAdded' && msg.params.level === 'error') {
    const where = msg.params.source?.realm || '';
    if (!/favicon/.test(msg.params.text || '')) {
      consoleErrors.push(msg.params.text + (where ? ' @ ' + where : ''));
    }
  }
});

const send = (method, params = {}) => new Promise((res, rej) => {
  const id = ++nextID;
  pending.set(id, { res, rej });
  ws.send(JSON.stringify({ id, method, params }));
});

at('session.new');
await send('session.new', { capabilities: {} });
await send('session.subscribe', { events: ['log.entryAdded'] });
at('getTree');
const tree = await send('browsingContext.getTree', {});
const context = tree.contexts[0].context;

const [name, value] = cookie.split('=');
at('setCookie');
await send('storage.setCookie', {
  cookie: {
    name, value: { type: 'string', value },
    domain: host.split(':')[0], path: '/',
  },
});

at('navigate');
await send('browsingContext.navigate', { context, url, wait: 'complete' });
await new Promise((r) => setTimeout(r, 6000));

const evalIn = async (expression) => {
  const r = await send('script.evaluate', {
    expression, target: { context }, awaitPromise: true, resultOwnership: 'none',
  });
  if (r.type === 'exception') throw new Error('eval threw: ' + JSON.stringify(r.exceptionDetails));
  return r.result.value;
};

const fail = [];
const check = (name, ok, extra = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${extra ? ' — ' + extra : ''}`);
  if (!ok) fail.push(name);
};

check('page loads', typeof (await evalIn('document.title')) === 'string',
  await evalIn('document.title'));

if (detached) {
  const at = JSON.parse(await evalIn('JSON.stringify({href: location.href, cookie: document.cookie})'));
  check('the reader was handed off to the other origin', at.href.includes(readerHost.split(':')[0]), at.href);
  check('the credential was erased from the URL', !at.href.includes('#'), at.href);
  check('the reader origin holds no cookie', at.cookie === '', at.cookie);
}

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
    text: body ? (body.innerText || '').slice(0, 60) : '',
    colour: body ? getComputedStyle(body).color : '',
    scroll: document.querySelector('#reader-view .epub-container')?.scrollLeft ?? -1,
    ran: doc ? !!doc.documentElement.dataset.publicationRan : null,
  });
})()`;

at('first probe');
const diag = JSON.parse(await evalIn(probe));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('the engine rendered a chapter', diag.hasFrame && diag.text.length > 10,
  `frame=${diag.hasFrame} text=${JSON.stringify(diag.text)}`);
check('the title came out of the publication', diag.title === 'Moby-Dick', diag.title);
check('reader knows the spine', /^Chapter 1 of (\d+)$/.test(diag.chapter), diag.chapter);
check('publication stylesheet was applied',
  diag.colour.replace(/\s/g, '') === 'rgb(17,34,51)', diag.colour);
check('publication script did not run', diag.ran === false, String(diag.ran));

at('turning pages');
const seen = [];
for (let i = 0; i < 10; i++) {
  await evalIn(`document.getElementById('reader-next').click()`);
  await new Promise((r) => setTimeout(r, 900));
  const now = JSON.parse(await evalIn(probe));
  seen.push({ page: i + 2, chapter: now.chapter, progress: now.progress, scroll: now.scroll });
}
console.log('page turns:', JSON.stringify(seen, null, 1));
const distinct = new Set(seen.map((p) => p.chapter + '|' + p.progress + '|' + p.scroll)).size;
check('the book pages past page 2', distinct >= 6, `${distinct} distinct pages in 10 turns`);
check('the reader leaves the first chapter',
  seen.some((p) => p.chapter !== diag.chapter), seen.map((p) => p.chapter).join(' '));

if (process.env.SMOKE_SHOT) {
  const shot = await send('browsingContext.captureScreenshot', { context });
  writeFileSync(process.env.SMOKE_SHOT, Buffer.from(shot.data, 'base64'));
}

// Chromium says out loud that it refused to run the publication's
// script. Firefox reports the same refusal as a policy violation, and
// BiDi's log channel carries console calls and script errors only — a
// violation never reaches it. So state the refusal as what it is rather
// than as something a browser happens to print: the frame is sandboxed
// without allow-scripts, and the publication's script did not run.
const sandbox = diag.sandbox || '';
check('the publication is sandboxed without scripts',
  sandbox === 'allow-same-origin', JSON.stringify(diag.sandbox));
check('the browser refused to run the publication', diag.ran === false,
  String(diag.ran));
const unexpected = consoleErrors.filter((e) => !/Content-Security-Policy|Blocked script/i.test(e));
if (unexpected.length) console.log('console errors:\n  ' + unexpected.join('\n  '));
check('no console errors', unexpected.length === 0);

ws.close();
proc.kill();
process.exit(fail.length ? 1 : 0);
