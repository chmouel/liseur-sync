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
  console.error('offline reader timed out', diagnostics);
  chrome.kill();
  process.exit(1);
}, 75000);
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
  const responses = [];
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const promise = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) promise?.reject(new Error(JSON.stringify(message.error)));
      else promise?.resolve(message.result);
    }
    if (message.method === 'Network.responseReceived') responses.push(message.params.response);
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
  await online.call('Page.navigate', { url: `${base}ui/books/${book}` });
  await wait(online, 'document.readyState === "complete" && !!document.querySelector("[data-offline-book]")', 'Save offline button');
  await online.evaluate('document.querySelector("[data-offline-book]").click()');
  await wait(online, 'document.querySelector("[data-offline-book]")?.dataset.offlineReady === "1"', 'authenticated publication download');
  assert(responses.some(response => /\/publication\/[^/]+\/positions\.json$/.test(response.url) && response.status === 200),
    'download must fetch real publication positions');
  console.log('PASS authenticated Save offline downloads the real publication graph');

  await online.call('Page.navigate', { url: `${base}ui/offline/` });
  await wait(online, '!!navigator.serviceWorker.controller', 'service worker control');
  await wait(online, '!!document.querySelector("#offline-books a")', 'saved book on shelf');
  await online.evaluate('navigator.serviceWorker.ready.then(() => true)');

  // A new target has no reader modules or publication objects in memory.
  await send('Target.closeTarget', { targetId: online.targetId });
  const offline = await tab();
  await offline.call('Network.setCacheDisabled', { cacheDisabled: true });
  await offline.call('Network.emulateNetworkConditions', {
    offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0,
  });
  const readerURL = `${base}ui/offline/read/?book=${encodeURIComponent(book)}`;
  await offline.call('Page.navigate', { url: readerURL });
  const probe = `(() => {
    const view = document.querySelector('readium-view');
    const docs = view?.renderer?.getContents?.() || [];
    return docs.some(({doc}) => doc?.body?.textContent.includes('A title page, absolutely positioned'));
  })()`;
  await wait(offline, probe, 'cold offline reader renders the downloaded EPUB');
  assert.equal(await offline.evaluate(`fetch(${JSON.stringify(`${base}healthz`)}, {cache: 'no-store'})
    .then(() => false, () => true)`), true, 'real server requests are blocked by CDP offline mode');
  assert.equal(await offline.evaluate('!!navigator.serviceWorker.controller'), true);
  assert(responses.some(response => response.url === readerURL && response.fromServiceWorker),
    'cold reader navigation must come from the service worker');
  const inspect = await offline.evaluate(`(() => ({
    status: document.getElementById('reader-status')?.textContent || '',
    page: document.getElementById('reader-page')?.textContent || '',
    title: document.title,
    hostile: document.querySelector('readium-view').renderer.getContents().some(({doc}) =>
      doc.documentElement.dataset.publicationRan || doc.documentElement.dataset.svgRan || doc.defaultView.htmx),
    nonce: document.querySelector('script[nonce]')?.nonce,
  }))()`);
  assert(!/positions?.*(cannot|could not)|(?:cannot|could not).*positions?/i.test(inspect.status), inspect.status);
  assert.equal(/^(\d+) of (\d+)$/.exec(inspect.page)?.[2], process.env.SMOKE_PAGES,
    `real publication page count: ${inspect.page}`);
  assert.notEqual(inspect.title, 'pwned');
  assert.equal(inspect.hostile, false);
  assert(inspect.nonce, 'offline reader has a script nonce');
  await offline.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }))`);
  const saved = await wait(offline, `(async () => {
    const s = await import('../assets/offline-storage.js');
    const book = await s.getReadySnapshot({ bookID: ${JSON.stringify(book)} });
    const rows = await s.listOfflineOutbox({ partition: book.partition, account: book.account, kind: 'position' });
    return book.localPosition?.progression > 0.9 && rows.length ? book.localPosition : null;
  })()`, 'offline page turn is durably queued');
  await offline.call('Page.reload', { ignoreCache: true });
  await wait(offline, `(() => {
    const view = document.querySelector('readium-view');
    return view?.renderer?.getContents?.().some(({doc}) => doc?.body?.textContent.length > 0) &&
      parseFloat(document.getElementById('reader-progress-text')?.textContent) >= 90;
  })()`, 'offline reader restores the saved page after reload');
  assert.notEqual(await offline.evaluate('document.querySelector("script[nonce]")?.nonce'), inspect.nonce,
    'each cached reader navigation receives a fresh nonce');
  console.log('PASS cold /sync/ offline navigation, real positions, inert EPUB script and fresh nonce on reload');
  await offline.call('Network.emulateNetworkConditions', {
    offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1,
  });
  await wait(offline, `(async () => {
    const s = await import('../assets/offline-storage.js');
    const book = await s.getReadySnapshot({ bookID: ${JSON.stringify(book)} });
    return !(await s.listOfflineOutbox({ partition: book.partition, account: book.account, kind: 'position' })).length;
  })()`, 'reconnection acknowledges the durable position');
  const remote = await offline.evaluate(`(async () => {
    const { readerAuth } = await import('../assets/reader-auth.js');
    const base = ${JSON.stringify(base)};
    const account = await (await fetch(base + 'ui/offline/account')).json();
    const auth = readerAuth({ apiBase: base, tokenURL: base + 'ui/reader/token', csrf: account.csrf });
    return (await (await auth.request('v1/works/' + ${JSON.stringify(saved.work_id)} + '/positions?limit=1')).json()).ops[0];
  })()`);
  assert.equal(remote.op_id, saved.op_id, 'reconnect replays the persisted operation ID');
  assert.equal(remote.progression, saved.progression, 'server receives the page saved before reload');
  assert.deepEqual(remote.locator, saved.locator, 'exact locator survives offline reload and upload');
  console.log('PASS offline page turn survives reload and reconnect uploads the identical operation');
} catch (error) {
  console.error(error, JSON.stringify(diagnostics));
  process.exitCode = 1;
} finally {
  clearTimeout(timer);
  ws?.close();
  chrome.kill();
}
