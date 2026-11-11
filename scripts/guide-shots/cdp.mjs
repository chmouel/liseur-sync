// A minimal Chrome DevTools driver shared by the guide's web shots.
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function launch({ chrome, base, cookie, profile, downloadDir }) {
  mkdirSync(profile, { recursive: true })
  const port = 9700 + (process.pid % 200)
  const child = spawn(chrome, [
    '--headless=new', `--remote-debugging-port=${port}`, '--no-sandbox', '--disable-gpu',
    '--hide-scrollbars', '--user-data-dir=' + profile, 'about:blank',
  ], { stdio: 'ignore' })
  let wsURL
  for (let i = 0; i < 100 && !wsURL; i++) {
    try { wsURL = (await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()).webSocketDebuggerUrl } catch { await sleep(100) }
  }
  if (!wsURL) throw new Error('chrome never came up')
  const ws = new WebSocket(wsURL)
  await new Promise((r) => { ws.onopen = r })
  let nextID = 1
  const pending = new Map()
  const events = []
  ws.onmessage = (m) => {
    const x = JSON.parse(m.data)
    if (x.id && pending.has(x.id)) {
      const p = pending.get(x.id)
      pending.delete(x.id)
      x.error ? p.rej(new Error(JSON.stringify(x.error))) : p.res(x.result)
    } else events.push(x)
  }
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const id = nextID++
    pending.set(id, { res, rej })
    ws.send(JSON.stringify({ id, method, params, sessionId }))
  })
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
  const s = (m, p) => send(m, p, sessionId)
  const ev = async (expression) => {
    const r = await s('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.text + ' in ' + expression)
    return r.result.value
  }
  if (downloadDir) {
    mkdirSync(downloadDir, { recursive: true })
    await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloadDir, eventsEnabled: true })
  }
  await s('Page.enable'); await s('Runtime.enable'); await s('Network.enable')
  const [name, value] = cookie.split('=')
  await s('Network.setCookies', { cookies: [{ name, value, domain: new URL(base).hostname, path: '/' }] })
  await s('Emulation.setDeviceMetricsOverride', { width: 1100, height: 900, deviceScaleFactor: 2, mobile: false })

  const save = (file, data) => { writeFileSync(file, Buffer.from(data, 'base64')); console.log('shot', file) }
  return {
    s, ev, events,
    setWidth: (w) => s('Emulation.setDeviceMetricsOverride', { width: w, height: 900, deviceScaleFactor: 2, mobile: false }),
    goto: async (path) => { await s('Page.navigate', { url: base + path }); await sleep(1200) },
    // shootElement photographs one element with a little air around it.
    async shootElement(selector, file, pad = 16) {
      const box = await ev(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (!e) return null; e.scrollIntoView({block: 'center'}); const r = e.getBoundingClientRect(); return {x: r.left + scrollX, y: r.top + scrollY, w: r.width, h: r.height} })()`)
      if (!box) throw new Error('no element ' + selector)
      await sleep(250)
      const r = await s('Page.captureScreenshot', {
        format: 'png', captureBeyondViewport: true,
        clip: { x: Math.max(0, box.x - pad), y: Math.max(0, box.y - pad), width: box.w + 2 * pad, height: box.h + 2 * pad, scale: 1 },
      })
      save(file, r.data)
    },
    async shootPage(file, height) {
      const h = height || Math.ceil((await s('Page.getLayoutMetrics')).cssContentSize.height)
      const r = await s('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: 1100, height: h, scale: 1 } })
      save(file, r.data)
    },
    close: () => child.kill(),
  }
}
