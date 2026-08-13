// Tests for the parts of the web reader that are easy to get quietly
// wrong: the ZIP central-directory reader, and the arithmetic that
// turns a position into a place in the book and back.
//
// Run from Go (reader_js_test.go) so it travels with `go test` rather
// than needing a JavaScript toolchain of its own. The rendering half of
// reader.js needs a DOM and is exercised by the Go tests instead.

'use strict';

const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const reader = require(path.join(__dirname, '..', 'static', 'reader.js'));
const { Zip, resolvePath, placeFromProgression, placeFromLocator, locatorFor, mediaType } = reader;

let failures = 0;
async function test(name, fn) {
  try {
    await fn();
    console.log('ok   - ' + name);
  } catch (err) {
    failures++;
    console.log('FAIL - ' + name + ': ' + (err && err.message));
  }
}

function archive(file) {
  const buf = fs.readFileSync(file);
  return new Zip(buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength));
}

(async function () {
  const archivePath = process.argv[2];

  await test('reads a real archive, stored and deflated alike', async () => {
    const zip = archive(archivePath);

    // mimetype is stored uncompressed by every EPUB writer; the rest is
    // deflated. Both paths have to work or the reader opens nothing.
    assert.strictEqual(await zip.text('mimetype'), 'application/epub+zip');
    assert.ok(zip.has('META-INF/container.xml'), 'container.xml missing');

    const chapter = await zip.text('OEBPS/chapter1.xhtml');
    assert.ok(chapter.includes('Call me Ishmael'), 'chapter text did not survive inflate');
    assert.ok(chapter.length > 1000, 'deflated entry came back truncated');
  });

  await test('a name that is not in the archive is an error, not empty bytes', async () => {
    await assert.rejects(() => archive(archivePath).read('OEBPS/nope.xhtml'));
  });

  await test('something that is not a zip is refused', () => {
    assert.throws(() => new Zip(new TextEncoder().encode('x'.repeat(200)).buffer));
  });

  await test('hrefs resolve against the file that referenced them', () => {
    assert.strictEqual(resolvePath('OEBPS/content.opf', 'chapter1.xhtml'), 'OEBPS/chapter1.xhtml');
    assert.strictEqual(resolvePath('OEBPS/text/c1.xhtml', '../images/a.png'), 'OEBPS/images/a.png');
    assert.strictEqual(resolvePath('OEBPS/content.opf', './style.css'), 'OEBPS/style.css');
    // A fragment names a place inside the file, not a different file.
    assert.strictEqual(resolvePath('OEBPS/c.opf', 'ch1.xhtml#part2'), 'OEBPS/ch1.xhtml');
    // Percent-encoding is how a space survives being a URL.
    assert.strictEqual(resolvePath('OEBPS/c.opf', 'my%20chapter.xhtml'), 'OEBPS/my chapter.xhtml');
  });

  await test('a progression names a chapter and a place inside it', () => {
    assert.deepStrictEqual(placeFromProgression(4, 0), { index: 0, fraction: 0 });
    assert.deepStrictEqual(placeFromProgression(4, 0.25), { index: 1, fraction: 0 });
    const mid = placeFromProgression(4, 0.375);
    assert.strictEqual(mid.index, 1);
    assert.ok(Math.abs(mid.fraction - 0.5) < 1e-9, 'halfway through chapter 2');

    // The end of the book is the end of the last chapter, not the
    // start of a chapter that does not exist. Reading to the end and
    // reopening must not throw the reader off the end of the spine.
    const end = placeFromProgression(4, 1);
    assert.strictEqual(end.index, 3);
    // Nonsense from an older client should still land somewhere real.
    assert.deepStrictEqual(placeFromProgression(4, -1), { index: 0, fraction: 0 });
    assert.strictEqual(placeFromProgression(4, undefined).index, 0);
  });

  await test('a locator beats a fraction, and a foreign locator falls back to one', () => {
    const epub = {
      spine: [{ path: 'a.xhtml' }, { path: 'b.xhtml' }, { path: 'c.xhtml' }, { path: 'd.xhtml' }],
    };

    // Our own locator: believed exactly.
    const exact = placeFromLocator(epub,
      { href: 'c.xhtml', locations: { progression: 0.5, totalProgression: 0.9 } }, 0.9);
    assert.deepStrictEqual(exact, { index: 2, fraction: 0.5 },
      'a locator naming a resource we have must win over the fraction');

    // A locator from a reader that lays the book out differently: the
    // href means nothing here, so fall back to what everyone shares.
    const foreign = placeFromLocator(epub,
      { href: 'OEBPS/other.xhtml', locations: { totalProgression: 0.5 } }, 0.5);
    assert.strictEqual(foreign.index, 2, 'unknown href should fall back to progression');

    // kosync-origin ops carry no locator at all.
    assert.strictEqual(placeFromLocator(epub, null, 0.25).index, 1);
    assert.strictEqual(placeFromLocator(epub, undefined, 0).index, 0);
  });

  await test('the locator we emit is a Readium locator and round-trips', () => {
    const epub = {
      spine: [{ path: 'a.xhtml', type: 'application/xhtml+xml' },
        { path: 'b.xhtml', type: 'application/xhtml+xml' }],
      title: 'Moby-Dick',
    };
    const loc = locatorFor(epub, 1, 0.5);
    assert.strictEqual(loc.href, 'b.xhtml');
    assert.strictEqual(loc.type, 'application/xhtml+xml');
    assert.strictEqual(loc.locations.progression, 0.5);
    assert.strictEqual(loc.locations.totalProgression, 0.75);
    assert.ok(JSON.stringify(loc).length > 0, 'must survive JSON');

    // The property that matters: what we write, we can read back.
    const back = placeFromLocator(epub, loc, loc.locations.totalProgression);
    assert.deepStrictEqual(back, { index: 1, fraction: 0.5 });
  });

  await test('media types come from the extension, unknown ones stay opaque', () => {
    assert.strictEqual(mediaType('OEBPS/a.JPG'), 'image/jpeg');
    assert.strictEqual(mediaType('OEBPS/f.woff2'), 'font/woff2');
    assert.strictEqual(mediaType('OEBPS/x.bin'), 'application/octet-stream');
  });

  if (failures > 0) {
    console.error(failures + ' assertion(s) failed');
    process.exit(1);
  }
})();
