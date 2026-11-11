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
  const responses = [];
  const paused = [];
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const promise = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) promise?.reject(new Error(JSON.stringify(message.error)));
      else promise?.resolve(message.result);
    }
    if (message.method === 'Network.responseReceived') responses.push(message.params.response);
    if (message.method === 'Fetch.requestPaused') paused.push(message.params);
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

  const button = 'document.querySelector("[data-offline-book]")';
  const offlineState = (text, ready, status) => `(() => {
    const b = ${button};
    const s = document.querySelector('[data-offline-status="' + b.dataset.offlineBook + '"]');
    return b.textContent === ${JSON.stringify(text)} && b.dataset.offlineReady === ${JSON.stringify(ready)}
      && s?.textContent === ${JSON.stringify(status)};
  })()`;
  // The first click's handler runs up to its first await and takes the
  // slot, so the second click cancels before the saved copy is removed.
  await online.evaluate(`${button}.click(); ${button}.click();`);
  await wait(online, offlineState('Remove offline copy', '1', 'Download cancelled'),
    'a cancel before removal keeps the saved copy');
  console.log('PASS cancelling before removal keeps the saved copy');

  // offline-change is dispatched synchronously once storage has changed and
  // before the button is updated, so a click from it lands a cancel in the
  // window where the operation can no longer stop.
  const lateCancel = `addEventListener('offline-change', () => ${button}.click(), { once: true }); ${button}.click();`;
  await online.evaluate(lateCancel);
  await wait(online, offlineState('Save offline', '', 'Removed from this device'),
    'a cancel after removal started reports the removal');
  console.log('PASS a late cancel reports the removal that happened');

  // Cancel and restart while the first request is held: the cancelled
  // operation must not write over the one that replaced it.
  await online.call('Fetch.enable', { patterns: [{ urlPattern: '*/resolve', requestStage: 'Request' }] });
  await online.evaluate(`${button}.click()`);
  for (let attempt = 0; attempt < 100 && paused.length < 1; attempt++) await new Promise(r => setTimeout(r, 100));
  assert.equal(paused.length, 1, 'the first resolve request must be held');
  await online.evaluate(`(() => {
    const b = ${button};
    const s = document.querySelector('[data-offline-status="' + b.dataset.offlineBook + '"]');
    b.click(); b.click();
    window.afterRetry = [];
    new MutationObserver(() => afterRetry.push(s.textContent + ' | ' + b.textContent))
      .observe(s, { childList: true, characterData: true, subtree: true });
    new MutationObserver(() => afterRetry.push(s.textContent + ' | ' + b.textContent))
      .observe(b, { childList: true, characterData: true, subtree: true });
  })()`);
  for (let attempt = 0; attempt < 100 && paused.length < 2; attempt++) await new Promise(r => setTimeout(r, 100));
  assert.equal(paused.length, 2, 'the restarted download must reach its own resolve request');
  assert.equal(await online.evaluate(`${button}.textContent`), 'Cancel download',
    'the restarted download keeps its cancel label');
  await online.call('Fetch.continueRequest', { requestId: paused[0].requestId }).catch(() => {});
  await online.call('Fetch.continueRequest', { requestId: paused[1].requestId });
  await online.call('Fetch.disable');
  await wait(online, offlineState('Remove offline copy', '1', 'Saved offline'), 'restarted download completes');
  const afterRetry = await online.evaluate('afterRetry');
  assert(!afterRetry.some(entry => entry.startsWith('Download cancelled') || entry.endsWith('| Save offline')),
    `the cancelled operation wrote over the restarted one: ${JSON.stringify(afterRetry)}`);
  console.log('PASS cancel and immediate restart keeps the new download in charge');

  await online.evaluate(`${button}.click()`);
  await wait(online, offlineState('Save offline', '', 'Removed from this device'), 'remove before late-cancel download');
  await online.evaluate(lateCancel);
  await wait(online, offlineState('Remove offline copy', '1', 'Saved offline'),
    'a cancel after the commit started reports the saved copy');
  console.log('PASS a late cancel reports the copy that was saved');

  await online.call('Page.navigate', { url: `${base}ui/offline/` });
  await wait(online, '!!navigator.serviceWorker.controller', 'service worker control');
  await wait(online, '!!document.querySelector("#offline-books a")', 'saved book on shelf');
  const shelf = await online.evaluate(`(() => ({
    copyTag: document.querySelector('.offline-book-copy')?.tagName || '',
    hasProgressLabel: !!document.querySelector('.offline-book-progress-label'),
    hasProgressRail: !!document.querySelector('.offline-book-progress'),
  }))()`);
  assert.equal(shelf.copyTag, 'DIV', 'offline shelf copy wrapper must allow heading content');
  assert.equal(shelf.hasProgressLabel, false, 'a book with no local position shows no progress label');
  assert.equal(shelf.hasProgressRail, false, 'a book with no local position shows no progress rail');
  await online.evaluate(`(async () => {
    const { readerAuth } = await import('./assets/reader-auth.js');
    const base = ${JSON.stringify(base)};
    const bookID = ${JSON.stringify(book)};
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
          op_id: 'shelf-remote-progress',
          work_id: resolved.work_id,
          progression: 1,
          client_ts: new Date().toISOString(),
        }],
      }),
    });
    if (!applied.ok) throw new Error('server position was not accepted');
    dispatchEvent(new CustomEvent('offline-change', { detail: { partition: base } }));
  })()`);
  await wait(online, `(() => {
    const label = document.querySelector('.offline-book-progress-label')?.textContent?.trim();
    const rail = document.querySelector('.offline-book-progress');
    return label === 'Finished'
      && rail?.getAttribute('role') === 'progressbar'
      && rail?.getAttribute('aria-valuenow') === '100'
      && rail?.getAttribute('aria-valuetext') === 'Finished';
  })()`, 'shelf redraws progress after background reconciliation');
  console.log('PASS shelf redraws progress after background reconciliation');
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
    return docs.some(({doc}) => {
      const text = doc?.body?.textContent?.trim() || '';
      return text.length > 0 && !/^Back\s*$/i.test(text) && !/^Retry sync\s*$/i.test(text);
    });
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
  // #106: waiting on a network is the reader's business, so the control
  // that answers it lives on the top bar with the rest of the controls
  // and inside its bounds, never behind it.
  const syncPill = await offline.evaluate(`(() => {
    const pill = document.getElementById('reader-offline-sync');
    const bar = document.querySelector('.reader-bar');
    if (!pill || !bar) return null;
    const box = pill.getBoundingClientRect();
    const barBox = bar.getBoundingClientRect();
    return {
      inBar: bar.contains(pill),
      visible: box.width > 0 && box.height > 0 &&
        box.top >= barBox.top - 1 && box.bottom <= barBox.bottom + 1,
      state: pill.dataset.state,
      text: pill.textContent.trim(),
      retry: pill.querySelector('.reader-offline-sync-retry')?.textContent.trim() || '',
      retryHidden: !!pill.querySelector('.reader-offline-sync-retry')?.hidden,
    };
  })()`);
  assert(syncPill, 'the offline reader offers a sync control');
  assert.equal(syncPill.inBar, true, 'the sync control belongs in the reader top bar');
  assert.equal(syncPill.visible, true, 'the sync control must not be drawn behind the top bar');
  assert.equal(syncPill.state, 'waiting');
  assert.match(syncPill.text, /Waiting to sync/);
  assert.equal(syncPill.retry, 'Try again');
  assert.equal(syncPill.retryHidden, false);
  console.log('PASS the sync control sits in the top bar, not behind it');

  // #106 at the narrow end: 320px and a finger, which is where the bar
  // has the least room and every control asks for 44px.
  await offline.call('Emulation.setDeviceMetricsOverride', {
    width: 320, height: 568, deviceScaleFactor: 1, mobile: true,
  });
  await offline.call('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
  const narrowBar = `(() => {
    const bar = document.querySelector('.reader-bar');
    const box = bar.getBoundingClientRect();
    const controls = [...bar.querySelectorAll('a, button, .reader-offline-sync')]
      .filter(node => node.getBoundingClientRect().width > 0 && !node.closest('#reader-settings'))
      .map(node => ({ id: node.id || node.className, box: node.getBoundingClientRect() }));
    return {
      coarse: matchMedia('(any-pointer: coarse)').matches,
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth,
      bar: { left: box.left, right: box.right, top: box.top, bottom: box.bottom },
      overflow: controls.filter(({ box: c }) => c.right > box.right + 0.5 || c.left < box.left - 0.5)
        .map(({ id }) => id),
      below: controls.filter(({ box: c }) => c.bottom > box.bottom + 0.5).map(({ id }) => id),
    };
  })()`;
  let narrow = await offline.evaluate(narrowBar);
  assert.equal(narrow.coarse, true, 'the emulated phone reports a coarse pointer');
  assert.equal(narrow.scrollWidth <= narrow.innerWidth, true,
    `the reader toolbar overflows at 320px: ${JSON.stringify(narrow)}`);
  assert.deepEqual(narrow.overflow, [], 'no reader control is drawn outside the bar at 320px');
  assert.deepEqual(narrow.below, [], 'no reader control drops out of the bar at 320px');
  const pillBox = await offline.evaluate(`(() => {
    const pill = document.getElementById('reader-offline-sync');
    const retry = pill?.querySelector('.reader-offline-sync-retry');
    const box = pill?.getBoundingClientRect();
    return pill && {
      visible: !pill.hidden && box.width > 0,
      width: box.width,
      text: pill.textContent.trim(),
      retry: retry?.textContent.trim() || '',
      retryBox: retry?.getBoundingClientRect().width || 0,
    };
  })()`);
  assert.equal(pillBox.visible, true, 'a queued change still shows at 320px');
  assert.equal(pillBox.retry, 'Try again');
  assert(pillBox.retryBox >= 44, `the retry target is ${pillBox.retryBox}px, not 44px`);
  console.log('PASS the 320px toolbar holds its controls and the retry stays tappable');

  // Photos of the reader with the queue waiting, opt-in: LISEUR_SHOTS
  // names the directory. Nothing here asserts; a picture is for the
  // review, not for the suite.
  if (process.env.SMOKE_SHOTS) {
    const { mkdirSync, writeFileSync } = await import('node:fs');
    const { join } = await import('node:path');
    const dir = process.env.SMOKE_SHOTS;
    mkdirSync(dir, { recursive: true });
    // What the picture has to show: a bar whose controls are inside it,
    // and footer buttons whose hit areas are the 44px a finger needs
    // even when the text on them is two characters long.
    const readerLayout = () => offline.evaluate(`(() => {
      const bar = document.querySelector('.reader-bar');
      const barBox = bar.getBoundingClientRect();
      const controls = [...bar.querySelectorAll('a, button, .reader-offline-sync')]
        .filter(node => !node.closest('#reader-settings') &&
          node.getBoundingClientRect().width > 0);
      const footer = document.getElementById('reader-footer');
      const pill = document.getElementById('reader-offline-sync');
      const pillBox = pill.getBoundingClientRect();
      return {
        viewport: [innerWidth, innerHeight],
        coarse: matchMedia('(any-pointer: coarse)').matches,
        scroll: [document.documentElement.scrollWidth, document.documentElement.scrollHeight],
        bar: [Math.round(barBox.right), Math.round(barBox.bottom)],
        overflow: controls
          .filter(node => node.getBoundingClientRect().right > barBox.right + 0.5)
          .map(node => node.id || node.className),
        pill: {
          shown: !pill.hidden, w: Math.round(pillBox.width), h: Math.round(pillBox.height),
          state: pill.dataset.state, text: pill.textContent.trim(),
        },
        footer: [...footer.querySelectorAll('button')].map(button => {
          const box = button.getBoundingClientRect();
          const hit = getComputedStyle(button, '::after');
          return {
            id: button.id, w: Math.round(box.width), h: Math.round(box.height),
            hitW: hit.width, hitH: hit.height,
          };
        }),
      };
    })()`);
    const shoot = async (name, width, height) => {
      await offline.call('Emulation.setDeviceMetricsOverride', {
        width, height, deviceScaleFactor: 2, mobile: true,
      });
      await new Promise(resolve => setTimeout(resolve, 500));
      const { data } = await offline.call('Page.captureScreenshot', { format: 'png' });
      writeFileSync(join(dir, name), Buffer.from(data, 'base64'));
      console.log('SHOT', join(dir, name));
    };
    for (const theme of ['light', 'dark']) {
      await offline.call('Network.setCookie', {
        name: 'liseur_ui', value: theme, url: base, path: '/',
      });
      await offline.call('Page.reload', { ignoreCache: true });
      await wait(offline, probe, `the reader after switching to ${theme}`);
      await wait(offline, `document.body.dataset.readerTheme === ${JSON.stringify(theme)}`,
        `the reader's ${theme} palette`);
      await wait(offline, `(() => {
        const pill = document.getElementById('reader-offline-sync');
        return pill && !pill.hidden && pill.dataset.state === 'waiting';
      })()`, 'the waiting queue on the shot');
      await shoot(`reader-${theme}-ipad.png`, 834, 1112);
      console.log('SHOTREPORT', theme, 'ipad', JSON.stringify(await readerLayout()));
      await shoot(`reader-${theme}-phone.png`, 390, 844);
      console.log('SHOTREPORT', theme, 'phone', JSON.stringify(await readerLayout()));
    }
    await offline.call('Emulation.clearDeviceMetricsOverride');
    await offline.call('Emulation.setTouchEmulationEnabled', { enabled: false });
    await offline.call('Network.setCookie', { name: 'liseur_ui', value: 'light', url: base, path: '/' });
    await offline.call('Page.reload', { ignoreCache: true });
    await wait(offline, probe, 'the reader back at its own size');
  }
  await offline.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }))`);
  let saved = await wait(offline, `(async () => {
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
  const peak = saved;
  await offline.evaluate("document.getElementById('reader-progress-text').click()");
  await offline.evaluate("document.getElementById('reader-goto-input').value = '31'; document.getElementById('reader-goto-form').requestSubmit()");
  await wait(offline, "document.querySelector('readium-view').lastLocation.fraction < 0.4", 'offline earlier place');
  await offline.evaluate("document.getElementById('reader-sync').click()");
  await wait(offline, "document.getElementById('reader-sync-dialog').open && !document.getElementById('reader-sync-furthest-take').hidden",
    'offline manual furthest without contacting a server');
  assert.match(await offline.evaluate("document.getElementById('reader-sync-furthest').textContent"), /known on this device/);
  await offline.evaluate("document.getElementById('reader-sync-furthest-take').click()");
  await wait(offline, "!document.getElementById('reader-sync-dialog').open && document.querySelector('readium-view').lastLocation.fraction > 0.9",
    'offline historical navigation');
  await offline.evaluate("document.getElementById('reader-sync').click()");
  await wait(offline, "document.getElementById('reader-sync-dialog').open && !document.getElementById('reader-sync-back').hidden",
    'offline way back');
  await offline.evaluate("document.getElementById('reader-sync-back').click()");
  await wait(offline, "!document.getElementById('reader-sync-dialog').open && document.querySelector('readium-view').lastLocation.fraction < 0.4",
    'offline way back restores the lower current position');
  saved = await offline.evaluate(`(async () => {
    const s = await import('../assets/offline-storage.js');
    return (await s.getReadySnapshot({ bookID: ${JSON.stringify(book)} })).localPosition;
  })()`);
  assert(saved.progression < peak.progression, 'historical navigation does not force the current position to stay at the maximum');
  console.log('PASS offline historical recovery and way back work without a server');
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

  // A synced queue has nothing to say: the control goes, rather than
  // sitting on the bar as an empty capsule.
  const syncedPill = await wait(offline, `(() => {
    const pill = document.getElementById('reader-offline-sync');
    if (!pill) return null;
    const box = pill.getBoundingClientRect();
    return (pill.hidden && getComputedStyle(pill).display === 'none') ? { width: box.width } : null;
  })()`, 'the sync control retires once the queue is empty');
  assert.equal(syncedPill.width, 0, 'a hidden control takes no room on the bar');
  const syncedNarrow = await offline.evaluate(narrowBar);
  assert.equal(syncedNarrow.scrollWidth <= syncedNarrow.innerWidth, true,
    `the synced reader toolbar overflows at 320px: ${JSON.stringify(syncedNarrow)}`);
  assert.deepEqual(syncedNarrow.overflow, []);
  console.log('PASS a synced queue leaves no empty capsule on a narrow bar');
  await offline.call('Emulation.clearDeviceMetricsOverride');
  await offline.call('Emulation.setTouchEmulationEnabled', { enabled: false });

  // A control that retired must come back the moment this device owes
  // something, and offline the coordinator is not the one to say so.
  await offline.call('Network.emulateNetworkConditions', {
    offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0,
  });
  await wait(offline, 'navigator.onLine === false', 'the emulated network going away');
  await offline.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }))`);
  await wait(offline, `(() => {
    const pill = document.getElementById('reader-offline-sync');
    return pill && !pill.hidden && pill.dataset.state === 'waiting'
      && !pill.querySelector('.reader-offline-sync-retry').hidden;
  })()`, 'the sync control returning for a queued change');
  console.log('PASS a retired sync control comes back when the queue is not empty');
} catch (error) {
  console.error(error, JSON.stringify(diagnostics));
  process.exitCode = 1;
} finally {
  clearTimeout(timer);
  ws?.close();
  chrome.kill();
}
