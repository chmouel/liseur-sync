// Web half of the setup-guide screenshots, after KOReader has run. It does
// not photograph anything: it checks that the position KOReader pushed with
// Progress sync is on the server, which is the claim the guide ends on.
import { join } from 'node:path'
import { launch, sleep } from './cdp.mjs'

const { SHOT_CHROME: chrome, SHOT_URL: base, SHOT_COOKIE: cookie, ZIP_DIR: zipDir } = process.env
const b = await launch({ chrome, base, cookie, profile: join(zipDir, '.profile-after') })

await b.goto('/ui/library?filter=all')
await sleep(800)
const text = (await b.ev('document.body.innerText')).replace(/\s+/g, ' ')
const progress = text.match(/(\d+)%\s+Synced elsewhere/)
if (!progress) throw new Error('the server has no position from KOReader: Progress sync did not arrive')
console.log(`the server holds a position from KOReader (${progress[1]}%)`)
b.close()
process.exit(0)
