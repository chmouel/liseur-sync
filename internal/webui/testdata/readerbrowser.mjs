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
// Two-origin mode (ADR-0007 phase 3): the reader hostname has to resolve
// somewhere, and it is not in DNS. Chrome will map it for us.
const mapHost = process.env.SMOKE_MAP;
const detached = process.env.SMOKE_DETACHED === '1';
const withAnnotations = process.env.SMOKE_ANNOTATIONS === '1';
// NaN-guard mode (the position-jumps fix): synthetic relocate events
// with a NaN then a finite fraction, watching whether the reader pushes.
const nan = process.env.SMOKE_NAN === '1';
// Session mode (ADR-0030): hides and shows the tab with a skewed clock,
// watching what the reader posts to /v1/sessions.
const sessions = process.env.SMOKE_SESSIONS === '1';
// How many pages the fixture has, counted from the archive by the Go
// side with Readium's recipe (ADR-0032). The footer has to agree, or the
// browser and the app are naming different pages again.
const expectedPages = Number(process.env.SMOKE_PAGES) || 0;

const profile = mkdtempSync(join(tmpdir(), 'smoke-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  ...(mapHost ? [`--host-resolver-rules=MAP ${mapHost} 127.0.0.1`] : []),
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
    if (!/favicon/.test(msg.params.entry.url || '')) consoleErrors.push(msg.params.entry.text + ' @ ' + (msg.params.entry.url || ''));
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
await new Promise((r) => setTimeout(r, 8000));

// In two-origin mode the page under test is not the one navigated to:
// the main origin authorised the book and redirected here with the
// credential in the fragment. Everything after this point is the same
// set of checks, which is the point — a reader that behaves differently
// on the second hostname is not the same reader.

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

// The NaN guard is a self-contained probe: it does not want the render,
// page-turn and annotation battery below, so it runs and exits here.
if (nan) {
  await nanGuard(evalIn, check);
  ws.close();
  proc.kill();
  process.exit(fail.length ? 1 : 0);
}
if (sessions) {
  await sessionGuard(evalIn, check);
  ws.close();
  proc.kill();
  process.exit(fail.length ? 1 : 0);
}

if (detached) {
  const where = await evalIn('JSON.stringify({href: location.href, cookie: document.cookie})');
  const at = JSON.parse(where);
  console.log('detached at:', at.href, 'cookie:', JSON.stringify(at.cookie));
  check('the reader was handed off to the other origin',
    at.href.includes(mapHost.split(':')[0]), at.href);
  // The credential must not survive in the address bar, the history
  // entry, or anything the reader might paste to somebody.
  check('the credential was erased from the URL', !at.href.includes('#'), at.href);
  // The whole point of the second hostname: nothing to steal on it.
  check('the reader origin holds no cookie', at.cookie === '', at.cookie);
}

// The engine (foliate-js, ADR-0012) keeps its frames inside closed
// shadow roots, so the page cannot reach them by DOM query — and
// neither can anything else on the page, which is part of the point.
// The probe therefore goes through the engine's public API: the
// chapter documents via renderer.getContents(), the position via
// lastLocation.
const probe = `(() => {
  const view = document.querySelector('foliate-view');
  const contents = view?.renderer?.getContents?.() ?? [];
  const doc = contents[0]?.doc;
  const body = doc?.body;
  const loc = view?.lastLocation;
  return JSON.stringify({
    status: document.getElementById('reader-status')?.textContent,
    chapter: document.getElementById('reader-chapter')?.textContent,
    progress: document.getElementById('reader-progress-text')?.textContent,
    page: document.getElementById('reader-page')?.textContent,
    footerShown: (() => {
      const f = document.getElementById('reader-footer');
      if (!f) return false;
      const cs = getComputedStyle(f);
      return cs.display !== 'none' && cs.visibility !== 'hidden' && f.getClientRects().length > 0;
    })(),
    footerMode: document.body.dataset.readerFooter,
    title: document.getElementById('reader-title-text')?.textContent,
    hasDoc: !!doc,
    frameReachable: !!document.querySelector('#reader-view iframe'),
    text: body ? (body.innerText || '').slice(0, 60) : '',
    colour: body ? doc.defaultView.getComputedStyle(body).color : '',
    stageBackground: document.getElementById('reader-view')
      ? getComputedStyle(document.getElementById('reader-view')).backgroundColor : '',
    fraction: typeof loc?.fraction === 'number' ? +loc.fraction.toFixed(4) : -1,
    cfi: loc?.cfi || '',
    ran: doc ? !!doc.documentElement.dataset.publicationRan : null,
    svgRan: doc ? !!doc.documentElement.dataset.svgRan : null,
    extRan: doc ? typeof doc.defaultView.htmx !== 'undefined' : null,
    overlay: (() => {
      const o = contents[0]?.overlayer;
      if (!o) return null;
      const g = o.element.querySelector('g');
      return {
        rects: o.element.querySelectorAll('rect').length,
        fill: g ? g.getAttribute('fill') : '',
      };
    })(),
    pageTitle: document.title,
    fontSize: body ? doc.defaultView.getComputedStyle(body).fontSize : '',
    wrapWidth: body && body.firstElementChild
      ? doc.defaultView.getComputedStyle(body.firstElementChild).width : '',
    wrapMaxWidth: body && body.firstElementChild && body.firstElementChild.firstElementChild
      ? doc.defaultView.getComputedStyle(body.firstElementChild.firstElementChild).maxWidth : '',
  });
})()`;

const diag = JSON.parse(await evalIn(probe));
console.log('diag:', JSON.stringify(diag));

check('no error banner', !diag.status, diag.status);
check('the engine rendered a chapter', diag.hasDoc && diag.text.length > 10,
  `doc=${diag.hasDoc} text=${JSON.stringify(diag.text)}`);
check('the title came out of the publication', diag.title === 'Moby-Dick', diag.title);
check('reader shows the book: own chapter label',
  diag.chapter === 'Title Page', diag.chapter);
// The footer's page is a Readium position — the same page the app names
// for the same spot (ADR-0032) — and it is a page of the book, not a
// fabrication: n is within m.
const pageOf = (t) => {
  const m = /^(\d+) of (\d+)$/.exec(t || '');
  return m ? { n: +m[1], of: +m[2] } : null;
};
check('the footer shows a page of the book',
  pageOf(diag.page) && pageOf(diag.page).n >= 1 && pageOf(diag.page).n <= pageOf(diag.page).of,
  diag.page);
if (expectedPages) {
  check('the page count is the book\'s positions, as the app counts them',
    pageOf(diag.page)?.of === expectedPages,
    `${diag.page} want m=${expectedPages}`);
}
check('the footer is on screen with the book', diag.footerShown, String(diag.footerShown));
// The chapter frame lives in a closed shadow root: nothing on the
// page — including anything a publication managed to smuggle onto it —
// can reach in by DOM query.
check('the chapter frame is not reachable from the page',
  diag.frameReachable === false, String(diag.frameReachable));

// The publication's own stylesheet is a separate zip entry. The engine
// rewrites the link to a blob URL, which the page CSP has to permit.
check('publication stylesheet was applied',
  diag.colour.replace(/\s/g, '') === 'rgb(17,34,51)', diag.colour);

// The publication's script must not have run. It sets a data attribute
// on the documentElement; the reader strips script elements from every
// resource and the page CSP — inherited by each blob chapter — refuses
// what stripping might miss. This is the promise the vendored engine
// had to keep.
check('publication script did not run', diag.ran === false, String(diag.ran));

// Annotations (ADR-0028), when the harness seeded them: the highlight
// whose CFI anchors in this very chapter must have actually drawn —
// rects in the overlayer SVG, filled with the palette's green, never
// raw CSS from the wire — and the two that cannot draw must be listed
// in the sidebar rather than reported as errors.
if (withAnnotations) {
  check('a synced highlight draws over the text',
    !!diag.overlay && diag.overlay.rects > 0 && diag.overlay.fill === '#81c784',
    JSON.stringify(diag.overlay));
  const anns = JSON.parse(await evalIn(`JSON.stringify((() => {
    const panel = document.getElementById('reader-annotations');
    return {
      hidden: panel ? panel.hidden : null,
      entries: [...(panel?.querySelectorAll('.reader-ann-text') ?? [])]
        .map((s) => s.textContent),
    };
  })())`));
  check('the sidebar lists the note',
    anns.hidden === false &&
      anns.entries.some((t) => t.includes('A thought about the whale')),
    JSON.stringify(anns));
  check('an unanchorable highlight degrades to a sidebar entry',
    anns.entries.some((t) => t.includes('an unanchored highlight')),
    JSON.stringify(anns));
  check('the drawn highlight is not duplicated in the sidebar',
    !anns.entries.some((t) => t.includes('title page, absolut')),
    JSON.stringify(anns));
}

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
for (let i = 0; i < 10; i++) {
  await evalIn(`document.getElementById('reader-next').click()`);
  await new Promise((r) => setTimeout(r, 900));
  const now = JSON.parse(await evalIn(probe));
  seen.push({ page: i + 2, chapter: now.chapter, progress: now.progress, fraction: now.fraction, cfi: now.cfi, loc: now.page });
}
console.log('page turns:', JSON.stringify(seen, null, 1));

// Ten turns, ten different places. A book that lays itself out too wide
// still turns the page — it just turns onto blank column after blank
// column — so the count that matters is of distinct pages, not of clicks.
const distinct = new Set(seen.map((p) => p.chapter + '|' + p.fraction + '|' + p.cfi)).size;
check('the book pages past page 2', distinct >= 6,
  `${distinct} distinct pages in 10 turns`);
check('the reader leaves the first chapter',
  seen.some((p) => p.chapter !== diag.chapter),
  seen.map((p) => p.chapter).join(' '));
// Turning forward never sends the page number backwards, and it does
// move: a counter that stays put is not counting pages.
const locs = seen.map((p) => pageOf(p.loc)).filter(Boolean);
check('the page number counts forward with the turns',
  locs.length === seen.length &&
    locs.every((l, i) => i === 0 || l.n >= locs[i - 1].n) &&
    locs[locs.length - 1].n > locs[0].n &&
    locs.every((l) => l.of === locs[0].of),
  seen.map((p) => p.loc).join(' '));

// A click on the footer changes what its middle says and nothing else:
// it is not a stage surface, so the page under it stays where it was.
{
  const before = JSON.parse(await evalIn(probe));
  await evalIn(`document.getElementById('reader-footer').click()`);
  await new Promise((r) => setTimeout(r, 300));
  const after = JSON.parse(await evalIn(probe));
  check('a click on the footer cycles the middle slot',
    before.footerMode === 'chapter' && after.footerMode === 'time-chapter' &&
      /left in chapter$/.test(after.chapter || ''),
    `${before.footerMode} -> ${after.footerMode}: ${JSON.stringify(after.chapter)}`);
  check('a click on the footer turns no page',
    before.cfi === after.cfi && before.fraction === after.fraction && before.page === after.page,
    `${before.page} -> ${after.page}`);
  // Twice more and it is back to the chapter's name.
  await evalIn(`document.getElementById('reader-footer').click()`);
  await evalIn(`document.getElementById('reader-footer').click()`);
  await evalIn(`document.getElementById('reader-footer').click()`);
  await new Promise((r) => setTimeout(r, 300));
  const round = JSON.parse(await evalIn(probe));
  check('the footer ring comes back round to the chapter',
    round.footerMode === 'chapter' && round.chapter === before.chapter,
    `${round.footerMode}: ${JSON.stringify(round.chapter)}`);
}

// Clicking percentage opens the go-to dialog and lets the reader jump to a percentage.
{
  await evalIn(`document.getElementById('reader-progress-text').click()`);
  const dialogOpen = await evalIn(`document.getElementById('reader-goto')?.open`);
  check('clicking percentage opens the goto dialog', dialogOpen === true);
  const title = await evalIn(`document.getElementById('reader-goto-title')?.textContent`);
  check('dialog title is percentage', title === 'Go to percentage');
  // Jump to 50%
  await evalIn(`(() => {
    const input = document.getElementById('reader-goto-input');
    input.value = '50';
    document.getElementById('reader-goto-form').dispatchEvent(new Event('submit', { cancelable: true }));
  })()`);
  await new Promise((r) => setTimeout(r, 600));
  const jumped = JSON.parse(await evalIn(probe));
  check('percentage jump reached ~50%', Math.abs(jumped.fraction - 0.5) < 0.15, `${jumped.fraction}`);
}

// Clicking page number opens the go-to dialog and lets the reader jump to a page.
{
  await evalIn(`document.getElementById('reader-page').click()`);
  const dialogOpen = await evalIn(`document.getElementById('reader-goto')?.open`);
  check('clicking page opens the goto dialog', dialogOpen === true);
  const title = await evalIn(`document.getElementById('reader-goto-title')?.textContent`);
  check('dialog title is page', title === 'Go to page');
  // Jump to page 1
  await evalIn(`(() => {
    const input = document.getElementById('reader-goto-input');
    input.value = '1';
    document.getElementById('reader-goto-form').dispatchEvent(new Event('submit', { cancelable: true }));
  })()`);
  await new Promise((r) => setTimeout(r, 600));
  const jumped = JSON.parse(await evalIn(probe));
  check('page jump reached page 1', /^(1 of|1\b)/.test(jumped.page || ''), `${jumped.page}`);
}

// Going back has to work too, or the reader is a one-way trip.
await evalIn(`document.getElementById('reader-prev').click()`);
await new Promise((r) => setTimeout(r, 900));
const back = JSON.parse(await evalIn(probe));
const wasAt = seen[seen.length - 1];
check('the book pages backwards',
  back.chapter !== wasAt.chapter || back.fraction !== wasAt.fraction ||
  back.cfi !== wasAt.cfi,
  `${wasAt.chapter} @${wasAt.fraction} -> ${back.chapter} @${back.fraction}`);

// The appearance settings must reach inside the publication: choose each
// named palette and verify both chapter text and the stage background. This
// also proves the user stylesheet survives the engine's page lifecycle
// rather than styling a page that is repainted away.
for (const [value, label, colour, background] of [
  ['tokyo-night', 'Tokyo Night', 'rgb(192,202,245)', 'rgb(26,27,38)'],
  ['rose-pine', 'Rosé Pine', 'rgb(224,222,244)', 'rgb(25,23,36)'],
]) {
  await evalIn(`(() => {
    const radio = document.querySelector(
      '#reader-settings-form input[name="theme"][value="${value}"]',
    );
    radio.checked = true;
    radio.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  await new Promise((r) => setTimeout(r, 700));
  const themed = JSON.parse(await evalIn(probe));
  check(`the ${label} theme restyles the publication`,
    themed.colour.replace(/\s/g, '') === colour, themed.colour);
  check(`the ${label} theme colors the reader stage`,
    themed.stageBackground.replace(/\s/g, '') === background,
    themed.stageBackground);
  const saved = await evalIn(`localStorage.getItem('liseur.reader.settings')`);
  check(`${label} settings persist in the browser`,
    typeof saved === 'string' && saved.includes(`"theme":"${value}"`),
    String(saved));
}
await evalIn(`document.getElementById('reader-settings-reset').click()`);
await new Promise((r) => setTimeout(r, 700));
const unthemed = JSON.parse(await evalIn(probe));
check('reset restores the publisher styling',
  unthemed.colour.replace(/\s/g, '') === 'rgb(17,34,51)', unthemed.colour);

// In scroll mode the text runs under the bottom edge, so the footer
// goes: a line drawn there would print itself over the book. It is back
// the moment the pages are.
{
  const setFlow = (value) => evalIn(`(() => {
    const radio = document.querySelector(
      '#reader-settings-form input[name="flow"][value="${value}"]',
    );
    radio.checked = true;
    radio.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  await setFlow('scrolled');
  await new Promise((r) => setTimeout(r, 700));
  const scrolled = JSON.parse(await evalIn(probe));
  check('the footer leaves in scroll mode', !scrolled.footerShown,
    String(scrolled.footerShown));
  await setFlow('paginated');
  await new Promise((r) => setTimeout(r, 900));
  const paged = JSON.parse(await evalIn(probe));
  check('the footer returns with the pages', paged.footerShown && pageOf(paged.page),
    `${paged.footerShown} ${paged.page}`);
}

// A click on any blank margin is a page turn: aimed at the right edge
// of the stage — which the engine's own margin occupies, retargeted to
// the foliate-view host — the book moves forward.
const beforeClick = JSON.parse(await evalIn(probe));
await evalIn(`(() => {
  const stage = document.getElementById('reader-view');
  const box = stage.getBoundingClientRect();
  const target = document.elementFromPoint(box.right - 4, box.top + box.height / 2) || stage;
  target.dispatchEvent(new MouseEvent('click', { bubbles: true, clientX: box.right - 4, clientY: box.top + box.height / 2 }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const afterClick = JSON.parse(await evalIn(probe));
check('a click on the blank margin turns the page',
  afterClick.fraction > beforeClick.fraction,
  `${beforeClick.fraction} -> ${afterClick.fraction}`);

// The reading keys: space turns forward, shift+space turns back — and
// they must keep working after a click in the text, when the chapter
// frame owns the keyboard and only the per-document listener hears it.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const spaced = JSON.parse(await evalIn(probe));
check('space turns the page', spaced.fraction > afterClick.fraction,
  `${afterClick.fraction} -> ${spaced.fraction}`);
await evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  doc.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', shiftKey: true, bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const unspaced = JSON.parse(await evalIn(probe));
check('shift+space inside the chapter turns back',
  unspaced.fraction < spaced.fraction,
  `${spaced.fraction} -> ${unspaced.fraction}`);

// "?" summons the keyboard help; while it is up the reading keys are
// parked (space must not turn the page under the dialog); Escape puts
// it away — the native dialog owns that.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '?', shiftKey: true, bubbles: true }));
})()`);
const helpOpen = await evalIn(`document.getElementById('reader-help').open`);
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 600));
const heldStill = JSON.parse(await evalIn(probe));
await evalIn(`document.getElementById('reader-help').close()`);
const helpClosed = await evalIn(`!document.getElementById('reader-help').open`);
check('"?" opens the keyboard help', helpOpen === true, String(helpOpen));
check('the reading keys are parked while help is up',
  heldStill.fraction === unspaced.fraction,
  `${unspaced.fraction} -> ${heldStill.fraction}`);
check('the help closes', helpClosed === true, String(helpClosed));

// The contents drawer: "t" opens it, its entries come from the book's
// own nav document (nested entry included), a click on one jumps
// there, and Escape puts the drawer away.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 't', bubbles: true }));
})()`);
const toc = JSON.parse(await evalIn(`JSON.stringify((() => {
  const panel = document.getElementById('reader-toc');
  return {
    open: !panel.hidden,
    entries: [...panel.querySelectorAll('a')].map((a) => a.textContent),
  };
})())`));
check('"t" opens the contents drawer', toc.open === true, String(toc.open));
check('the drawer lists the book: own contents',
  toc.entries.includes('Loomings') && toc.entries.includes('The Spouter-Inn'),
  toc.entries.join(' | '));
const beforeJump = JSON.parse(await evalIn(probe));
await evalIn(`(() => {
  for (const a of document.getElementById('reader-toc').querySelectorAll('a')) {
    if (a.textContent === 'Chowder') { a.click(); return; }
  }
})()`);
await new Promise((r) => setTimeout(r, 1200));
const jumped = JSON.parse(await evalIn(probe));
const tocGone = await evalIn(`document.getElementById('reader-toc').hidden`);
check('a contents entry jumps there',
  jumped.chapter === 'Chowder' && jumped.cfi !== beforeJump.cfi,
  `${beforeJump.chapter} -> ${jumped.chapter}`);
check('the jump closes the drawer', tocGone === true, String(tocGone));
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 't', bubbles: true }));
})()`);
const currentMarked = await evalIn(
  `document.querySelector('#reader-toc a.current')?.textContent ?? ''`);
check('the drawer marks where the reader is', currentMarked === 'Chowder', currentMarked);
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
})()`);
const tocClosed = await evalIn(`document.getElementById('reader-toc').hidden`);
check('Escape closes the drawer', tocClosed === true, String(tocClosed));

// The first chapter carries the full hostile battery: an inline script,
// a script inside an SVG island, an external script pointing at a real
// same-origin file, and an attempt to retitle the parent page. Jump
// there and confirm every one of them came to nothing — the transform
// strips them, and the nonce-gated CSP refuses whatever a stripper
// might ever miss.
await evalIn(`document.querySelector('foliate-view').goTo(1)`);
await new Promise((r) => setTimeout(r, 900));
const hostile = JSON.parse(await evalIn(probe));
check('the inline script did not run', hostile.ran === false, String(hostile.ran));
check('the SVG script did not run', hostile.svgRan === false, String(hostile.svgRan));
check('the same-origin external script did not run',
  hostile.extRan === false, String(hostile.extRan));
check('the publication could not reach the parent page',
  !String(hostile.pageTitle).includes('pwned'), hostile.pageTitle);

// The font-size slider at full stretch has to mean it: the chapter's
// type is really 250% of the 16px default, and the publication's own
// width caps — a wrapper's width and a nested wrapper's max-width,
// which would otherwise keep the old, short lines pinned to the left
// of a much wider column — are lifted, so the bigger type gets the
// page. All of these were user reports before they were checks.
check('a book that caps its own width starts capped',
  hostile.wrapMaxWidth === '480px' && hostile.wrapWidth === '480px',
  `max-width ${hostile.wrapMaxWidth}, width ${hostile.wrapWidth}`);
await evalIn(`(() => {
  const slider = document.querySelector('#reader-settings-form input[name="size"]');
  slider.value = '250';
  slider.dispatchEvent(new Event('input', { bubbles: true }));
})()`);
await new Promise((r) => setTimeout(r, 900));
const sized = JSON.parse(await evalIn(probe));
check('the font-size slider actually sizes the type',
  sized.fontSize === '40px', sized.fontSize);
check("the slider lifts the publication's own width caps",
  sized.wrapMaxWidth === 'none' && sized.wrapWidth !== '480px',
  `max-width ${sized.wrapMaxWidth}, width ${sized.wrapWidth}`);

// The chrome (top bar and the two arrows) steps aside while nobody is
// reaching for it, and what replaces it is the tap model: the sides of
// the window turn the page wherever they are clicked — including on the
// text, which is the only thing a phone has left once the arrows have
// faded — and the middle brings the bar back.
const chromeState = () => evalIn(`JSON.stringify({
  state: document.body.dataset.readerChrome || 'visible',
  bar: getComputedStyle(document.querySelector('.reader-bar')).opacity,
  arrow: getComputedStyle(document.getElementById('reader-next')).opacity,
  footer: getComputedStyle(document.getElementById('reader-footer')).display,
})`);
const idle = () => new Promise((r) => setTimeout(r, 3000));
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
// Tap the chapter document itself, the way a pointer really does it:
// pointerdown, pointerup, then the click, in the frame's own
// coordinates, so the page turned on is the one the reader is looking
// at. A mouse tap waits out the double-click interval before it counts,
// which is why every check below settles for longer than that.
const tapChapter = (where, kind = 'mouse', drift = 0) => evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const xs = { left: 6, middle: window.innerWidth / 2, right: window.innerWidth - 6 };
  const x = xs['${where}'] - box.left, y = window.innerHeight / 2 - box.top;
  const at = (type, extra) => doc.body.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, button: 0, pointerType: '${kind}', clientX: x, clientY: y, ...extra,
  }));
  at('pointerdown');
  at('pointerup', { clientX: x + ${drift} });
  doc.body.dispatchEvent(new win.MouseEvent('click', {
    bubbles: true, button: 0, detail: 1, clientX: x + ${drift}, clientY: y,
  }));
  return true;
})()`);
const settle = () => new Promise((r) => setTimeout(r, 1200));

const settingsOpened = await evalIn(`(() => {
  const panel = document.getElementById('reader-settings');
  panel.querySelector('summary').click();
  return panel.open;
})()`);
check('the Aa menu opens', settingsOpened === true, String(settingsOpened));
await tapChapter('middle', 'touch');
await new Promise((r) => setTimeout(r, 400));
const settingsClosed = await evalIn(`!document.getElementById('reader-settings').open`);
check('clicking the book closes the Aa menu', settingsClosed === true, String(settingsClosed));

await idle();
const parked = JSON.parse(await chromeState());
check('the chrome steps aside while reading',
  parked.state === 'hidden' && parked.bar === '0' && parked.arrow === '0',
  JSON.stringify(parked));
// The footer is the one piece of chrome that stays: the figures are
// what a reader glances at mid-page, bars or no bars.
check('the footer stays while the chrome is hidden',
  parked.footer !== 'none', JSON.stringify(parked));

await evalIn(`(() => {
  document.dispatchEvent(new PointerEvent('pointermove', {
    bubbles: true, clientX: window.innerWidth - 4, clientY: window.innerHeight / 2,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const passedSide = JSON.parse(await chromeState());
check('moving along the side leaves the chrome hidden',
  passedSide.state === 'hidden' && passedSide.bar === '0', JSON.stringify(passedSide));

await evalIn(`(() => {
  document.dispatchEvent(new PointerEvent('pointermove', {
    bubbles: true, clientX: window.innerWidth / 2, clientY: 4,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400)); // the bar fades in
const reached = JSON.parse(await chromeState());
check('reaching for the top of the window brings the chrome back',
  reached.state === 'visible' && reached.bar === '1', JSON.stringify(reached));

await idle();
const beforeTap = JSON.parse(await evalIn(probe));
await tapChapter('right');
await settle();
const tappedForward = JSON.parse(await evalIn(probe));
check('a tap on the right of the text turns the page',
  tappedForward.fraction > beforeTap.fraction,
  `${beforeTap.fraction} -> ${tappedForward.fraction}`);
await tapChapter('left');
await settle();
const tappedBack = JSON.parse(await evalIn(probe));
check('a tap on the left of the text turns it back',
  tappedBack.fraction < tappedForward.fraction,
  `${tappedForward.fraction} -> ${tappedBack.fraction}`);

// A finger has no double-click to wait for, so it turns at once.
await tapChapter('right', 'touch');
await new Promise((r) => setTimeout(r, 900));
const touched = JSON.parse(await evalIn(probe));
check('a touch tap turns the page without waiting',
  touched.fraction > tappedBack.fraction,
  `${tappedBack.fraction} -> ${touched.fraction}`);
await tapChapter('left', 'touch');
await settle();
const touchedBack = JSON.parse(await evalIn(probe));

await idle();
await tapChapter('middle');
await settle();
const middled = JSON.parse(await chromeState());
const stillThere = JSON.parse(await evalIn(probe));
check('a tap in the middle of the text asks for the chrome',
  middled.state === 'visible', JSON.stringify(middled));
check('the middle of the text never turns the page',
  stillThere.fraction === touchedBack.fraction,
  `${touchedBack.fraction} -> ${stillThere.fraction}`);

// A tap is only a tap when it is nothing else: a drag, a double-click
// on a word, a control, or a live selection all mean the reader is
// doing something with the text, and the page must stay where it is.
await tapChapter('right', 'mouse', 40);
await settle();
const afterDrag = JSON.parse(await evalIn(probe));
check('a drag across the text is not a page turn',
  afterDrag.fraction === stillThere.fraction,
  `${stillThere.fraction} -> ${afterDrag.fraction}`);

await evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const x = window.innerWidth - 6 - box.left, y = window.innerHeight / 2 - box.top;
  const opts = { bubbles: true, button: 0, clientX: x, clientY: y };
  doc.body.dispatchEvent(new win.PointerEvent('pointerdown', { ...opts, pointerType: 'mouse' }));
  doc.body.dispatchEvent(new win.PointerEvent('pointerup', { ...opts, pointerType: 'mouse' }));
  doc.body.dispatchEvent(new win.MouseEvent('click', { ...opts, detail: 1 }));
  doc.body.dispatchEvent(new win.MouseEvent('dblclick', { ...opts, detail: 2 }));
  return true;
})()`);
await settle();
const afterDouble = JSON.parse(await evalIn(probe));
check('a double-click on a word is not a page turn',
  afterDouble.fraction === afterDrag.fraction,
  `${afterDrag.fraction} -> ${afterDouble.fraction}`);

await evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  const button = doc.createElement('button');
  button.id = 'tap-guard';
  button.textContent = 'not a page turn';
  doc.body.append(button);
  const box = doc.defaultView.frameElement.getBoundingClientRect();
  button.dispatchEvent(new MouseEvent('click', {
    bubbles: true, button: 0, detail: 1,
    clientX: window.innerWidth - 6 - box.left,
    clientY: window.innerHeight / 2 - box.top,
  }));
  return true;
})()`);
await settle();
const afterControl = JSON.parse(await evalIn(probe));
check('a click on a control in the text is not a page turn',
  afterControl.fraction === afterDouble.fraction,
  `${afterDouble.fraction} -> ${afterControl.fraction}`);

await evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  doc.getSelection().selectAllChildren(doc.body);
  const box = doc.defaultView.frameElement.getBoundingClientRect();
  doc.body.dispatchEvent(new MouseEvent('click', {
    bubbles: true, button: 0, detail: 1,
    clientX: window.innerWidth - 6 - box.left,
    clientY: window.innerHeight / 2 - box.top,
  }));
  return true;
})()`);
await settle();
const afterSelection = JSON.parse(await evalIn(probe));
check('a click while text is selected is not a page turn',
  afterSelection.fraction === afterControl.fraction,
  `${afterControl.fraction} -> ${afterSelection.fraction}`);
await evalIn(`(() => {
  document.querySelector('foliate-view').renderer.getContents()[0]
    .doc.getSelection().removeAllRanges();
  return true;
})()`);

// Everything above dispatched its own events. Untrusted events skip the
// browser's real input pipeline — no native selection, no synthesized
// dblclick, no touch-to-pointer translation, and none of the engine's
// own swipe handling. The checks below therefore go through CDP's
// Input domain, which is the same path a hand takes.
const mouse = (type, x, y, clickCount = 1) => S('Input.dispatchMouseEvent', {
  type, x, y, button: 'left', buttons: type === 'mouseReleased' ? 0 : 1, clickCount,
});
const realClick = async (x, y, clickCount = 1) => {
  await mouse('mousePressed', x, y, clickCount);
  await mouse('mouseReleased', x, y, clickCount);
};
const selectionText = () => evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  return String(doc.getSelection());
})()`);
const clearSelection = () => evalIn(`(() => {
  document.querySelector('foliate-view').renderer.getContents()[0]
    .doc.getSelection().removeAllRanges();
  return true;
})()`);
// Two points in the page-turning ribbon on the right: one on a word,
// which a double-click could be selecting, and one in the blank between
// lines, which it cannot. The reader treats them differently on
// purpose, so the harness has to find both for real.
const ribbonPoints = async () => JSON.parse(await evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const w = window.innerWidth, h = window.innerHeight;
  const side = Math.max(64, Math.min(w * (w < 700 ? 0.3 : 0.22), 260));
  // The page-turn arrows are real buttons sitting in this same ribbon,
  // and a real pointer reveals them as it moves. A point behind one of
  // them would test the button, not the tap zones.
  const arrows = [...document.querySelectorAll('.reader-turn')]
    .map((b) => b.getBoundingClientRect());
  const behindAnArrow = (top, bottom, left, right) => arrows.some((a) =>
    bottom > a.top - 8 && top < a.bottom + 8 && right > a.left - 8 && left < a.right + 8);
  const leftLines = [];
  const rightLines = [];
  const leftWords = [];
  const rightWords = [];
  const addRect = (target, rect) => {
    const left = rect.left + box.left, right = rect.right + box.left;
    const top = rect.top + box.top, bottom = rect.bottom + box.top;
    if (top < 0 || bottom > h || behindAnArrow(top, bottom, left, right)) return;
    target.push({ left, right, top, bottom });
  };
  const walk = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    if (!n.data.trim()) continue;
    const r = doc.createRange();
    r.selectNodeContents(n);
    for (const rect of r.getClientRects()) {
      if (rect.width < 4 || rect.height < 4) continue;
      const left = rect.left + box.left, right = rect.right + box.left;
      if (right > 0 && left < side) addRect(leftLines, rect);
      if (right > w - side && left < w) addRect(rightLines, rect);
    }
    const words = /\S+/g;
    let match;
    while ((match = words.exec(n.data))) {
      const wordRange = doc.createRange();
      wordRange.setStart(n, match.index);
      wordRange.setEnd(n, match.index + match[0].length);
      for (const rect of wordRange.getClientRects()) {
        if (rect.width < 4 || rect.height < 4) continue;
        const left = rect.left + box.left, right = rect.right + box.left;
        if (right > 0 && left < side) addRect(leftWords, rect);
        if (right > w - side && left < w) addRect(rightWords, rect);
      }
    }
  }
  const lines = [...rightLines, ...leftLines].sort((a, b) => a.top - b.top);
  const edge = (rect, sideName) => ({
    x: sideName === 'right'
      ? (Math.max(rect.left, w - side + 4) + Math.min(rect.right, w - 8)) / 2
      : (Math.max(rect.left, 8) + Math.min(rect.right, side - 4)) / 2,
    y: (rect.top + rect.bottom) / 2,
    side: sideName,
  });
  const wordRect = leftWords[Math.floor(leftWords.length / 2)] ||
    leftLines[Math.floor(leftLines.length / 2)] ||
    rightWords[Math.floor(rightWords.length / 2)] ||
    rightLines[Math.floor(rightLines.length / 2)];
  const wordSide = leftWords.length || leftLines.length ? 'left' : 'right';
  const word = wordRect
    ? edge(wordRect, wordSide)
    : null;
  // Somewhere in the ribbon with no word under it and no button over
  // it: between two lines, in a paragraph margin, or past the last one.
  const all = [];
  const everything = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT);
  for (let n = everything.nextNode(); n; n = everything.nextNode()) {
    if (!n.data.trim()) continue;
    const r = doc.createRange();
    r.selectNodeContents(n);
    for (const rect of r.getClientRects()) {
      if (rect.width < 4 || rect.height < 4) continue;
      all.push({
        top: rect.top + box.top, bottom: rect.bottom + box.top,
        left: rect.left + box.left, right: rect.right + box.left,
      });
    }
  }
  // The arrows are full-height strips at both edges, so a point in the
  // ribbon has to sit inboard of them.
  const arrowEdge = arrows.length
    ? Math.min(...arrows.filter((a) => a.left > w / 2).map((a) => a.left)) - 10
    : w - 6;
  const readerRight = document.getElementById('reader-view')
    .getBoundingClientRect().right;
  const bx = Math.max(
    w - side + 8,
    Math.min(arrowEdge - 12, readerRight - 12, w - 8),
  );
  let blank = null;
  for (let y = 80; y < h - 40 && !blank; y += 8) {
    if (behindAnArrow(y, y, bx, bx)) continue;
    if (all.some((r) => y > r.top - 3 && y < r.bottom + 3 && bx > r.left - 3 && bx < r.right + 3))
      continue;
    blank = { x: bx, y };
  }
  return JSON.stringify({
    frame: { left: box.left, right: box.right, width: box.width },
    window: w,
    lines: lines.length,
    word,
    blank,
  });
})()`));

await idle();
const spots = await ribbonPoints();
check('the harness found real text in the page-turning ribbon',
  spots.lines > 0 && !!spots.word, JSON.stringify(spots));

if (spots.word) {
  // A real double-click selects a word. The first of its two clicks is
  // indistinguishable from a tap when it arrives, so this is the case
  // the deferral exists for — and 350ms apart it is slower than a
  // synthetic pair, which is the point of testing it for real.
  const before = JSON.parse(await evalIn(probe));
  await realClick(spots.word.x, spots.word.y, 1);
  await new Promise((r) => setTimeout(r, 350));
  await realClick(spots.word.x, spots.word.y, 2);
  const picked = await selectionText();
  await settle();
  const afterReal = JSON.parse(await evalIn(probe));
  // What is asserted is the reader's part: the deferred first click was
  // cancelled, so the pair selected rather than navigated. Whether the
  // browser also left a word highlighted is the browser's business, and
  // a headless build does not always bother.
  check('a real double-click does not turn the page',
    afterReal.fraction === before.fraction,
    `${before.fraction} -> ${afterReal.fraction}, selected ${JSON.stringify(picked.slice(0, 20))}`);
  await clearSelection();

  // A real drag is a selection, never a page turn.
  await mouse('mousePressed', spots.word.x, spots.word.y, 1);
  await mouse('mouseMoved', spots.word.x - 120, spots.word.y, 1);
  await mouse('mouseReleased', spots.word.x - 120, spots.word.y, 1);
  const dragged = await selectionText();
  await settle();
  const afterRealDrag = JSON.parse(await evalIn(probe));
  check('a real drag across the text does not turn the page',
    afterRealDrag.fraction === before.fraction,
    `${before.fraction} -> ${afterRealDrag.fraction}, selected ${JSON.stringify(dragged.slice(0, 20))}`);
  await clearSelection();

  // A single real click on a word still turns, once the double-click
  // window has passed.
  await realClick(spots.word.x, spots.word.y, 1);
  await settle();
  const afterRealClick = JSON.parse(await evalIn(probe));
  const wordDirection = spots.word.side === 'right' ? 1 : -1;
  check('a real click on a word still turns the page',
    wordDirection > 0
      ? afterRealClick.fraction > before.fraction
      : afterRealClick.fraction < before.fraction,
    `${before.fraction} -> ${afterRealClick.fraction}`);
  await realClick(wordDirection > 0 ? 6 : spots.window - 6, spots.word.y, 1);
  await settle();
}

if (spots.blank) {
  // Nothing to select here, so nothing to wait for: the page turns
  // well inside the double-click window.
  const before = JSON.parse(await evalIn(probe));
  await realClick(spots.blank.x, spots.blank.y, 1);
  await new Promise((r) => setTimeout(r, 250));
  const quick = JSON.parse(await evalIn(probe));
  check('a real click between the lines turns the page at once',
    quick.fraction > before.fraction, `${before.fraction} -> ${quick.fraction}`);
  await settle();
  await realClick(6, spots.blank.y, 1);
  await settle();
} else {
  console.log('note: no blank gap found in the ribbon on this page');
}

// A finger is the engine's business as much as ours: foliate-js pans
// and snaps on a swipe. If both it and the reader acted on the same
// gesture the book would jump two pages, so the measure here is one
// page's worth of progress, taken from a page turn immediately before.
const innerWidthGuess = Number(await evalIn('window.innerWidth'));
const innerHeightGuess = Number(await evalIn('window.innerHeight'));
const beforeStep = JSON.parse(await evalIn(probe));await evalIn(`document.getElementById('reader-next').click()`);
await settle();
const afterStep = JSON.parse(await evalIn(probe));
const pageStep = afterStep.fraction - beforeStep.fraction;
await evalIn(`document.getElementById('reader-prev').click()`);
await settle();

const touchable = await S('Input.dispatchTouchEvent', {
  type: 'touchStart', touchPoints: [{ x: Math.round(innerWidthGuess * 0.7), y: 200 }],
}).then(() => true, () => false);
if (touchable) {
  await S('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  const beforeSwipe = JSON.parse(await evalIn(probe));
  const y = Math.round(spots.word ? spots.word.y : innerHeightGuess / 2);
  const from = Math.round(innerWidthGuess * 0.75);
  await S('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: from, y }] });
  for (const step of [0.2, 0.4, 0.6, 0.8, 1]) {
    await S('Input.dispatchTouchEvent', {
      type: 'touchMove', touchPoints: [{ x: Math.round(from - 300 * step), y }],
    });
    await new Promise((r) => setTimeout(r, 20));
  }
  await S('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await settle();
  const afterSwipe = JSON.parse(await evalIn(probe));
  const moved = afterSwipe.fraction - beforeSwipe.fraction;
  check('a swipe never turns more than one page',
    pageStep > 0 && moved <= pageStep * 1.6 + 1e-9,
    `${beforeSwipe.fraction} -> ${afterSwipe.fraction} (one page is ${pageStep.toFixed(4)})`);

  // A tap has no gesture to disambiguate from, so it must not wait.
  const beforeTouchTap = JSON.parse(await evalIn(probe));
  await S('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x: Math.round(innerWidthGuess - 6), y }],
  });
  await S('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await new Promise((r) => setTimeout(r, 250));
  const afterTouchTap = JSON.parse(await evalIn(probe));
  check('a real touch tap turns the page without waiting',
    afterTouchTap.fraction > beforeTouchTap.fraction,
    `${beforeTouchTap.fraction} -> ${afterTouchTap.fraction}`);
  await settle();
} else {
  console.log('note: this browser build refused synthetic touch events');
}

// A second finger is a pinch or a scroll, never a page turn, and a
// pointer the browser takes back mid-gesture is not one either.
await idle();
const beforeFingers = JSON.parse(await evalIn(probe));
await evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const x = window.innerWidth - 6 - box.left, y = window.innerHeight / 2 - box.top;
  const at = (type, id, extra) => doc.body.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, button: 0, pointerType: 'touch', pointerId: id,
    clientX: x, clientY: y, ...extra,
  }));
  at('pointerdown', 1);
  at('pointerdown', 2, { clientX: x - 60 });
  at('pointerup', 1);
  at('pointerup', 2, { clientX: x - 60 });
  return true;
})()`);
await settle();
const afterFingers = JSON.parse(await evalIn(probe));
check('a second finger cancels the tap',
  afterFingers.fraction === beforeFingers.fraction,
  `${beforeFingers.fraction} -> ${afterFingers.fraction}`);

await evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const box = win.frameElement.getBoundingClientRect();
  const x = window.innerWidth - 6 - box.left, y = window.innerHeight / 2 - box.top;
  const at = (type) => doc.body.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, button: 0, pointerType: 'touch', pointerId: 7, clientX: x, clientY: y,
  }));
  at('pointerdown');
  at('pointercancel');
  at('pointerup');
  return true;
})()`);
await settle();
const afterCancel = JSON.parse(await evalIn(probe));
check('a cancelled pointer turns nothing',
  afterCancel.fraction === beforeFingers.fraction,
  `${beforeFingers.fraction} -> ${afterCancel.fraction}`);

// A click that puts a selection away is not a tap — and by the time it
// arrives the browser has already collapsed the selection, so the only
// place the evidence still exists is the pointerdown before it.
// A few words on the page this reader is looking at, not the whole
// body: a selection running past the end of the page makes the engine
// itself follow it forward, which would prove nothing about taps.
const live = Number(await evalIn(`(() => {
  const view = document.querySelector('foliate-view');
  const doc = view.renderer.getContents()[0].doc;
  const box = doc.defaultView.frameElement.getBoundingClientRect();
  const walk = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    if (n.data.trim().length < 12) continue;
    const range = doc.createRange();
    range.setStart(n, 0);
    range.setEnd(n, 10);
    const rect = range.getBoundingClientRect();
    if (rect.top + box.top < 0 || rect.bottom + box.top > window.innerHeight) continue;
    const left = rect.left + box.left;
    if (left < 0 || left > window.innerWidth) continue;
    const sel = doc.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    return String(sel).length;
  }
  return 0;
})()`));
await realClick(spots.word.x, spots.word.y, 1);
await settle();
const afterClearing = JSON.parse(await evalIn(probe));
check('a real click that clears a selection turns nothing',
  live > 0 && afterClearing.fraction === beforeFingers.fraction,
  `${live} characters selected, ${beforeFingers.fraction} -> ${afterClearing.fraction}`);
await clearSelection();

// A fixed-layout publication is scaled by the engine with a CSS
// transform, so a coordinate inside the chapter is not a coordinate in
// the window. Scaling the frame by hand proves the conversion without
// needing a second book: the same viewport point must still be the
// page-turning ribbon.
// Turning a page scrolls the frame under the window, so the local
// coordinates are taken fresh for every tap rather than once.
const scaledLocal = async (share) => JSON.parse(await evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  const frame = doc.defaultView.frameElement;
  frame.style.transformOrigin = 'top left';
  frame.style.transform = 'scale(0.8)';
  const box = frame.getBoundingClientRect();
  const vx = ${'SHARE'} === 1 ? window.innerWidth - 6 : window.innerWidth * ${'SHARE'};
  const vy = window.innerHeight / 2;
  return JSON.stringify({
    scale: box.width / frame.offsetWidth,
    x: (vx - box.left) / 0.8,
    y: (vy - box.top) / 0.8,
  });
})()`.replaceAll('SHARE', String(share))));
const scaledTap = (x, y) => evalIn(`(() => {
  const doc = document.querySelector('foliate-view').renderer.getContents()[0].doc;
  const win = doc.defaultView;
  const at = (type) => doc.body.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, button: 0, pointerType: 'touch', pointerId: 3,
    clientX: ${x}, clientY: ${y},
  }));
  at('pointerdown');
  at('pointerup');
  return true;
})()`);
const beforeScaled = JSON.parse(await evalIn(probe));
const rightSpot = await scaledLocal(1);
check("the frame reports the engine's scale",
  Math.abs(rightSpot.scale - 0.8) < 0.01, String(rightSpot.scale));
await scaledTap(rightSpot.x, rightSpot.y);
await settle();
const afterScaledRight = JSON.parse(await evalIn(probe));
check('a tap in a scaled chapter lands where the reader aimed it',
  afterScaledRight.fraction > beforeScaled.fraction,
  `${beforeScaled.fraction} -> ${afterScaledRight.fraction}`);
const middleSpot = await scaledLocal(0.5);
await scaledTap(middleSpot.x, middleSpot.y);
await settle();
const afterScaledMiddle = JSON.parse(await evalIn(probe));
check('the middle of a scaled chapter is still the middle',
  afterScaledMiddle.fraction === afterScaledRight.fraction,
  `${afterScaledRight.fraction} -> ${afterScaledMiddle.fraction}`);
await evalIn(`(() => {
  const frame = document.querySelector('foliate-view').renderer
    .getContents()[0].doc.defaultView.frameElement;
  frame.style.transform = '';
  return true;
})()`);

// The contents drawer cannot hear a tap that happened inside the book,
// so the book has to tell it.
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 't', bubbles: true }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const beforeDrawerTap = JSON.parse(await evalIn(probe));
await tapChapter('right', 'touch');
await settle();
const afterDrawerTap = JSON.parse(await evalIn(probe));
const drawerShut = await evalIn(`document.getElementById('reader-toc').hidden`);
check('a tap in the book puts the contents drawer away', drawerShut === true,
  String(drawerShut));
check('the tap that closed the drawer did not also turn the page',
  afterDrawerTap.fraction === beforeDrawerTap.fraction,
  `${beforeDrawerTap.fraction} -> ${afterDrawerTap.fraction}`);

// "z" is the keyboard's way of putting the chrome away and getting it
// back, and the contents drawer brings its own button back with it.
await evalIn(`(() => {
  document.dispatchEvent(new PointerEvent('pointermove', {
    bubbles: true, clientX: window.innerWidth / 2, clientY: 4,
  }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', bubbles: true }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const zHidden = JSON.parse(await chromeState());
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', bubbles: true }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const zShown = JSON.parse(await chromeState());
check('"z" puts the chrome away', zHidden.state === 'hidden', JSON.stringify(zHidden));
check('"z" brings it back and pins it', zShown.state === 'visible' && zShown.bar === '1',
  JSON.stringify(zShown));
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', bubbles: true }));
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 't', bubbles: true }));
  return true;
})()`);
await new Promise((r) => setTimeout(r, 400));
const withDrawer = JSON.parse(await chromeState());
check('the contents drawer brings the bar back with it',
  withDrawer.state === 'visible' && withDrawer.bar === '1', JSON.stringify(withDrawer));
await evalIn(`(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  return true;
})()`);

// Switching the setting off is what a reader who wants the bar there
// does, and it has to stick.
await evalIn(`(() => {
  const box = document.querySelector('#reader-settings-form input[name="autohide"]');
  box.checked = false;
  box.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`);
await idle();
const pinned = JSON.parse(await chromeState());
const savedChrome = await evalIn(`localStorage.getItem('liseur.reader.settings')`);
check('the chrome stays put once auto-hiding is switched off',
  pinned.state === 'visible' && pinned.bar === '1', JSON.stringify(pinned));
check('the auto-hide choice persists in the browser',
  typeof savedChrome === 'string' && savedChrome.includes('"autohide":false'),
  String(savedChrome));
await tapChapter('middle', 'touch');
await new Promise((r) => setTimeout(r, 500));
const fixedAfterTap = JSON.parse(await chromeState());
check('a middle tap does not hide fixed chrome',
  fixedAfterTap.state === 'visible' && fixedAfterTap.bar === '1',
  JSON.stringify(fixedAfterTap));

// The browser refusing to run the publication's script is verified by
// confirming the script did not run and no unexpected errors occurred.
const unexpected = consoleErrors.filter((e) =>
  !/Blocked script execution/.test(e) &&
  !/Permissions policy violation: unload is not allowed/.test(e)
);
check('the browser refused to run the publication', diag.ran === false);

if (unexpected.length) {
  console.log('console errors:\n  ' + unexpected.join('\n  '));
}
check('no console errors', unexpected.length === 0);

ws.close();
proc.kill();
process.exit(fail.length ? 1 : 0);

// nanGuard proves the position-jumps fix from the page's own side. The
// server now rejects a null progression whatever the client does, so a
// test that only inspected the op log would be vacuous: the observable
// thing is the *attempt*, so this wraps fetch and watches for the POST.
// internal/api/progression_test.go is what actually protects the log;
// this checks that the reader fails closed on a bad fraction without
// wedging its ability to save again: a bad fraction must not be posted
// as null, and it should prod the engine for a fresh layout rather than
// wait indefinitely for the reader to turn another page.
async function nanGuard(evalIn, check) {
  // Record every POST to /v1/ops the page attempts, installed before a
  // push can happen. api() reads the global `fetch` afresh on each call,
  // so reassigning it here is enough to see the reader's own requests.
  await evalIn(`(() => {
    window.__pushedOps = [];
    const orig = window.fetch;
    window.fetch = function (input, init) {
      const url = typeof input === 'string' ? input : (input && input.url) || '';
      const method = ((init && init.method) || (input && input.method) || 'GET').toUpperCase();
      if (method === 'POST' && url.indexOf('v1/ops') >= 0) {
        window.__pushedOps.push(String((init && init.body) || ''));
      }
      return orig.apply(this, arguments);
    };
    return true;
  })()`);

  // A synthetic relocate carrying just enough of the detail shape that
  // paint() and the push path touch — fraction, cfi, section, tocItem.
  const relocate = (frac) => `(() => {
    document.querySelector('foliate-view').dispatchEvent(
      new CustomEvent('relocate', { detail: {
        fraction: ${frac},
        cfi: 'epubcfi(/6/8!/4/2/1:0)',
        section: { current: 1, total: 12 },
        tocItem: { label: 'Chowder' },
      } }));
    return true;
  })()`;
  const pushed = async () =>
    JSON.parse(await evalIn('JSON.stringify(window.__pushedOps)'));
  const finiteProgressions = (bodies) => bodies.map((body) => {
    try {
      return JSON.parse(body).ops[0].progression;
    } catch (e) {
      return null;
    }
  }).every((p) => typeof p === 'number' && Number.isFinite(p));
  // Comfortably past the 1.5s debounce in schedulePush().
  const settle = () => new Promise((r) => setTimeout(r, 2500));

  await evalIn('(() => { window.__pushedOps = []; return true; })()');
  await evalIn(relocate('NaN'));
  await settle();
  const afterNaN = await pushed();
  check('a NaN fraction is never pushed as null',
    finiteProgressions(afterNaN), JSON.stringify(afterNaN));
  check('a skipped NaN schedules a layout retry',
    afterNaN.length >= 1, JSON.stringify(afterNaN));

  await evalIn(relocate('0.47'));
  await settle();
  const afterFinite = await pushed();
  check('a finite fraction still pushes after a skipped NaN',
    afterFinite.length >= 1, JSON.stringify(afterFinite));
  let prog = null;
  if (afterFinite.length) {
    try {
      prog = JSON.parse(afterFinite[afterFinite.length - 1]).ops[0].progression;
    } catch (e) { /* leaves prog null, the check below reports it */ }
  }
  check('the recovered push carries the finite progression', prog === 0.47, String(prog));
}

// sessionGuard proves that reading in the browser is counted (ADR-0030).
// The reader opens a sitting once a position is measured and closes it
// when the tab goes away, so the probe stands in for the tab: it skews
// performance.now to make minutes pass, turns a page, then says the
// document is hidden. The observable thing is the POST to /v1/sessions;
// the Go side then checks the row reached the store with the figures
// the page said.
async function sessionGuard(evalIn, check) {
  await evalIn(`(() => {
    window.__pushedSessions = [];
    const orig = window.fetch;
    window.fetch = function (input, init) {
      const url = typeof input === 'string' ? input : (input && input.url) || '';
      const method = ((init && init.method) || (input && input.method) || 'GET').toUpperCase();
      if (method === 'POST' && url.indexOf('v1/sessions') >= 0) {
        window.__pushedSessions.push(String((init && init.body) || ''));
      }
      return orig.apply(this, arguments);
    };
    // A clock the probe can wind forward, and a visibility the probe
    // can set: the page consults both through the usual names.
    const base = performance.now.bind(performance);
    window.__skew = 0;
    performance.now = () => base() + window.__skew;
    // Both clocks move together, as they would in a real sitting; the
    // server refuses idle past the wall-clock span, so winding only
    // the monotonic one would be refused as a lie.
    const RealDate = Date;
    window.Date = class extends RealDate {
      constructor(...args) {
        if (args.length) super(...args);
        else super(RealDate.now() + window.__skew);
      }
      static now() { return RealDate.now() + window.__skew; }
    };
    window.__hidden = false;
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.__hidden });
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => window.__hidden ? 'hidden' : 'visible' });
    return true;
  })()`);

  const relocate = (frac) => `(() => {
    document.querySelector('foliate-view').dispatchEvent(
      new CustomEvent('relocate', { detail: {
        fraction: ${frac},
        cfi: 'epubcfi(/6/8!/4/2/1:0)',
        section: { current: 1, total: 12 },
        tocItem: { label: 'Chowder' },
      } }));
    return true;
  })()`;
  const setHidden = (hidden) => evalIn(`(() => {
    window.__hidden = ${hidden};
    document.dispatchEvent(new Event('visibilitychange'));
    return true;
  })()`);
  const wind = (ms) => evalIn(`(() => { window.__skew += ${ms}; return true; })()`);
  const pushed = async () =>
    JSON.parse(await evalIn('JSON.stringify(window.__pushedSessions)')).flatMap((body) => {
      try { return JSON.parse(body).sessions; } catch (e) { return []; }
    });
  const settle = () => new Promise((r) => setTimeout(r, 1500));

  // The book opened with a real relocate, so a sitting is already open.
  // Four minutes of reading with a TOC tap in the middle, then the tab
  // is hidden. Past the three-minute cap without that tap, so a zero
  // here proves navigation input resets the gap; the relocate alone
  // must not, since the engine also relocates on a resize.
  await wind(2 * 60000);
  const tappedTOC = await evalIn(`(() => {
    const link = document.querySelector('#reader-toc-list a[data-href]');
    if (!link) return false;
    link.click();
    return true;
  })()`);
  check('the fixture has a TOC navigation tap', tappedTOC === true, String(tappedTOC));
  await evalIn(relocate('0.47'));
  await wind(2 * 60000);
  await setHidden(true);
  await settle();
  let got = await pushed();
  check('hiding the tab posts the sitting', got.length === 1, JSON.stringify(got));
  const first = got[0] || {};
  check('the sitting ends where the reader is', first.end_progression === 0.47, String(first.end_progression));
  check('the sitting began before it ended',
    typeof first.start_progression === 'number' && first.start_progression <= 0.47, String(first.start_progression));
  check('a TOC tap resets the idle gap', first.idle_ms === 0, String(first.idle_ms));

  // Back, and away again at once: too short to be a sitting.
  await setHidden(false);
  await setHidden(true);
  await settle();
  got = await pushed();
  check('a glance is not a session', got.length === 1, JSON.stringify(got));

  // Back for a long stare at one page: the threshold is credited and
  // the rest is idle.
  await setHidden(false);
  await wind(10 * 60000);
  await setHidden(true);
  await settle();
  got = await pushed();
  check('a second sitting is a second session', got.length === 2, JSON.stringify(got));
  const second = got[1] || {};
  // Real milliseconds pass between the probe's steps, so within a second.
  check('time past the idle threshold is idle',
    Math.abs(second.idle_ms - 7 * 60000) < 1000, String(second.idle_ms));
  check('the two sittings have distinct ids',
    first.session_id && second.session_id && first.session_id !== second.session_id);
}
