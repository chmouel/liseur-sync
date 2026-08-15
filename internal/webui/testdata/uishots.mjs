// A screenshot walk of the revamped UI (ADR-0011 phase 7).
//
// This is not an assertion; it is how a visual change gets looked at.
// It signs in with a session cookie, visits each page at three widths,
// and writes a PNG per page per width. Run it with:
//
//	LISEUR_UI_SHOTS=/tmp/ui go test ./internal/webui/ -run UIScreenshots -v
//
// scripts/screenshots.sh drives the same walk against a server holding
// real books, to make the images in the README. That is what the
// optional variables below are for.
//
// SHOT_WIDTHS narrows the walk to one width, SHOT_NAMES gives each shot
// a stable filename instead of one derived from a URL full of uuids,
// SHOT_WAIT holds a JavaScript expression per path — newline-separated,
// blank for "do not wait" — that must go truthy before the shutter
// opens, SHOT_EVAL runs one more expression per path just before it,
// and SHOT_CLIP set to `viewport` photographs the window rather than
// the whole page. The reader needs all four: it unpacks its book in the
// browser, keeps its appearance in localStorage, and lives in an iframe
// that a full-page capture leaves blank.
import { mkdirSync, writeFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { join } from 'node:path'

const chrome = process.env.SHOT_CHROME
const base = process.env.SHOT_URL
const cookie = process.env.SHOT_COOKIE
const outDir = process.env.SHOT_DIR
const paths = process.env.SHOT_PATHS.split(',')
const names = (process.env.SHOT_NAMES || '').split(',')
const waits = (process.env.SHOT_WAIT || '').split('\n')
const evals = (process.env.SHOT_EVAL || '').split('\n')
const clips = (process.env.SHOT_CLIP || '').split(',')
const widths = (process.env.SHOT_WIDTHS || '1440,900,420')
  .split(',').map((w) => Number(w.trim()))

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
  for (const [i, path] of paths.entries()) {
    const url = base + path
    await s('Page.navigate', { url })
    // Settle: the grid's images are lazy, so give the layout a moment
    // rather than photographing a page mid-load.
    await new Promise((r) => setTimeout(r, 700))
    const until = (waits[i] || '').trim()
    if (until) {
      let ready = false
      for (let tries = 0; tries < 100 && !ready; tries++) {
        const { result } = await s('Runtime.evaluate', {
          expression: `!!(${until})`, returnByValue: true,
        })
        ready = result.value === true
        if (!ready) await new Promise((r) => setTimeout(r, 200))
      }
      if (!ready) throw new Error(`gave up waiting for ${until} on ${path}`)
      // The engine has a document; let it finish painting it.
      await new Promise((r) => setTimeout(r, 500))
    }
    const setup = (evals[i] || '').trim()
    if (setup) {
      const { exceptionDetails } = await s('Runtime.evaluate', {
        expression: setup, awaitPromise: true,
      })
      if (exceptionDetails) {
        throw new Error(`SHOT_EVAL failed on ${path}: ${exceptionDetails.text}`)
      }
      await new Promise((r) => setTimeout(r, 700))
    }
    // captureBeyondViewport photographs a whole page by growing it,
    // which is right for a scrolling list and wrong for the reader:
    // the book lives in an iframe that comes back blank when the page
    // is resized under it. A page that fills the window says so.
    const { data } = await s('Page.captureScreenshot', {
      format: 'png', captureBeyondViewport: (clips[i] || '').trim() !== 'viewport',
    })
    const derived = (path.replace(/[^a-z0-9]+/gi, '-') || 'root').replace(/^-|-$/g, '')
    const label = (names[i] || '').trim() || derived || 'dashboard'
    const suffix = names[i] && widths.length === 1 ? '' : `-${width}`
    writeFileSync(join(outDir, `${process.env.SHOT_TAG || ''}${label}${suffix}.png`), Buffer.from(data, 'base64'))
    console.log(`shot ${width} ${path}`)
  }
}

ws.close()
child.kill()
