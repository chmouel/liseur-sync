// Unit tests for ReaderPublication's cache: raw bytes and blobs must not be
// retained for the whole reading session, only for spine documents inside
// Readium's active frame window (see reader-engine.js's releaseOutOfWindow).
// document()/assetURL() need a DOM (DOMParser, Blob) that plain Node does not
// have, so these exercise bytes()/retain() directly against the byte-level
// cache and referrer bookkeeping they share with the blob path.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReaderPublication } from '../static/reader-publication.js';

function fixture(hrefs) {
  const bodies = new Map([...hrefs, 'package.opf', 'style.css'].map(href => [href, new TextEncoder().encode(href)]));
  const requested = [];
  const request = async path => {
    const href = path.replace(/^prefix\//, '');
    requested.push(href);
    return { ok: true, arrayBuffer: async () => bodies.get(href).buffer };
  };
  const manifest = {
    readingOrder: hrefs.map(href => ({ href, type: 'application/xhtml+xml' })),
    resources: [{ href: 'style.css', type: 'text/css' }],
  };
  const pub = new ReaderPublication({ request, current: () => true, prefix: 'prefix/', manifest, packageHref: 'package.opf' });
  return { pub, requested };
}

test('bytes() caches and does not refetch on a second call', async () => {
  const { pub, requested } = fixture(['c1.xhtml']);
  await pub.bytes('c1.xhtml');
  await pub.bytes('c1.xhtml');
  assert.deepEqual(requested, ['c1.xhtml']);
});

test('retain() releases a chapter whose only referrer left the window', async () => {
  const { pub } = fixture(['c1.xhtml', 'c2.xhtml']);
  await pub.bytes('c1.xhtml', 'c1.xhtml');
  await pub.bytes('c2.xhtml', 'c2.xhtml');
  assert.ok(pub.raw.has('c1.xhtml'));
  pub.retain(new Set(['c2.xhtml']));
  assert.ok(!pub.raw.has('c1.xhtml'));
  assert.ok(pub.raw.has('c2.xhtml'));
});

test('retain() keeps a shared asset referenced by a chapter still in the window', async () => {
  const { pub } = fixture(['c1.xhtml', 'c2.xhtml']);
  // style.css is shared: both chapters reference it (as assetURL() would
  // record via addReferrer before caching its blob).
  await pub.bytes('style.css', 'c1.xhtml');
  pub.addReferrer('style.css', 'c2.xhtml');
  pub.retain(new Set(['c2.xhtml']));
  assert.ok(pub.raw.has('style.css'), 'style.css should survive: c2.xhtml still references it');
});

test('retain() never releases the package document', async () => {
  const { pub } = fixture(['c1.xhtml']);
  await pub.bytes('package.opf', 'package.opf');
  pub.retain(new Set(['c1.xhtml']));
  assert.ok(pub.raw.has('package.opf'));
});

test('a fetch failure does not leave a stale cache entry', async () => {
  const bodies = new Map();
  const request = async () => ({ ok: false, status: 500 });
  const manifest = { readingOrder: [{ href: 'c1.xhtml', type: 'application/xhtml+xml' }], resources: [] };
  const pub = new ReaderPublication({ request, current: () => true, prefix: 'prefix/', manifest, packageHref: 'package.opf' });
  await assert.rejects(() => pub.bytes('c1.xhtml'));
  assert.ok(!pub.raw.has('c1.xhtml'));
  void bodies;
});
