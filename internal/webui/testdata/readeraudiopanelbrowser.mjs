import { writeFileSync } from 'node:fs';

export async function installAudioFixture(S) {
  await S('Page.addScriptToEvaluateOnNewDocument', { source: `(() => {
    const bytes = new Uint8Array(44 + 8000 * 120);
    const view = new DataView(bytes.buffer);
    const text = (offset, s) => [...s].forEach((c, i) => bytes[offset + i] = c.charCodeAt(0));
    text(0, 'RIFF'); view.setUint32(4, bytes.length - 8, true); text(8, 'WAVE'); text(12, 'fmt ');
    view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true);
    view.setUint32(24, 8000, true); view.setUint32(28, 8000, true); view.setUint16(32, 1, true);
    view.setUint16(34, 8, true); text(36, 'data'); view.setUint32(40, bytes.length - 44, true);
    bytes.fill(128, 44);
    const stream = URL.createObjectURL(new Blob([bytes], {type: 'audio/wav'}));
    const original = window.fetch;
    const json = data => new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json'}});
    window.fetch = async function(input, init) {
      const url = typeof input === 'string' ? input : input.url;
      const mapping = JSON.parse(sessionStorage.getItem('liseur-test-audio-mapping') || '{"segments":[],"resources":[]}');
      if (url.endsWith('/audio/info')) return json({audiobook: {id: 'browser-audio', title: 'Moby-Dick', duration_seconds: 120, chapters: [{title: 'Title Page', start: 0, end: 120}]}, audio_time: 0, mapping});
      if (url.endsWith('/audio/ticket')) return json({stream_url: stream, expires_at: new Date(Date.now() + 3600000).toISOString()});
      if (url.endsWith('/audio/locate')) return json({audio_time: 0});
      const response = await original.call(this, input, init);
      if (url.endsWith('/resolve') && response.ok) return json({...await response.json(), has_audio: true});
      return response;
    };
  })()` });
}

export async function checkNarrationFollowing({ S, evalIn, waitFor, check, url, mode }) {
  await S('Emulation.setDeviceMetricsOverride', { width: 320, height: 300, deviceScaleFactor: 1, mobile: true });
  await evalIn(`(() => {
    const href = document.querySelector('readium-view').book.sections[0].id;
    sessionStorage.setItem('liseur-test-audio-mapping', JSON.stringify({
      segments: [{ a0: 0, a1: 120, b0: 0, b1: 1, href }],
      resources: [{ href, start: 0, end: 1 }],
      full_narrative: true,
    }));
  })()`);
  await S('Page.navigate', { url });
  await waitFor("document.querySelector('readium-view')?.contents().some(({doc}) => doc?.body)", 'mapped chapter');
  await waitFor("!document.getElementById('reader-audio-toggle').hidden", 'available audiobook');
  await evalIn("document.getElementById('reader-audio-toggle').click()");
  await waitFor("!document.getElementById('reader-audio-bar').hidden && document.getElementById('reader-audio-el').readyState >= 1", 'audio metadata');
  await evalIn(`(async () => {
    const { chapterIndex, offsetOfFraction, sentenceAt, rangeOf } = await import('/ui/static/reader-narration.js');
    window.__narrationProbe = time => {
      const view = document.querySelector('readium-view');
      const doc = view.contents()[0].doc;
      const index = chapterIndex(doc);
      const offset = Math.min(index.text.length - 1, offsetOfFraction(index, time / 120));
      const sentence = sentenceAt(index.sentences, offset);
      const character = rangeOf(doc, index, offset, offset + 1);
      const full = rangeOf(doc, index, sentence.start, sentence.end);
      const highlights = [...doc.defaultView.CSS.highlights.values()].flatMap(h => [...h]);
      return {
        placement: view.rangePlacement(character),
        sentencePlacement: view.rangePlacement(full),
        highlighted: highlights.some(r => r.toString() === full.toString()),
        scrollX: doc.defaultView.scrollX,
      };
    };
  })()`);
  await waitFor('__narrationProbe(0).placement === "here" && __narrationProbe(0).highlighted', 'first narrated character');
  for (const time of [66, 108]) {
    const before = await evalIn(`__narrationProbe(${time})`);
    check(mode + ' later character is on another page', before.placement === 'elsewhere', JSON.stringify(before));
    if (mode === 'unspaced') {
      check('the same unspaced sentence is still partly visible', before.sentencePlacement === 'here' && before.highlighted, JSON.stringify(before));
    }
    await evalIn(`document.getElementById('reader-audio-el').currentTime = ${time}`);
    await waitFor(`__narrationProbe(${time}).placement === 'here' && __narrationProbe(${time}).highlighted`, mode + ' follows the narrated character at ' + time);
  }
  if (mode === 'rtl') check('RTL following uses negative scroll coordinates', (await evalIn('__narrationProbe(108)')).scrollX < 0);
  await evalIn("document.getElementById('reader-audio-el').currentTime = 0");
  await waitFor('__narrationProbe(0).placement === "here" && __narrationProbe(0).highlighted', 'rewind before rapid skips');
  await evalIn("for (let i = 0; i < 3; i++) document.getElementById('reader-audio-fwd').click()");
  await waitFor("document.getElementById('reader-audio-el').currentTime === 90 && __narrationProbe(90).placement === 'here' && __narrationProbe(90).highlighted", mode + ' follows the final forward skip');
  check(mode + ' rapid forward skips leave the narrated character on screen', true);
  const hidden = await evalIn(`(() => {
    const view = document.querySelector('readium-view');
    const doc = view.contents()[0].doc;
    const el = doc.createElement('span');
    el.textContent = 'Hidden text';
    doc.body.append(el);
    const range = doc.createRange();
    range.selectNodeContents(el);
    el.style.display = 'none';
    const notDisplayed = view.rangePlacement(range);
    el.style.cssText = 'position: absolute; top: 0; width: 1px; overflow: hidden';
    el.style[doc.defaultView.getComputedStyle(doc.scrollingElement).direction === 'rtl' ? 'right' : 'left'] = '-9999px';
    const offscreen = view.rangePlacement(range);
    el.remove();
    return { notDisplayed, offscreen };
  })()`);
  check(mode + ' hidden text has no page to follow', hidden.notDisplayed === 'nowhere' && hidden.offscreen === 'nowhere', JSON.stringify(hidden));
}

export async function checkAudioPanel({ S, evalIn, waitFor, check, url }) {
  const panel = "document.getElementById('reader-audio-bar')";
  const open = async () => {
    await waitFor("!document.getElementById('reader-audio-toggle').hidden", 'available audiobook');
    await evalIn("document.getElementById('reader-audio-toggle').click()");
    await waitFor(`!${panel}.hidden && (${panel}.dataset.audioLayout === 'floating' || ${panel}.dataset.audioLayout === 'dock') && document.getElementById('reader-audio-el').readyState >= 1`, 'panel layout and audio metadata');
  };
  const click = async selector => {
    await evalIn(`document.querySelector(${JSON.stringify(selector)}).click()`);
    await evalIn('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  };
  const rect = selector => evalIn(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; })()`);
  const bounded = () => evalIn(`(() => { const r = ${panel}.getBoundingClientRect(); return r.x >= 0 && r.y >= 0 && r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1; })()`);
  const screenshot = async name => {
    if (!process.env.SMOKE_SHOT) return;
    await evalIn(`Promise.all(${panel}.getAnimations({subtree:true}).map(animation => animation.finished.catch(() => {})))`);
    const { data } = await S('Page.captureScreenshot', { format: 'png' });
    writeFileSync(process.env.SMOKE_SHOT.replace(/\.png$/, '') + '-' + name + '.png', Buffer.from(data, 'base64'));
  };
  await S('Emulation.setDeviceMetricsOverride', { width: 1000, height: 800, deviceScaleFactor: 1, mobile: false });
  await open();
  check('mini defaults to a bounded non-modal player', await evalIn(`${panel}.dataset.audioSize === 'mini' && !document.querySelector('dialog[open]')`) && await bounded());
  check('one audio element and unique player controls', await evalIn(`(() => { const ids = [...${panel}.querySelectorAll('[id]')].map(e=>e.id); return new Set(ids).size === ids.length && ${panel}.querySelectorAll('audio').length === 1; })()`));
  await evalIn(`window.__panelAudio = document.getElementById('reader-audio-el')`);
  check('desktop mini is a single row', (await rect('#reader-audio-bar')).height <= 64);
  await screenshot('desktop-mini');
  await click('button[data-audio-size]');
  check('expanded exposes existing controls without replacing audio', await evalIn(`!document.querySelector('[data-audio-details]').hidden && document.getElementById('reader-audio-el') === window.__panelAudio`));
  await evalIn(`document.getElementById('reader-audio-follow').click(); document.getElementById('reader-audio-el').currentTime = 20; document.getElementById('reader-audio-rate').value = '1.25'; document.getElementById('reader-audio-rate').dispatchEvent(new Event('change'))`);
  const play = await rect('#reader-audio-play');
  await S('Input.dispatchMouseEvent', { type: 'mousePressed', x: play.x + play.width / 2, y: play.y + play.height / 2, button: 'left', clickCount: 1 });
  await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: play.x + play.width / 2, y: play.y + play.height / 2, button: 'left', clickCount: 1 });
  await waitFor("!document.getElementById('reader-audio-el').paused", 'audio playback');
  const playback = await evalIn("({src: __panelAudio.src, time: __panelAudio.currentTime, rate: __panelAudio.playbackRate})");
  await click('button[data-audio-size]');
  check('minimizing preserves playback, speed and source', await evalIn(`!__panelAudio.paused && __panelAudio.src === ${JSON.stringify(playback.src)} && __panelAudio.playbackRate === ${playback.rate} && __panelAudio.currentTime >= ${playback.time}`));
  await click('button[data-audio-size]');
  await screenshot('desktop-expanded');
  const passage = await evalIn("document.querySelector('readium-view').lastLocation.fraction");
  await click('#reader-next');
  await waitFor(`document.querySelector('readium-view').lastLocation.fraction !== ${passage}`, 'page turn beside expanded player');
  check('page turns do not stop playback', await evalIn('!__panelAudio.paused'));
  check('publication text stays selectable', await evalIn(`(() => {
    const doc = document.querySelector('readium-view').renderer.getContents().find(c => c.doc?.body)?.doc;
    if (!doc) return false;
    const range = doc.createRange(); range.selectNodeContents(doc.body);
    const selection = doc.getSelection(); selection.removeAllRanges(); selection.addRange(range);
    return selection.toString().length > 0;
  })()`));
  for (const edge of ['top', 'left', 'right', 'bottom']) {
    await click(`[data-audio-dock="${edge}"]`);
    check('dock ' + edge + ' stays in viewport', await bounded());
  }
  await click('[data-audio-dock="bottom-right"]');
  const handle = await rect('[data-audio-handle]');
  await S('Input.dispatchMouseEvent', { type: 'mousePressed', x: handle.x + 20, y: handle.y + 20, button: 'left', clickCount: 1 });
  for (let i = 1; i <= 8; i++) {
    await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: handle.x + 20 - i * 45, y: handle.y + 20 - i * 15, button: 'left', buttons: 1 });
  }
  await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: handle.x - 340, y: handle.y - 100, button: 'left', clickCount: 1 });
  check('drag over publication frames moves player and removes shield', await evalIn(`!document.querySelector('.reader-audio-drag-shield') && JSON.parse(localStorage.getItem('liseur.reader.audio-panel.v1')).edge === 'free'`));
  await evalIn("document.querySelector('[data-audio-handle]').focus()");
  const beforeKey = await rect('#reader-audio-bar');
  await S('Input.dispatchKeyEvent', { type: 'keyDown', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
  await S('Input.dispatchKeyEvent', { type: 'keyUp', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
  const afterKey = await rect('#reader-audio-bar');
  check('keyboard moves player by ten pixels', Math.abs(afterKey.x - beforeKey.x - 10) < 1);
  await evalIn("document.getElementById('reader-audio-play').click()");
  await S('Page.navigate', { url });
  await waitFor("document.querySelector('readium-view')?.renderer?.getContents?.().some(({doc}) => doc?.body)", 'publication after reload');
  check('reload does not reopen the player', await evalIn(`${panel}.hidden`));
  await open();
  check('reload restores size and placement', await evalIn(`${panel}.dataset.audioSize === 'expanded'`) && Math.abs((await rect('#reader-audio-bar')).x - afterKey.x) < 2);
  const longMetadata = () => evalIn(`
    document.getElementById('reader-audio-chapter').textContent = 'Chapter 55: The long road through the mountains and beyond';
    document.getElementById('reader-audio-current').textContent = '24:40:44';
    document.getElementById('reader-audio-duration').textContent = '31:08:16';
  `);
  await longMetadata();
  for (const width of [390, 360, 320]) {
    await S('Emulation.setDeviceMetricsOverride', { width, height: 780, deviceScaleFactor: 1, mobile: true });
    await waitFor(`${panel}.dataset.audioLayout === 'dock' && innerWidth === ${width}`, 'phone dock');
    await evalIn('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
    const reserved = await evalIn("document.querySelector('.reader-stage').style.getPropertyValue('--reader-audio-inset')");
    check('phone ' + width + ' keeps expanded controls bounded', await bounded());
    await screenshot('phone-' + width + '-expanded');
    await click('button[data-audio-size]');
    await waitFor(`${panel}.dataset.audioSize === 'mini'`, 'mini phone player');
    check('phone ' + width + ' reserves text space above footer', await evalIn(`(() => { const p = ${panel}.getBoundingClientRect(), f = document.getElementById('reader-footer').getBoundingClientRect(), v = document.getElementById('reader-view').getBoundingClientRect(); return p.bottom <= f.top && v.bottom <= p.top + 1 && ${panel}.scrollWidth <= ${panel}.clientWidth; })()`));
    check('phone ' + width + ' mini stays one row with long metadata', await evalIn(`(() => {
      const p = ${panel}.getBoundingClientRect();
      const time = document.querySelector('.reader-audio-times').getBoundingClientRect();
      const transport = document.querySelector('.reader-audio-transport').getBoundingClientRect();
      return p.height <= 64 && time.right <= transport.left && time.top >= p.top && time.bottom <= p.bottom;
    })()`));
    check('expansion does not repaginate for extra controls', reserved === await evalIn("document.querySelector('.reader-stage').style.getPropertyValue('--reader-audio-inset')"));
    await screenshot('phone-' + width + '-mini');
    await click('button[data-audio-size]');
  }
  for (const theme of ['sepia', 'dark', 'tokyo-night', 'rose-pine', 'black', 'light']) {
    await click(`input[name="theme"][value="${theme}"]`);
    await waitFor(`document.body.dataset.readerTheme === '${theme}'`, 'reader theme ' + theme);
    check('player remains usable in ' + theme, await bounded());
    await screenshot('phone-' + theme + '-expanded');
    await click('button[data-audio-size]');
    await screenshot('phone-' + theme + '-mini');
    await click('button[data-audio-size]');
  }
  await S('Emulation.setDeviceMetricsOverride', { width: 844, height: 390, deviceScaleFactor: 1, mobile: true });
  await waitFor(`${panel}.dataset.audioLayout === 'floating'`, 'landscape layout');
  check('rotation restores bounded floating placement', await bounded());
  await S('Emulation.setDeviceMetricsOverride', { width: 1000, height: 800, deviceScaleFactor: 1, mobile: false });
  await click('input[name="theme"][value="tokyo-night"]');
  await click('button[data-audio-size]');
  await longMetadata();
  await screenshot('desktop-tokyo-night-mini');
  await click('button[data-audio-size]');
  await screenshot('desktop-tokyo-night-expanded');
  await evalIn("document.getElementById('reader-audio-close').click()");
  check('close returns focus to toolbar', await evalIn(`${panel}.hidden && document.activeElement.id === 'reader-audio-toggle'`));
  await checkNarration({ S, evalIn, waitFor, check, url, open, screenshot });
}

// checkNarration maps the whole audiobook onto Loomings, sixty identical
// paragraphs, and follows the highlighted sentence through it: the
// decoration must land on the paragraph the narrator is in, not the first
// one quoting the same words, and the page must turn to it.
async function checkNarration({ S, evalIn, waitFor, check, url, open, screenshot }) {
  await S('Emulation.setDeviceMetricsOverride', { width: 1000, height: 800, deviceScaleFactor: 1, mobile: false });
  await evalIn(`(() => {
    const href = document.querySelector('readium-view').book.sections.find(s => s.id.includes('chapter1')).id;
    sessionStorage.setItem('liseur-test-audio-mapping', JSON.stringify({
      segments: [{ a0: 0, a1: 120, b0: 0, b1: 1, href, label: 'Loomings' }],
      resources: [{ href, start: 0, end: 1 }],
      full_narrative: true,
    }));
  })()`);
  await S('Page.navigate', { url });
  await waitFor("document.querySelector('readium-view')?.renderer?.getContents?.().some(({doc}) => doc?.body)", 'publication with a mapped audiobook');
  await open();
  // What the frame actually paints: Chromium draws decorations as CSS
  // custom highlights, so the range there is the one a reader sees, and
  // its paragraph is where the highlight landed.
  const spoken = `(() => {
    const view = document.querySelector('readium-view');
    if ((view.navigator._decorations.get('read-aloud') || []).length !== 1) return null;
    const doc = view.contents().find(({ doc }) => doc.defaultView.frameElement.checkVisibility({ visibilityProperty: true }))?.doc;
    const win = doc?.defaultView;
    const ranges = [...(win?.CSS?.highlights?.values?.() || [])].flatMap(h => [...h]);
    if (ranges.length !== 1) return null;
    const range = ranges[0];
    const node = range.startContainer.nodeType === Node.TEXT_NODE ? range.startContainer.parentElement : range.startContainer;
    return {
      text: range.toString(),
      paragraph: [...doc.querySelectorAll('p')].indexOf(node.closest('p')),
      visible: [...range.getClientRects()].some(r => r.width > 0 && r.right > 0 && r.left < win.innerWidth && r.bottom > 0 && r.top < win.innerHeight),
    };
  })()`;
  // The chapter opens on a heading positioned off every page, as
  // screen-reader-only text is. The narrator reads it, so it is
  // highlighted, but there is no page to turn to.
  await waitFor(spoken, 'a highlighted sentence');
  const heading = await evalIn(spoken);
  check('the offscreen heading is highlighted first', heading.text === 'Chapter heading for a screen reader' && !heading.visible, JSON.stringify(heading));
  await evalIn("document.getElementById('reader-audio-el').currentTime = 2");
  await waitFor(`${spoken}?.paragraph === 0`, 'the first paragraph');
  const first = await evalIn(spoken);
  check('a sentence of the first paragraph is highlighted', ['Call me Ishmael.', 'Some years ago, never mind how long precisely.'].includes(first.text), JSON.stringify(first));
  check('the highlighted sentence is on the page', first.visible, JSON.stringify(first));
  await evalIn("document.getElementById('reader-audio-el').currentTime = 90");
  await waitFor(`(() => { const s = ${spoken}; return s && s.paragraph > 40 && s.visible; })()`, 'the page following the highlight');
  const later = await evalIn(spoken);
  check('a later sentence is highlighted in its own paragraph', later.paragraph >= 43 && later.paragraph <= 46, JSON.stringify(later));
  await screenshot('narration');
  await evalIn("document.getElementById('reader-audio-highlight').click()");
  await waitFor("document.querySelector('readium-view').contents().every(({ doc }) => [...doc.defaultView.CSS.highlights.values()].every(h => h.size === 0))", 'highlight turned off');
  check('turning the highlight off clears it and is remembered', await evalIn("localStorage.getItem('liseur.reader.audio-highlight') === 'off' && document.getElementById('reader-audio-highlight').getAttribute('aria-pressed') === 'false'"));
  await evalIn("document.getElementById('reader-audio-highlight').click()");
  await evalIn("document.getElementById('reader-audio-close').click()");
  await evalIn("sessionStorage.removeItem('liseur-test-audio-mapping')");
}
