// Throwaway harness: drives real Chromium over CDP to prove the library
// page's refresh actually redraws the shelf. Not part of the committed
// suite; the Go test next door starts it.
//
// This exists because a refresh that quietly does the wrong thing looks
// exactly like one that works. Asking the server for this page over
// htmx returns a fragment of the card list rather than the page around
// it, and swapping that into the page removes the shelf. Nothing short
// of a browser notices.
import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const chrome = process.env.SMOKE_CHROME;
const url = process.env.SMOKE_URL;
const cookie = process.env.SMOKE_COOKIE;
const host = process.env.SMOKE_HOST;
const title = process.env.SMOKE_TITLE || '';

const profile = mkdtempSync(join(tmpdir(), 'smoke-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  '--window-size=800,600',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });
process.on('exit', () => proc.kill());
process.on('SIGTERM', () => process.exit(1));

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
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
    return;
  }
  if (msg.method === 'Log.entryAdded' && msg.params.entry.level === 'error') {
    if (!/favicon/.test(msg.params.entry.url || '')) {
      consoleErrors.push(msg.params.entry.text + ' @ ' + (msg.params.entry.url || ''));
    }
  }
  if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
    consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description).join(' '));
  }
});

function send(method, params = {}, sessionId) {
  const id = ++nextID;
  return new Promise((res, rej) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      rej(new Error('CDP request timed out: ' + method));
    }, 15000);
    pending.set(id, {
      res: value => { clearTimeout(timer); res(value); },
      rej: error => { clearTimeout(timer); rej(error); },
    });
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

const evalIn = async (expr) => {
  const r = await S('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error('eval threw: ' + JSON.stringify(r.exceptionDetails));
  return r.result.value;
};

async function waitFor(expression, description, timeout = 10000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await evalIn(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error('Timed out waiting for ' + description);
}

const check = (ok, what) => { if (!ok) throw new Error(what); };

await S('Page.navigate', { url });
await waitFor(`!!document.querySelector('#content .page')`, 'the shelf to arrive');

// The button is served hidden and revealed by the module. If it is
// still hidden, the module did not run and nothing below means
// anything.
await waitFor(`(() => {
  const button = document.querySelector('[data-refresh]');
  return !!button && !button.hidden;
})()`, 'the refresh button to be revealed');

check(await evalIn(`document.body.innerText.includes(${JSON.stringify(title)})`),
  'the shelf did not list the seeded book before refreshing');

// Where the shelf actually settled, which is not the URL navigated to:
// /ui/ redirects to the library. A refresh must not move off it.
const settled = await evalIn(`location.href`);

// Mark the page that is on screen now, so the swap can be told apart
// from nothing having happened at all.
await evalIn(`document.querySelector('#content .page').dataset.before = 'yes'`);
await evalIn(`document.querySelector('[data-refresh]').click()`);

await waitFor(`document.getElementById('pull-indicator')?.textContent === 'Refreshed.'`,
  'the refresh to report success');

const page = await evalIn(`(() => {
  const region = document.querySelector('#content .page');
  return {
    present: !!region,
    swapped: !!region && region.dataset.before !== 'yes',
    lists: document.body.innerText.includes(${JSON.stringify(title)}),
    cards: document.querySelectorAll('.bookcard').length,
    // A fragment answer would bring the card list without the page
    // around it, which is exactly the failure this test exists for.
    intro: !!document.querySelector('.page-intro'),
    button: !!document.querySelector('[data-refresh]:not([hidden])'),
  };
})()`);

check(page.present, 'the refresh removed the shelf');
check(page.swapped, 'the refresh did not redraw anything');
check(page.lists, 'the redrawn shelf does not list the book');
check(page.cards > 0, 'the redrawn shelf has no cards');
check(page.intro, 'the redrawn shelf is a bare fragment, not the page');
check(page.button, 'the refresh button did not survive its own refresh');
const landed = await evalIn(`location.href`);
check(landed === settled,
  'the refresh navigated away instead of redrawing in place: ' + landed);

if (consoleErrors.length) {
  throw new Error('console errors: ' + consoleErrors.join(' | '));
}

console.log('library refresh ok: ' + page.cards + ' card(s) redrawn');
process.exit(0);
