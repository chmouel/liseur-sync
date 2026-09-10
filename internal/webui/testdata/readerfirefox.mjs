// Firefox harness for the reader, driven over WebDriver BiDi.
//
// Chromium is not the only browser that has to lay a book out, and the
// two disagree about exactly the things this reader depends on: how a
// sandboxed frame inherits a policy, and what a blocked script reports.
// This runs the same checks as the Chromium harness against Firefox, so
// "works here" means something.
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
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
  finish(2);
}, 120000);

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
// The profile is a real Firefox profile directory, and this
// harness runs several times per suite. Left behind they fill /tmp
// within a few dozen runs, and what breaks then is whatever wants space
// next, which is rarely this test. So every way out removes one.
const removeProfile = () => {
  try {
    rmSync(profile, { recursive: true, force: true, maxRetries: 3 });
  } catch {
    // A profile left behind is untidy; a cleanup that fails the run is
    // worse, and by here the answer is already printed.
  }
};

// The last resort, for the ways out that are not finish(): a crash, or a
// signal. The browser may still be writing when this runs, so it is
// best-effort by nature.
process.on('exit', () => {
  proc.kill();
  removeProfile();
});
process.on('SIGTERM', () => process.exit(1));
process.on('SIGINT', () => process.exit(1));

// finish is the ordinary way out. It waits for the browser to actually
// go before deleting its profile: asking it to quit and deleting in the
// same breath leaves the directory behind half emptied, because the
// browser writes a few more files on its way out. The wait is bounded,
// since a browser that will not close must not hold up the run.
let finishing = false;
const finish = async (code) => {
  // A second caller waits here rather than killing the browser twice.
  if (finishing) return new Promise(() => {});
  finishing = true;
  proc.kill();
  await new Promise((resolve) => {
    const giveUp = setTimeout(resolve, 5000);
    proc.once('close', () => {
      clearTimeout(giveUp);
      resolve();
    });
  });
  removeProfile();
  process.exit(code);
};

// A failed check throws, and Node's own answer to that is to exit
// straight away — which lands on the synchronous net above and races the
// browser to its own directory. Losing that race is how a failing run
// used to leave a hundred megabytes behind. So a throw takes the same
// way out as everything else, and the error still reaches the log first.
const abort = (err) => {
  console.error(err);
  finish(1);
};
process.on('uncaughtException', abort);
process.on('unhandledRejection', abort);


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
  finish(2);
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

const evalIn = async (expression) => {
  const r = await send('script.evaluate', {
    expression, target: { context }, awaitPromise: true, resultOwnership: 'none',
  });
  if (r.type === 'exception') throw new Error('eval threw: ' + JSON.stringify(r.exceptionDetails));
  return r.result.value;
};

async function waitFor(expression, description, timeout = 10000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await evalIn(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  const state = await evalIn(`JSON.stringify((() => {
    const view = document.querySelector('readium-view');
    return { location: view?.lastLocation, contents: view?.renderer?.getContents?.().map(({ doc, index }) => ({
      index, selection: String(doc.getSelection()), href: doc.documentElement.dataset.readerHref,
      frame: doc.defaultView.frameElement.getBoundingClientRect().toJSON(),
    })) };
  })())`);
  throw new Error('Timed out waiting for ' + description + ': ' + state);
}

await waitFor(`document.querySelector('readium-view')?.renderer?.getContents?.().some(({ doc }) => doc?.body) &&
  document.getElementById('reader-status')?.textContent === ''`, 'the publication to open');

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

// Readium owns the frame pool, so the probe goes through the engine's public
// API rather than depending on its frame layout.
const probe = `(() => {
  const view = document.querySelector('readium-view');
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
    frameSandboxed: (() => {
      const frame = document.querySelector('#reader-view iframe');
      return !!frame && frame.contentWindow.location.href.startsWith('blob:') &&
        frame.sandbox.contains('allow-same-origin') &&
        frame.sandbox.contains('allow-scripts') &&
        !frame.sandbox.contains('allow-top-navigation');
    })(),
    text: body ? (body.innerText || '').slice(0, 60) : '',
    colour: body ? doc.defaultView.getComputedStyle(body).color : '',
    selectionBackground: body ? doc.defaultView.getComputedStyle(body, '::selection').backgroundColor : '',
    selectionColour: body ? doc.defaultView.getComputedStyle(body, '::selection').color : '',
    selectionTokenBackground: doc
      ? doc.defaultView.getComputedStyle(doc.documentElement).getPropertyValue('--liseur-selection-bg').trim() : '',
    selectionTokenColour: doc
      ? doc.defaultView.getComputedStyle(doc.documentElement).getPropertyValue('--liseur-selection-fg').trim() : '',
    stageBackground: document.getElementById('reader-view')
      ? getComputedStyle(document.getElementById('reader-view')).backgroundColor : '',
    fraction: typeof loc?.fraction === 'number' ? +loc.fraction.toFixed(4) : -1,
    cfi: loc?.cfi || JSON.stringify(loc?.locator || ''),
    ran: doc ? !!doc.documentElement.dataset.publicationRan : null,
    svgRan: doc ? !!doc.documentElement.dataset.svgRan : null,
    extRan: doc ? typeof doc.defaultView.htmx !== 'undefined' : null,
    pageTitle: document.title,
  });
})()`;

// setReaderTheme picks a palette the way a reader does, and waits for the
// engine to restyle the open chapter rather than for a fixed delay.
async function setReaderTheme(value, colour, description) {
  await evalIn(`(() => {
    const radio = document.querySelector(
      '#reader-settings-form input[name="theme"][value="${value}"]',
    );
    radio.checked = true;
    radio.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  await waitFor(`(() => {
    const doc = document.querySelector('readium-view').renderer.getContents()[0]?.doc;
    return doc && doc.defaultView.getComputedStyle(doc.body).color.replace(/\\s/g, '') === ${JSON.stringify(colour)};
  })()`, description);
}

at('first probe');
const diag = JSON.parse(await evalIn(probe));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('the engine rendered a chapter', diag.hasDoc && diag.text.length > 10,
  `doc=${diag.hasDoc} text=${JSON.stringify(diag.text)}`);
check('the title came out of the publication', diag.title === 'Moby-Dick', diag.title);
check('reader shows the book: own chapter label',
  diag.chapter === 'Title Page', diag.chapter);
// Seeing the publication's own stylesheet means asking for the
// Publisher theme first: a fresh reader opens Light, and every theme but
// Publisher deliberately overrides the book's colours.
await setReaderTheme('original', 'rgb(17,34,51)', 'the publisher styling');
const published = JSON.parse(await evalIn(probe));
check('publication stylesheet was applied',
  published.colour.replace(/\s/g, '') === 'rgb(17,34,51)', published.colour);
await setReaderTheme('light', 'rgb(27,27,31)', 'the Light palette');
check('publication script did not run', diag.ran === false, String(diag.ran));

at('turning pages');
const seen = [];
for (let i = 0; i < 10; i++) {
  // Observe the button's navigation promise so the next click cannot overlap
  // a chapter transition on slower browser engines.
  await evalIn(`(async () => {
    const view = document.querySelector('readium-view');
    const turn = view.turn;
    let navigation;
    view.turn = function(...args) { return navigation = turn.apply(this, args); };
    try {
      document.getElementById('reader-next').click();
      if (!navigation) throw new Error('The next-page button did not navigate');
      await navigation;
    } finally { view.turn = turn; }
  })()`);
  await waitFor(`document.querySelector('readium-view').renderer.getContents().some(({ doc }) => doc?.body)`,
    'the turned page to render');
  const now = JSON.parse(await evalIn(probe));
  seen.push({ page: i + 2, chapter: now.chapter, progress: now.progress, fraction: now.fraction, cfi: now.cfi });
}
console.log('page turns:', JSON.stringify(seen, null, 1));
const distinct = new Set(seen.map((p) => p.chapter + '|' + p.fraction + '|' + p.cfi)).size;
check('the book pages past page 2', distinct >= 6, `${distinct} distinct pages in 10 turns`);
check('the reader leaves the first chapter',
  seen.some((p) => p.chapter !== diag.chapter), seen.map((p) => p.chapter).join(' '));

// Same appearance round-trip as the Chromium harness: the theme owns the
// publication colours, stage background and live selection treatment, all
// of which must switch without reopening the book.
at('appearance settings');
for (const [value, label, colour, background, selectionTokenBackground, selectionTokenColour,
  selectionBackground, selectionColour] of [
  ['light', 'Light', 'rgb(27,27,31)', 'rgb(255,255,255)', '#cfe3ff', '#1b1b1f', 'rgb(207,227,255)', 'rgb(27,27,31)'],
  ['sepia', 'Sepia', 'rgb(91,70,54)', 'rgb(246,236,217)', '#d8c2a0', '#4a392c', 'rgb(216,194,160)', 'rgb(74,57,44)'],
  ['dark', 'Dark', 'rgb(207,207,212)', 'rgb(32,33,36)', '#4f6fbe', '#f5f7ff', 'rgb(79,111,190)', 'rgb(245,247,255)'],
  ['tokyo-night', 'Tokyo Night', 'rgb(192,202,245)', 'rgb(26,27,38)', '#445c9b', '#eef2ff', 'rgb(68,92,155)', 'rgb(238,242,255)'],
  ['rose-pine', 'Rosé Pine', 'rgb(224,222,244)', 'rgb(25,23,36)', '#5c4a88', '#f7f4ff', 'rgb(92,74,136)', 'rgb(247,244,255)'],
  ['black', 'Black', 'rgb(171,171,174)', 'rgb(0,0,0)', '#375f9d', '#f5f7ff', 'rgb(55,95,157)', 'rgb(245,247,255)'],
]) {
  await setReaderTheme(value, colour, 'the ' + label + ' publication theme');
  const themed = JSON.parse(await evalIn(probe));
  check(`the ${label} theme restyles the publication`,
    themed.colour.replace(/\s/g, '') === colour, themed.colour);
  check(`the ${label} theme colors the reader stage`,
    themed.stageBackground.replace(/\s/g, '') === background,
    themed.stageBackground);
  check(`the ${label} theme updates selection tokens`,
    themed.selectionTokenBackground !== '' &&
      themed.selectionTokenBackground.toLowerCase() === selectionTokenBackground &&
      themed.selectionTokenColour.toLowerCase() === selectionTokenColour,
    JSON.stringify({
      bg: themed.selectionTokenBackground,
      fg: themed.selectionTokenColour,
    }));
  check(`the ${label} theme styles browser text selection`,
    themed.selectionBackground.replace(/\s/g, '') === selectionBackground &&
      themed.selectionColour.replace(/\s/g, '') === selectionColour,
    JSON.stringify({
      bg: themed.selectionBackground,
      fg: themed.selectionColour,
    }));
}
await evalIn(`document.getElementById('reader-settings-reset').click()`);
await waitFor(`(() => {
  const doc = document.querySelector('readium-view').renderer.getContents()[0]?.doc;
  return doc && doc.defaultView.getComputedStyle(doc.body).color.replace(/\\s/g, '') === 'rgb(27,27,31)';
})()`, 'the default Light palette');
const unthemed = JSON.parse(await evalIn(probe));
// Reset restores the defaults, and the default palette is Light.
check('reset restores the default Light palette',
  unthemed.colour.replace(/\s/g, '') === 'rgb(27,27,31)', unthemed.colour);

// A risk-focused slice of the Chromium chrome/tap matrix. Gecko is
// where this can differ: frameElement coordinates, PointerEvent
// delivery inside a blob chapter, `:has()` in the chrome CSS, and
// caretPositionFromPoint standing in for Blink's caretRangeFromPoint
// when the reader decides whether a click landed on a word.
at('auto-hiding chrome');
// Gesture checks need enough text for both turns to stay in one chapter.
// Chapter transitions were exercised above; use the fixture's long chapter
// here so pointer handling is measured independently of frame replacement.
await evalIn(`document.querySelector('readium-view').goTo(1)`);
const chromeState = () => evalIn(`JSON.stringify({
  state: document.body.dataset.readerChrome,
  bar: getComputedStyle(document.querySelector('.reader-bar')).opacity,
})`);
const defaultChrome = JSON.parse(await chromeState());
const defaultAutoHide = await evalIn(
  `document.querySelector('#reader-settings-form input[name="autohide"]').checked`,
);
check('the chrome stays visible by default',
  defaultAutoHide === false && defaultChrome.state === 'visible' && defaultChrome.bar === '1',
  JSON.stringify({ defaultAutoHide, ...defaultChrome }));
await evalIn(`(() => {
  const box = document.querySelector('#reader-settings-form input[name="autohide"]');
  box.checked = true;
  box.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`);
const ffTap = (where, kind) => evalIn(`(() => {
  const doc = document.querySelector('readium-view').renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const xs = { left: 6, right: window.innerWidth - 6 };
  const x = xs['${where}'] - box.left, y = window.innerHeight / 2 - box.top;
  const at = (type, extra) => doc.body.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, button: 0, pointerType: '${kind}', clientX: x, clientY: y, ...extra,
  }));
  at('pointerdown');
  at('pointerup');
  doc.body.dispatchEvent(new win.MouseEvent('click', {
    bubbles: true, button: 0, detail: 1, clientX: x, clientY: y,
  }));
  return true;
})()`);

await new Promise((r) => setTimeout(r, 2600));
const ffParked = JSON.parse(await chromeState());
check('the chrome steps aside while reading',
  ffParked.state === 'hidden' && ffParked.bar === '0', JSON.stringify(ffParked));

await evalIn(`(() => {
  document.dispatchEvent(new PointerEvent('pointermove', {
    bubbles: true, clientX: window.innerWidth - 4, clientY: window.innerHeight / 2,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const ffPassedSide = JSON.parse(await chromeState());
check('moving along the side leaves the chrome hidden',
  ffPassedSide.state === 'hidden' && ffPassedSide.bar === '0', JSON.stringify(ffPassedSide));

await evalIn(`(() => {
  document.dispatchEvent(new PointerEvent('pointermove', {
    bubbles: true, clientX: window.innerWidth / 2, clientY: 4,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const ffReached = JSON.parse(await chromeState());
check('reaching for the top of the window brings the chrome back',
  ffReached.state === 'visible' && ffReached.bar === '1', JSON.stringify(ffReached));

await new Promise((r) => setTimeout(r, 2600));
const ffBefore = JSON.parse(await evalIn(probe));
await ffTap('right', 'touch');
await waitFor(`document.querySelector('readium-view').lastLocation.fraction > ${ffBefore.fraction} &&
  document.querySelector('readium-view').renderer.getContents().some(({ doc }) => doc?.body)`, 'the touch page turn');
const ffForward = JSON.parse(await evalIn(probe));
check('a tap on the right of the text turns the page',
  ffForward.fraction > ffBefore.fraction,
  `${ffBefore.fraction} -> ${ffForward.fraction}`);

// The mouse path is the one that has to ask Gecko where the caret is.
// Let the 700ms suppression of synthetic mouse events after touch expire.
await new Promise(resolve => setTimeout(resolve, 750));
await ffTap('right', 'mouse');
await waitFor(`document.querySelector('readium-view').lastLocation.fraction > ${ffForward.fraction} &&
  document.querySelector('readium-view').renderer.getContents().some(({ doc }) => doc?.body)`, 'the mouse page turn');
const ffMouse = JSON.parse(await evalIn(probe));
check('a mouse click on the right of the text turns the page',
  ffMouse.fraction > ffForward.fraction,
  `${ffForward.fraction} -> ${ffMouse.fraction}`);

await evalIn(`(() => {
  const doc = document.querySelector('readium-view').renderer.getContents()[0].doc;
  doc.getSelection().selectAllChildren(doc.body);
  const box = doc.defaultView.frameElement.getBoundingClientRect();
  doc.body.dispatchEvent(new MouseEvent('click', {
    bubbles: true, button: 0, detail: 1,
    clientX: window.innerWidth - 6 - box.left,
    clientY: window.innerHeight / 2 - box.top,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 1200));
const ffSelected = JSON.parse(await evalIn(probe));
check('a click while text is selected is not a page turn',
  ffSelected.fraction === ffMouse.fraction,
  `${ffMouse.fraction} -> ${ffSelected.fraction}`);
await evalIn(`(() => {
  document.querySelector('readium-view').renderer.getContents()[0]
    .doc.getSelection().removeAllRanges();
  document.getElementById('reader-settings-form').autohide.checked = false;
  document.getElementById('reader-settings-form').autohide
    .dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));

// The same hostile battery as the Chromium harness, judged by Firefox.
at('hostile chapter');
await evalIn(`document.querySelector('readium-view').goTo(1)`);
await new Promise((r) => setTimeout(r, 900));
const hostile = JSON.parse(await evalIn(probe));
check('the inline script did not run', hostile.ran === false, String(hostile.ran));
check('the SVG script did not run', hostile.svgRan === false, String(hostile.svgRan));
check('the same-origin external script did not run',
  hostile.extRan === false, String(hostile.extRan));
check('the publication could not reach the parent page',
  !String(hostile.pageTitle).includes('pwned'), hostile.pageTitle);

if (process.env.SMOKE_SHOT) {
  const shot = await send('browsingContext.captureScreenshot', { context });
  writeFileSync(process.env.SMOKE_SHOT, Buffer.from(shot.data, 'base64'));
}

// Chromium says out loud that it refused to run the publication's
// script. Firefox reports the same refusal as a policy violation, and
// BiDi's log channel carries console calls and script errors only — a
// violation never reaches it. So state the refusal structurally: the
// chapter frame is sandboxed, the
// reader stripped the publication's script elements, and the page CSP
// — inherited by every blob chapter — refuses whatever stripping might
// miss. What can be observed from here is that the script did not run.
check('the chapter frame is sandboxed', diag.frameSandboxed, String(diag.frameSandboxed));
check('the browser refused to run the publication', diag.ran === false,
  String(diag.ran));
const unexpected = consoleErrors.filter((e) => !/Content-Security-Policy|Blocked script/i.test(e));
if (unexpected.length) console.log('console errors:\n  ' + unexpected.join('\n  '));
check('no console errors', unexpected.length === 0);

ws.close();
await finish(fail.length ? 1 : 0);
