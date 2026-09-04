// Unit tests for the footer's page number (reader-positions.js).
// Run by TestReaderPositionArithmetic through `node --test`; no browser.
//
// The numbers these pin down are Readium's, because that is the whole
// point of the module: the page the app shows and the page the browser
// shows have to be the same page.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { positionTable, pageAt, pageLocation, PAGE_LENGTH } from '../static/reader-positions.js';

const section = (compressedSize, extra = {}) => ({ compressedSize, ...extra });
const table = (...sections) => positionTable(sections);

test('a resource is at least one page, however short', () => {
  const t = table(section(1), section(0), section(1023));
  assert.deepEqual(t.counts, [1, 1, 1]);
  assert.equal(t.total, 3);
});

test('a resource is its stored bytes over 1024, rounded up', () => {
  assert.equal(PAGE_LENGTH, 1024);
  const t = table(section(1024), section(1025), section(4096));
  assert.deepEqual(t.counts, [1, 2, 4]);
  assert.deepEqual(t.starts, [0, 1, 3]);
  assert.equal(t.total, 7);
});

test('non-linear sections count for nothing', () => {
  // Readium drops them from the reading order before it counts, so they
  // must not swell the total either.
  const t = table(
    section(4096),
    section(999999, { linear: 'no' }),
    section(2048),
  );
  assert.deepEqual(t.counts, [4, 0, 2]);
  assert.equal(t.total, 6);
});

test('a section with no archive length falls back to its own length', () => {
  // A book that did not come out of an archive: the same fallback chain
  // Readium walks when a resource has no archive properties.
  const t = positionTable([{ size: 4096 }, { size: 2048 }]);
  assert.deepEqual(t.counts, [4, 2]);
});

test('the archive length wins over the resource length', () => {
  const t = table(section(1024, { size: 999999 }));
  assert.deepEqual(t.counts, [1]);
});

test('a book with nothing measurable in it has no opinion', () => {
  assert.equal(positionTable([]), null);
  assert.equal(positionTable(null), null);
  assert.equal(positionTable([{ linear: 'no', compressedSize: 4096 }]), null);
});

test('the first page is 1 and the last page is the total', () => {
  const t = table(section(4096), section(2048));
  assert.equal(pageAt(t, 0, 0), 1);
  assert.equal(pageAt(t, 1, 1), t.total);
  assert.equal(t.total, 6);
});

test('pages march forward within a resource and across its edge', () => {
  const t = table(section(4096), section(4096));
  // Readium spreads a resource's positions evenly: position p starts at
  // (p - 1) / count.
  assert.deepEqual([0, 0.25, 0.5, 0.75].map((f) => pageAt(t, 0, f)), [1, 2, 3, 4]);
  // The very end of a section is its last page, never the next one's
  // first.
  assert.equal(pageAt(t, 0, 1), 4);
  assert.equal(pageAt(t, 1, 0), 5);
  assert.deepEqual([0.25, 0.5, 0.75, 1].map((f) => pageAt(t, 1, f)), [6, 7, 8, 8]);
});

test('a page never leaves the book', () => {
  const t = table(section(4096));
  assert.equal(pageAt(t, 0, -5), 1);
  assert.equal(pageAt(t, 0, 5), t.total);
  assert.equal(pageAt(t, 0, NaN), 1);
  assert.equal(pageAt(t, 0, undefined), 1);
});

test('a section the reader can reach but Readium never counted', () => {
  // Following a link into a non-linear section: the page is the
  // boundary it sits on, not a number outside the book.
  const t = table(section(4096), section(999999, { linear: 'no' }), section(2048));
  assert.equal(pageAt(t, 1, 0.5), 4);
});

test('nothing to say, said as nothing', () => {
  const t = table(section(4096));
  assert.equal(pageAt(null, 0, 0), null);
  assert.equal(pageAt(t, -1, 0), null);
  assert.equal(pageAt(t, 9, 0), null);
  assert.equal(pageAt(t, undefined, 0), null);
});

test('a real book comes out at the number the app shows', () => {
  // "Dominium Mundi Livre 2", 14 spine items, stored lengths as they sit
  // in the archive. Readium 3.3.0 counts 578 positions; foliate's own
  // locations counter says 1156, and that disagreement is what this
  // module exists to end.
  const stored = [
    392, 1456, 325, 858, 321, 117517, 134039, 321,
    72116, 108814, 119685, 26728, 1270, 521,
  ];
  const t = table(...stored.map((n) => section(n)));
  assert.equal(t.total, 578);
  assert.equal(pageAt(t, 0, 0), 1);
  assert.equal(pageAt(t, stored.length - 1, 1), 578);
});

test('pageLocation round-trips exactly through pageAt for every page', () => {
  const t = table(section(1024), section(2048), section(4096));
  for (let p = 1; p <= t.total; p++) {
    const loc = pageLocation(t, p);
    assert.ok(loc, `expected location for page ${p}`);
    assert.equal(pageAt(t, loc.index, loc.anchor), p);
  }
});

test('pageLocation skips non-linear sections properly', () => {
  const t = table(
    section(2048, { linear: 'no' }),
    section(4096),
    section(1024, { linear: 'no' }),
    section(2048),
  );
  // Total is 4 + 2 = 6 pages
  assert.equal(t.total, 6);
  // Page 1 is in section 1
  const loc1 = pageLocation(t, 1);
  assert.equal(loc1.index, 1);
  assert.equal(pageAt(t, loc1.index, loc1.anchor), 1);
  // Page 5 is in section 3
  const loc5 = pageLocation(t, 5);
  assert.equal(loc5.index, 3);
  assert.equal(pageAt(t, loc5.index, loc5.anchor), 5);
});

test('pageLocation clamps out-of-range pages and rounds floats', () => {
  const t = table(section(4096));
  assert.equal(pageLocation(t, 0).anchor, pageLocation(t, 1).anchor);
  assert.equal(pageLocation(t, -10).anchor, pageLocation(t, 1).anchor);
  assert.equal(pageLocation(t, 999).anchor, pageLocation(t, t.total).anchor);
  assert.equal(pageLocation(t, 2.2).anchor, pageLocation(t, 2).anchor);
});

test('pageLocation returns null for invalid inputs', () => {
  const t = table(section(4096));
  assert.equal(pageLocation(null, 1), null);
  assert.equal(pageLocation(t, NaN), null);
  assert.equal(pageLocation(t, Infinity), null);
  assert.equal(pageLocation(t, -Infinity), null);
  assert.equal(pageLocation(t, 'not a number'), null);
  assert.equal(pageLocation({ total: 0 }, 1), null);
});
