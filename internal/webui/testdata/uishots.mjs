// A screenshot walk of the revamped UI (ADR-0011 phase 7).
//
// This is not an assertion; it is how a visual change gets looked at.
// It signs in with a session cookie, visits each page at three widths,
// and writes a PNG per page per width. Run it with:
//
//	LISEUR_UI_SHOTS=/tmp/ui go test ./internal/webui/ -run UIScreenshots -v
import { mkdirSync, writeFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { join } from 'node:path'

const chrome = process.env.SHOT_CHROME
const base = process.env.SHOT_URL
const cookie = process.env.SHOT_COOKIE
const outDir = process.env.SHOT_DIR
const paths = process.env.SHOT_PATHS.split(',')
const widths = [1440, 900, 420]

mkdirSync(outDir, { recursive: true })

const port = 9333 + (process.pid % 500)
const child = spawn(chrome, [
  '--headless=new',
  `--remote-debugging-port=${port}`,
  '--no-sandbox',
  '--disable-gpu',
  '--hide-scrollbars',
  '--user-data-dir=' + join(outDir, 'profile'),
  'about:blank',
], { stdio: ['ignore', 'pipe', 'pipe'] })

let stderr = ''
child.stderr.on('data', (b) => { stderr += b })

const wsURL = await (async () => {
  for (let i = 0; i < 100; i++) {
    try {
      const r = await fetch(`http://127.0.0.1:${port}/json/version`)
      const j = await r.json()
      return j.webSocketDebuggerUrl
    } catch {
      await new Promise((r) => setTimeout(r, 100))
    }
  }
  throw new Error('chrome never came up:\n' + stderr)
})()

const ws = new WebSocket(wsURL)
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej })

let nextID = 1
const pending = new Map()
const events = []
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data)
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id)
    pending.delete(msg.id)
    msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result)
    return
  }
  events.push(msg)
}
const send = (method, params = {}, sessionId) =>
  new Promise((res, rej) => {
    const id = nextID++
    pending.set(id, { res, rej })
    ws.send(JSON.stringify({ id, method, params, sessionId }))
  })

const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
const s = (method, params) => send(method, params, sessionId)

await s('Page.enable')
await s('Runtime.enable')
await s('Network.enable')
const [name, value] = cookie.split('=')
const host = new URL(base).hostname
const cookies = [{ name, value, domain: host, path: '/' }]
// A theme is a cookie, so a second palette is a second cookie rather
// than a second build.
if (process.env.SHOT_PREFS) {
  cookies.push({ name: 'liseur_ui', value: process.env.SHOT_PREFS, domain: host, path: '/' })
}
await s('Network.setCookies', { cookies })

for (const width of widths) {
  await s('Emulation.setDeviceMetricsOverride', {
    width, height: 900, deviceScaleFactor: 1, mobile: false,
  })
  for (const path of paths) {
    const url = base + path
    await s('Page.navigate', { url })
    // Settle: the grid's images are lazy, so give the layout a moment
    // rather than photographing a page mid-load.
    await new Promise((r) => setTimeout(r, 700))
    const { data } = await s('Page.captureScreenshot', {
      format: 'png', captureBeyondViewport: true,
    })
    const label = (path.replace(/[^a-z0-9]+/gi, '-') || 'root').replace(/^-|-$/g, '')
    writeFileSync(join(outDir, `${process.env.SHOT_TAG || ''}${label || 'dashboard'}-${width}.png`), Buffer.from(data, 'base64'))
    console.log(`shot ${width} ${path}`)
  }
}

ws.close()
child.kill()
