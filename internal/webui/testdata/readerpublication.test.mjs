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

// build()/warm() reach document() and XMLSerializer, neither of which
// plain Node has. Standing in for them keeps these tests on the part
// that is actually under test: which chapters get built, how often, and
// when the built document is released.
function building(hrefs) {
  const made = fixture(hrefs);
  const built = [];
  made.pub.document = async href => {
    built.push(href);
    await made.pub.bytes(href, href);
    return { href };
  };
  globalThis.XMLSerializer ??= class {
    serializeToString(doc) { return `<x href="${doc.href}"/>`; }
  };
  return { ...made, built };
}

test('build() builds a spine document once and reuses it', async () => {
  const { pub, built } = building(['c1.xhtml']);
  const first = await pub.build('c1.xhtml');
  const second = await pub.build('c1.xhtml');
  assert.deepEqual(built, ['c1.xhtml']);
  assert.equal(first, second);
});

test('warm() builds a chapter nobody has asked for yet', async () => {
  const { pub, built } = building(['c1.xhtml', 'c2.xhtml']);
  pub.warm('c2.xhtml');
  await pub.documents.get('c2.xhtml');
  assert.deepEqual(built, ['c2.xhtml']);
  // The read Readium eventually does must not fetch or build again.
  await pub.get({ href: 'c2.xhtml' }).read();
  assert.deepEqual(built, ['c2.xhtml']);
});

test('warm() ignores a chapter that is not in the publication', async () => {
  const { pub, built } = building(['c1.xhtml']);
  pub.warm('nowhere.xhtml');
  pub.warm(undefined);
  assert.deepEqual(built, []);
  assert.ok(!pub.documents.has('nowhere.xhtml'));
});

test('a warm that fails is swallowed and leaves no stale entry', async () => {
  const { pub } = building(['c1.xhtml']);
  pub.document = async () => { throw new Error('no'); };
  pub.warm('c1.xhtml');
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.ok(!pub.documents.has('c1.xhtml'), 'a failed build must not be cached');
  // And the real read still fails on its own terms rather than silently.
  await assert.rejects(() => pub.get({ href: 'c1.xhtml' }).read());
});

test('retain() releases a built document with the blobs it points at', async () => {
  const { pub, built } = building(['c1.xhtml', 'c2.xhtml']);
  await pub.build('c1.xhtml');
  await pub.build('c2.xhtml');
  // A serialised chapter names blob URLs by reference. Keeping it after
  // those blobs are revoked would hand Readium a chapter of dead URLs.
  pub.retain(new Set(['c2.xhtml']));
  assert.ok(!pub.documents.has('c1.xhtml'));
  assert.ok(pub.documents.has('c2.xhtml'));
  await pub.build('c1.xhtml');
  assert.deepEqual(built, ['c1.xhtml', 'c2.xhtml', 'c1.xhtml'], 'c1 must be rebuilt, not served stale');
});

test('close() drops the built documents too', async () => {
  const { pub } = building(['c1.xhtml']);
  await pub.build('c1.xhtml');
  pub.close();
  assert.equal(pub.documents.size, 0);
});

test('a build released mid-flight refuses to hand back a stale document', async () => {
  const { pub } = building(['c1.xhtml', 'c2.xhtml']);
  let release;
  pub.document = href => new Promise(resolve => {
    pub.addReferrer(href, href); // As bytes() would, before the fetch returns.
    release = () => resolve({ href });
  });
  const pending = pub.build('c1.xhtml');
  // The reader jumps away, so retain() revokes the blob URLs this build's
  // document is about to name. It must not resolve with them anyway.
  pub.retain(new Set(['c2.xhtml']));
  release();
  await assert.rejects(() => pending, /released/);
  assert.ok(!pub.documents.has('c1.xhtml'));
});

test('a failed build does not evict the entry that replaced it', async () => {
  const { pub } = building(['c1.xhtml']);
  let fail;
  pub.document = () => new Promise((_, reject) => { fail = () => reject(new Error('gone')); });
  const first = pub.build('c1.xhtml');
  first.catch(() => {});
  pub.documents.delete('c1.xhtml');
  pub.document = async href => ({ href });
  const second = pub.build('c1.xhtml');
  fail();
  await assert.rejects(() => first);
  await second;
  assert.equal(pub.documents.get('c1.xhtml'), second, 'the newer build must survive the older failure');
});

test('a failed fetch does not evict the bytes that replaced it', async () => {
  const { pub } = fixture(['c1.xhtml']);
  let fail;
  pub.request = () => new Promise((_, reject) => { fail = () => reject(new Error('gone')); });
  const first = pub.bytes('c1.xhtml');
  first.catch(() => {});
  pub.raw.delete('c1.xhtml');
  pub.request = async () => ({ ok: true, arrayBuffer: async () => new Uint8Array([1]).buffer });
  const second = pub.bytes('c1.xhtml');
  fail();
  await assert.rejects(() => first);
  await second;
  assert.ok(pub.raw.has('c1.xhtml'), 'the newer fetch must survive the older failure');
});
