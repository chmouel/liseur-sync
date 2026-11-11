// Throwaway harness: photographs of the offline shelf, for reviewing
// its shape rather than its behaviour. Not part of the committed suite.
//
// It seeds a real shelf (three downloads with covers, one of them
// read to 42%), arms a removal so the question is in the picture, and
// shoots the page in both themes, online and offline, at the size an
// iPad held upright has. `SMOKE_SHOTS` is where the PNGs go.
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const base = process.env.SMOKE_BASE;
const books = (process.env.SMOKE_BOOKS || '').split(',').filter(Boolean);
const shots = process.env.SMOKE_SHOTS;
const chrome = spawn(process.env.SMOKE_CHROME, [
  '--headless', '--disable-gpu', '--no-sandbox', '--remote-debugging-port=0',
  '--window-size=900,1200', `--user-data-dir=${process.env.SMOKE_PROFILE}`, 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'], env: { ...process.env, DISPLAY: '', WAYLAND_DISPLAY: '' } });
let ws;
const diagnostics = [];
const timer = setTimeout(() => {
  console.error('offline shelf shots timed out', diagnostics);
  chrome.kill();
  process.exit(1);
}, 180000);
try {
  mkdirSync(shots, { recursive: true });
  const endpoint = await new Promise((resolve, reject) => {
    let output = '';
    chrome.on('error', reject);
    chrome.stderr.on('data', chunk => {
      output += chunk;
      const match = output.match(/ws:\/\/[^\s]+/);
      if (match) resolve(match[0]);
    });
    chrome.on('exit', code => reject(new Error(`Chromium exited ${code}: ${output}`)));
  });
  ws = new WebSocket(endpoint);
  await new Promise(resolve => ws.addEventListener('open', resolve, { once: true }));
  let id = 0;
  const pending = new Map();
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const promise = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) promise?.reject(new Error(JSON.stringify(message.error)));
      else promise?.resolve(message.result);
    }
    if (message.method === 'Runtime.exceptionThrown') diagnostics.push(message.params.exceptionDetails);
    if (message.method === 'Log.entryAdded') diagnostics.push(message.params.entry);
  });
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    pending.set(++id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params, sessionId }));
  });
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const call = (method, params) => send(method, params, sessionId);
  for (const domain of ['Page', 'Runtime', 'Network', 'Log']) await call(`${domain}.enable`);
  const evaluate = async expression => {
    const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async (expression, label) => {
    let last;
    for (let attempt = 0; attempt < 150; attempt++) {
      try { last = await evaluate(expression); } catch { last = false; }
      if (last) return last;
      await new Promise(resolve => setTimeout(resolve, 200));
    }
    throw new Error(`Timed out: ${label}; ${JSON.stringify(await evaluate('document.body.innerText'))}`);
  };
  const cookie = process.env.SMOKE_COOKIE;
  const split = cookie.indexOf('=');
  await call('Network.setCookie', {
    name: cookie.slice(0, split), value: cookie.slice(split + 1), url: base, path: '/',
  });
  const appearance = async theme => {
    await call('Network.setCookie', {
      name: 'liseur_ui', value: theme, url: base, path: '/',
    });
  };

  // An upright iPad: the size the shelf is meant to look right at.
  await call('Emulation.setDeviceMetricsOverride', {
    width: 834, height: 1112, deviceScaleFactor: 2, mobile: true,
  });

  for (const book of books) {
    await call('Page.navigate', { url: `${base}ui/books/${book}` });
    await wait('document.readyState === "complete" && !!document.querySelector("[data-offline-book]")',
      'Save offline button');
    await evaluate('document.querySelector("[data-offline-book]").click()');
    await wait('document.querySelector("[data-offline-book]")?.dataset.offlineReady === "1"',
      'saved copy');
    // The next card is drawn from what the server says, so give the shelf
    // a moment to finish writing this one before moving on.
    await new Promise(resolve => setTimeout(resolve, 300));
  }

  await call('Page.navigate', { url: `${base}ui/offline/` });
  await wait(`document.querySelectorAll(".offline-book").length === ${books.length}`,
    'the shelf holding every saved book');

  // One book read to 42%, so the card has something to say about where
  // the reader is rather than only what the copy costs.
  await evaluate(`(async () => {
    const { readerAuth } = await import('./assets/reader-auth.js');
    const base = ${JSON.stringify(base)};
    const bookID = ${JSON.stringify(books[1])};
    const account = await (await fetch('./account', {
      cache: 'no-store', credentials: 'same-origin',
    })).json();
    const auth = readerAuth({ apiBase: base, tokenURL: base + 'ui/reader/token', csrf: account.csrf });
    const resolved = await (await auth.request('v1/books/' + encodeURIComponent(bookID) + '/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    })).json();
    if (!resolved.work_id) throw new Error('resolved book has no work');
    const applied = await auth.request('v1/ops', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ops: [{
          op_id: 'shot-progress',
          work_id: resolved.work_id,
          progression: 0.42,
          client_ts: new Date().toISOString(),
        }],
      }),
    });
    if (!applied.ok) throw new Error('server position was not accepted');
    dispatchEvent(new CustomEvent('offline-change', { detail: { partition: base } }));
  })()`);
  await wait(`!!document.querySelector('.offline-book-progress-label')`, 'progress on a card');
  // The numbers a reviewer would take from the picture: nothing past
  // the edge, no card copy silently clipped, and text that can still be
  // read against the card it sits on.
  const inspect = async label => {
    const report = await evaluate(`(() => {
      const parse = css => (css.match(/[\\d.]+/g) || []).map(Number);
      const lum = ([r, g, b]) => {
        const f = value => {
          const c = value / 255;
          return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
        };
        return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
      };
      const ratio = (a, b) => {
        const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
        return Math.round(((hi + 0.05) / (lo + 0.05)) * 100) / 100;
      };
      const background = node => {
        for (let el = node; el; el = el.parentElement) {
          const color = getComputedStyle(el).backgroundColor;
          const parts = parse(color);
          if (parts.length >= 3 && (parts.length < 4 || parts[3] > 0)) return parts.slice(0, 3);
        }
        return [255, 255, 255];
      };
      const cards = [...document.querySelectorAll('.offline-book')].map(card => {
        const box = card.getBoundingClientRect();
        const clipped = [...card.querySelectorAll('.offline-book-title, .offline-book-author, .offline-book-eyebrow')]
          .filter(node => node.scrollWidth > node.clientWidth + 1)
          .map(node => node.className);
        const contrast = {};
        for (const name of ['offline-book-title', 'offline-book-author', 'offline-book-eyebrow']) {
          const node = card.querySelector('.' + name);
          if (!node) continue;
          contrast[name] = ratio(parse(getComputedStyle(node).color).slice(0, 3), background(node));
        }
        const cover = card.querySelector('.offline-book-cover');
        const confirm = card.querySelector('.offline-book-confirm');
        const confirmBox = confirm && !confirm.hidden ? confirm.getBoundingClientRect() : null;
        return {
          title: card.querySelector('.offline-book-title')?.textContent,
          right: Math.round(box.right), width: Math.round(box.width),
          progress: card.querySelector('.offline-book-progress-label')?.textContent || '',
          coverLoaded: !!cover && cover.complete && cover.naturalWidth > 0,
          clipped,
          contrast,
          confirm: confirmBox && {
            insideCard: confirmBox.right <= box.right + 1 && confirmBox.left >= box.left - 1 &&
              confirmBox.bottom <= box.bottom + 1,
            width: Math.round(confirmBox.width),
          },
        };
      });
      return {
        viewport: [innerWidth, innerHeight],
        scrollWidth: document.documentElement.scrollWidth,
        theme: document.documentElement.dataset.theme,
        headerNote: document.getElementById('offline-network-note')?.hidden === false
          ? document.getElementById('offline-network-note').textContent : '',
        libraryLinkShown: (() => {
          const link = document.getElementById('offline-open-library');
          return !!link && !link.hidden && getComputedStyle(link).display !== 'none';
        })(),
        cards,
      };
    })()`);
    console.log('REPORT', label, JSON.stringify(report, null, 1));
  };

  const shoot = async name => {
    const { data } = await call('Page.captureScreenshot', { format: 'png' });
    writeFileSync(join(shots, name), Buffer.from(data, 'base64'));
    console.log('SHOT', join(shots, name));
  };
  // Two presses: the first asks, in the card, whether the copy goes.
  const arm = async index => {
    await evaluate(`document.querySelectorAll(".offline-book-remove")[${index}].click()`);
    await wait('[...document.querySelectorAll(".offline-book-confirm")].some(node => !node.hidden)',
      'the removal question');
  };

  for (const theme of ['light', 'dark']) {
    await appearance(theme);
    await call('Page.reload');
    await wait(`document.querySelectorAll(".offline-book").length === ${books.length}`,
      `the ${theme} shelf`);
    await wait(`document.documentElement.dataset.theme === ${JSON.stringify(theme)}`, 'the theme');
    await new Promise(resolve => setTimeout(resolve, 400));
    await inspect(`${theme} online`);
    await shoot(`shelf-${theme}.png`);
    await arm(1);
    await inspect(`${theme} confirm`);
    await shoot(`shelf-${theme}-confirm.png`);
  }

  // The header without a network: the one state a screenshot has to be
  // told about rather than clicked into.
  for (const theme of ['light', 'dark']) {
    await appearance(theme);
    await call('Page.reload');
    await wait(`document.querySelectorAll(".offline-book").length === ${books.length}`,
      `the offline ${theme} shelf`);
    await call('Network.emulateNetworkConditions', {
      offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0,
    });
    await wait('document.documentElement.dataset.connection === "offline"', 'the offline header');
    await new Promise(resolve => setTimeout(resolve, 400));
    await arm(1);
    await inspect(`${theme} offline`);
    await shoot(`shelf-${theme}-offline.png`);
    await call('Network.emulateNetworkConditions', {
      offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1,
    });
  }
  const errors = diagnostics.filter(entry => entry.level === 'error' &&
    !/cover|Failed to load resource/.test(entry.text || ''));
  if (errors.length) throw new Error(`page errors: ${JSON.stringify(errors)}`);
} catch (error) {
  console.error(error, JSON.stringify(diagnostics));
  process.exitCode = 1;
} finally {
  clearTimeout(timer);
  ws?.close();
  chrome.kill();
}
