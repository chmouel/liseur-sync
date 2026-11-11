// Web half of the setup-guide screenshots, before KOReader runs: the
// "Set up KOReader" card, the real download and the Devices page that
// lists the three credentials it created.
import { renameSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { launch, sleep } from './cdp.mjs'

const { SHOT_CHROME: chrome, SHOT_URL: base, SHOT_COOKIE: cookie, IMG_DIR: img, ZIP_DIR: zipDir, READER_NAME: readerName } = process.env
const b = await launch({ chrome, base, cookie, profile: join(zipDir, '.profile'), downloadDir: zipDir })

await b.goto('/ui/settings?section=devices')
// A narrower window makes the text in these crops large enough to read once
// they are shown at half the page width.
await b.setWidth(820)
await sleep(400)
const form = 'form[action$="koreader/setup"]'
if (!(await b.ev(`!!document.querySelector('${form}')`))) throw new Error('the setup form is not on the page: are Progress sync and statistics enabled?')
await b.ev(`document.querySelector('${form} input[name=name]').value = ${JSON.stringify(readerName)}`)
await b.ev(`document.querySelector('${form}').closest('section').id = 'koreader-card'`)
await b.shootElement('#koreader-card', join(img, 'web-setup-card.png'))

await b.ev(`document.querySelector('${form} button[type=submit]').click()`)
for (let i = 0; i < 75; i++) {
  await sleep(200)
  if (b.events.some((e) => e.method === 'Browser.downloadProgress' && e.params.state === 'completed')) break
}
if (!b.events.some((e) => e.method === 'Browser.downloadWillBegin')) throw new Error('Chrome never started a download')
await sleep(300)
const zip = readdirSync(zipDir).find((f) => f.endsWith('.zip'))
if (!zip) throw new Error('no ZIP arrived')
renameSync(join(zipDir, zip), join(zipDir, 'setup.zip'))
console.log('downloaded', zip)

// The three credentials the download created, each on the page where it is
// revoked: the API token under Apps, the other two under E-readers.
for (const [heading, file, view] of [
  ['API tokens', 'web-cred-token.png', '&view=apps'],
  ['KOReader (kosync)', 'web-cred-sync.png', ''],
  ['KOReader statistics plugin', 'web-cred-stats.png', ''],
]) {
  await b.goto('/ui/settings?section=devices' + view)
  await b.setWidth(820)
  await sleep(400)
  const marked = await b.ev(`(() => { const h = [...document.querySelectorAll('h2')].find((e) => e.textContent.trim() === ${JSON.stringify(heading)}); const sec = h && h.closest('section, details'); if (!sec) return false; if (sec.tagName === 'DETAILS') sec.open = true; sec.id = 'cred-' + ${JSON.stringify(file)}.replace(/\\W/g, ''); return sec.id })()`)
  if (!marked) throw new Error('no section titled ' + heading)
  await b.shootElement('#' + marked, join(img, file))
}
b.close()
process.exit(0)
