import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';

const base = process.env.SMOKE_BASE;
const book = process.env.SMOKE_BOOK;
const chrome = spawn(process.env.SMOKE_CHROME, [
  '--headless', '--disable-gpu', '--no-sandbox', '--remote-debugging-port=0',
  '--window-size=800,600', `--user-data-dir=${process.env.SMOKE_PROFILE}`, 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'], env: { ...process.env, DISPLAY: '', WAYLAND_DISPLAY: '' } });
let ws;
const diagnostics = [];
const timer = setTimeout(() => {
  console.error('offline shelf timed out', diagnostics);
  chrome.kill();
  process.exit(1);
}, 120000);
try {
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
  const tab = async () => {
    const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
    const call = (method, params) => send(method, params, sessionId);
    for (const domain of ['Page', 'Runtime', 'Network', 'Log']) await call(`${domain}.enable`);
    const evaluate = async expression => {
      const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
      return result.result.value;
    };
    return { targetId, call, evaluate };
  };
  const wait = async (page, expression, label) => {
    let last;
    for (let attempt = 0; attempt < 100; attempt++) {
      try { last = await page.evaluate(expression); } catch { last = false; }
      if (last) return last;
      await new Promise(resolve => setTimeout(resolve, 200));
    }
    throw new Error(`Timed out: ${label}; ${JSON.stringify(await page.evaluate('document.body.innerText'))}`);
  };

  const online = await tab();
  const cookie = process.env.SMOKE_COOKIE;
  const split = cookie.indexOf('=');
  await online.call('Network.setCookie', {
    name: cookie.slice(0, split), value: cookie.slice(split + 1), url: base, path: '/',
  });

  // #111: the shelf is reachable from the rail, and only once a script
  // has established that this browser can keep it reachable.
  await online.call('Page.navigate', { url: `${base}ui/library` });
  const rail = await wait(online, `(() => {
    const link = document.querySelector('[data-rail-offline]');
    if (!link || link.hidden) return null;
    return { href: link.getAttribute('href'), text: link.textContent.trim(), before: link.previousElementSibling?.textContent.trim() };
  })()`, 'rail entry for the offline shelf');
  assert.equal(rail.href, 'offline/');
  assert.match(rail.text, /^Offline/);
  assert.equal(rail.before, 'Library', 'the shelf sits beside the library, not after the account pages');
  console.log('PASS the rail reveals the offline shelf where a service worker exists');

  await online.call('Page.navigate', { url: `${base}ui/books/${book}` });
  await wait(online, 'document.readyState === "complete" && !!document.querySelector("[data-offline-book]")', 'Save offline button');
  await online.evaluate('document.querySelector("[data-offline-book]").click()');
  await wait(online, 'document.querySelector("[data-offline-book]")?.dataset.offlineReady === "1"', 'saved copy');
  const saved = await online.evaluate(`(() => {
    const button = document.querySelector('[data-offline-book]');
    const status = document.querySelector('[data-offline-status="' + button.dataset.offlineBook + '"]');
    const link = status?.querySelector('a');
    return { text: status?.textContent, href: link?.getAttribute('href') || '' };
  })()`);
  assert.equal(saved.text, 'Saved offline', 'the label still says what it always said');
  assert.equal(saved.href, '/sync/ui/offline/', 'the label leads to the shelf that holds the copy');
  console.log('PASS the saved label on the book page links to the shelf');

  await online.call('Page.navigate', { url: `${base}ui/offline/` });
  await wait(online, 'document.querySelectorAll(".offline-book").length === 1', 'saved book on the shelf');
  const card = await online.evaluate(`(() => {
    const item = document.querySelector('.offline-book');
    return {
      title: item.querySelector('.offline-book-title')?.textContent || '',
      cover: item.querySelector('.offline-book-cover')?.getAttribute('src')?.slice(0, 40) || '',
      hasInitial: !!item.querySelector('.offline-book-initial'),
      facts: [...item.querySelectorAll('.offline-book-badge')].map(node => node.textContent),
      eyebrow: item.querySelector('.offline-book-eyebrow')?.textContent || '',
      remove: item.querySelector('.offline-book-remove')?.textContent || '',
    };
  })()`);
  assert.equal(card.title, 'Illustrated');
  assert.match(card.cover, /^data:image\/(png|jpeg);base64,/, 'the cover is kept with the book, not linked to a server');
  assert.equal(card.hasInitial, true, 'the monogram stays behind the cover for a book without one');
  assert.deepEqual(card.facts.filter(value => value === 'Ebook'), ['Ebook']);
  assert(card.facts.some(value => /^(kB|MB)$/.test(value.split(' ')[1] || '')),
    `the card does not say what the copy costs: ${JSON.stringify(card.facts)}`);
  assert.match(card.eyebrow, /^Saved /);
  assert.equal(card.remove, 'Remove');
  console.log('PASS the shelf shows the cover, the book, the badge and the size');

  const state = `(() => {
    const item = document.querySelector('.offline-book');
    const confirm = item?.querySelector('.offline-book-confirm');
    return {
      armed: !!confirm && !confirm.hidden,
      question: confirm?.querySelector('.offline-book-ask')?.textContent || '',
      hint: confirm?.querySelector('.offline-book-hint')?.textContent || '',
      focus: document.activeElement?.className || '',
      books: document.querySelectorAll('.offline-book').length,
      notice: document.getElementById('offline-notice')?.textContent || '',
      status: document.getElementById('offline-status')?.textContent || '',
    };
  })()`;
  await online.evaluate('document.querySelector(".offline-book-remove").click()');
  const armed = await wait(online, `(() => {
    const now = ${state};
    return now.armed ? now : null;
  })()`, 'the removal question');
  assert.equal(armed.question, 'Remove from this device?');
  assert.match(armed.hint, /freed|deleted/, 'the question says what it costs');
  assert.equal(armed.books, 1, 'arming must not delete anything');
  assert.match(armed.focus, /offline-book-keep/, 'focus lands on keeping the copy');
  assert.match(armed.notice, /Remove/, 'the question is announced');

  await online.evaluate('document.querySelector(".offline-book-keep").click()');
  const kept = await wait(online, `(() => {
    const now = ${state};
    return now.armed ? null : now;
  })()`, 'the kept copy');
  assert.equal(kept.books, 1);
  assert.match(kept.focus, /offline-book-remove/, 'focus returns to the button that asked');
  console.log('PASS a removal is a question, and keeping the copy costs one tap');

  await online.evaluate('document.querySelector(".offline-book-remove").click()');
  await wait(online, `${state}.armed`, 'the second question');
  await online.evaluate('document.querySelector(".offline-book-confirmed").click()');
  const removed = await wait(online, `(() => {
    const now = ${state};
    return now.books === 0 ? now : null;
  })()`, 'the removed copy');
  assert.match(removed.status, /Nothing saved yet/, removed.status);
  assert.match(removed.notice, /removed from this device/);
  console.log('PASS the second press removes the copy and says so');

  // #108: no network, no way out of the shelf.
  await online.call('Network.emulateNetworkConditions', {
    offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0,
  });
  const offlineHeader = await wait(online, `(() => {
    if (document.documentElement.dataset.connection !== 'offline') return null;
    const link = document.getElementById('offline-open-library');
    const note = document.getElementById('offline-network-note');
    const brand = document.getElementById('offline-brand');
    return {
      linkHidden: link.hidden,
      linkShown: getComputedStyle(link).display !== 'none',
      note: note.hidden ? '' : note.textContent,
      brandHref: brand.getAttribute('href'),
      brandDisabled: brand.getAttribute('aria-disabled'),
      brandTabindex: brand.getAttribute('tabindex'),
    };
  })()`, "the shelf header without a network");
  assert.equal(offlineHeader.linkShown, false, 'the library link must not be painted offline');
  assert.equal(offlineHeader.linkHidden, true);
  assert.match(offlineHeader.note, /You're offline, your library will be back/);
  assert.equal(offlineHeader.brandHref, null, 'the brand stops being a way out');
  assert.equal(offlineHeader.brandDisabled, 'true');
  assert.equal(offlineHeader.brandTabindex, '-1');
  console.log('PASS offline, the shelf offers no link the device cannot follow');

  await online.call('Network.emulateNetworkConditions', {
    offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1,
  });
  const backOnline = await wait(online, `(() => {
    if (document.documentElement.dataset.connection !== 'online') return null;
    const link = document.getElementById('offline-open-library');
    const note = document.getElementById('offline-network-note');
    const brand = document.getElementById('offline-brand');
    return {
      linkShown: !link.hidden && getComputedStyle(link).display !== 'none',
      noteHidden: note.hidden,
      brandHref: brand.getAttribute('href'),
      brandDisabled: brand.getAttribute('aria-disabled'),
    };
  })()`, "the shelf header once the network is back");
  assert.equal(backOnline.linkShown, true);
  assert.equal(backOnline.noteHidden, true);
  assert.equal(backOnline.brandHref, '../library');
  assert.equal(backOnline.brandDisabled, null);
  console.log('PASS reconnecting brings the library link back');

  assert.deepEqual(diagnostics.filter(entry => entry.level === 'error' && !/cover/.test(entry.url || '')), [],
    'no page errors');
  console.log('PASS no exception was thrown along the way');
} finally {
  clearTimeout(timer);
  chrome.kill();
  ws?.close();
}
