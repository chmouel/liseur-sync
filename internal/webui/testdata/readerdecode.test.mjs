// Unit tests for reader-publication.js's decodeText: a valid EPUB XHTML
// or SVG document may be UTF-16 (signaled by a BOM or by the null-byte
// pattern XML permits in an unsigned declaration), or may declare some
// other single-byte encoding. A wrong guess produces replacement
// characters and usually fails XML parsing, so this pins down the
// sniffing order without needing a browser.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeText } from '../static/reader-publication.js';

function utf16(text, little, bom) {
  const bytes = [];
  if (bom) little ? bytes.push(0xff, 0xfe) : bytes.push(0xfe, 0xff);
  for (const code of [...text].map(c => c.codePointAt(0))) {
    if (code > 0xffff) throw new Error('surrogate pairs not needed for this fixture');
    const hi = (code >> 8) & 0xff, lo = code & 0xff;
    little ? bytes.push(lo, hi) : bytes.push(hi, lo);
  }
  return new Uint8Array(bytes);
}

test('UTF-8 with a BOM decodes and drops the BOM', () => {
  const bytes = new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode('café')]);
  assert.equal(decodeText(bytes), 'café');
});

test('UTF-16LE with a BOM decodes', () => {
  assert.equal(decodeText(utf16('café', true, true)), 'café');
});

test('UTF-16BE with a BOM decodes', () => {
  assert.equal(decodeText(utf16('café', false, true)), 'café');
});

test('UTF-16LE without a BOM is sniffed from the null-byte pattern of "<?xml"', () => {
  const bytes = utf16('<?xml version="1.0" encoding="UTF-16"?><p>café</p>', true, false);
  assert.equal(decodeText(bytes), '<?xml version="1.0" encoding="UTF-16"?><p>café</p>');
});

test('UTF-16BE without a BOM is sniffed from the null-byte pattern of "<?xml"', () => {
  const bytes = utf16('<?xml version="1.0" encoding="UTF-16"?><p>café</p>', false, false);
  assert.equal(decodeText(bytes), '<?xml version="1.0" encoding="UTF-16"?><p>café</p>');
});

test('an XML declaration names a non-UTF-8 single-byte encoding', () => {
  const text = '<?xml version="1.0" encoding="iso-8859-1"?><p>caf\u00e9</p>';
  const bytes = new Uint8Array([...text].map(c => c.codePointAt(0)));
  assert.equal(decodeText(bytes), text);
});

test('an unknown encoding label falls back to UTF-8', () => {
  const bytes = new TextEncoder().encode('<?xml version="1.0" encoding="not-a-real-encoding"?><p>café</p>');
  assert.equal(decodeText(bytes), '<?xml version="1.0" encoding="not-a-real-encoding"?><p>café</p>');
});

test('plain UTF-8 with no declaration decodes as UTF-8', () => {
  const bytes = new TextEncoder().encode('<p>café</p>');
  assert.equal(decodeText(bytes), '<p>café</p>');
});

test('CSS @charset names a non-UTF-8 encoding', () => {
  const text = '@charset "iso-8859-1"; body { color: caf\u00e9; }';
  const bytes = new Uint8Array([...text].map(c => c.codePointAt(0)));
  assert.equal(decodeText(bytes, { css: true }), text);
});
