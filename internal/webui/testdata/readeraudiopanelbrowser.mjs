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
      if (url.endsWith('/audio/info')) return json({audiobook: {id: 'browser-audio', title: 'Moby-Dick', duration_seconds: 120, chapters: [{title: 'Title Page', start: 0, end: 120}]}, audio_time: 0, mapping: {segments: [], resources: []}});
      if (url.endsWith('/audio/ticket')) return json({stream_url: stream, expires_at: new Date(Date.now() + 3600000).toISOString()});
      if (url.endsWith('/audio/locate')) return json({audio_time: 0});
      const response = await original.call(this, input, init);
      if (url.endsWith('/resolve') && response.ok) return json({...await response.json(), has_audio: true});
      return response;
    };
  })()` });
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
  for (const width of [390, 360, 320]) {
    await S('Emulation.setDeviceMetricsOverride', { width, height: 780, deviceScaleFactor: 1, mobile: true });
    await waitFor(`${panel}.dataset.audioLayout === 'dock' && innerWidth === ${width}`, 'phone dock');
    await evalIn('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
    check('phone ' + width + ' keeps expanded controls bounded', await bounded());
    await screenshot('phone-' + width + '-expanded');
    await click('button[data-audio-size]');
    await waitFor(`${panel}.dataset.audioSize === 'mini'`, 'mini phone player');
    check('phone ' + width + ' reserves text space above footer', await evalIn(`(() => { const p = ${panel}.getBoundingClientRect(), f = document.getElementById('reader-footer').getBoundingClientRect(), v = document.getElementById('reader-view').getBoundingClientRect(); return p.bottom <= f.top && v.bottom <= p.top + 1 && ${panel}.scrollWidth <= ${panel}.clientWidth; })()`));
    await screenshot('phone-' + width + '-mini');
    await click('button[data-audio-size]');
  }
  for (const theme of ['sepia', 'dark', 'light']) {
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
  await evalIn("document.getElementById('reader-audio-close').click()");
  check('close returns focus to toolbar', await evalIn(`${panel}.hidden && document.activeElement.id === 'reader-audio-toggle'`));
}
