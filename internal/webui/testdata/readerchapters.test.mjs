import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  archiveKey,
  buildChapters,
  chapterAt,
  chapterForLocation,
  pagesLeftInChapter,
} from '../static/reader-chapters.js';

const sections = (titles = []) => [
  { id: 'OPS/cover.xhtml', title: titles[0] ?? null },
  { id: 'OPS/chapter.xhtml', title: titles[1] ?? null },
  { id: 'OPS/chapter.xhtml', title: titles[2] ?? null },
  { id: 'OPS/end.xhtml', title: titles[3] ?? null },
];
const table = { counts: [1, 2, 2, 1], starts: [0, 1, 3, 5], total: 6 };

test('archive keys are local, normalized, and fragment/query free', () => {
  assert.equal(archiveKey('OPS/chapter.xhtml#part-1'), 'OPS/chapter.xhtml');
  assert.equal(archiveKey('./chapter.xhtml?x=1', 'OPS/package.opf'), 'OPS/chapter.xhtml');
  assert.equal(archiveKey('OPS/%63hapter.xhtml'), 'OPS/chapter.xhtml');
  assert.equal(archiveKey('../outside.xhtml', 'OPS/package.opf'), 'outside.xhtml');
  assert.equal(archiveKey('https://example.test/book.xhtml'), null);
  assert.equal(archiveKey('//example.test/book.xhtml'), null);
  assert.equal(archiveKey('OPS/%2Fchapter.xhtml'), null);
  assert.equal(archiveKey('OPS/%E0%A4%A.xhtml'), null);
});

test('chapters preserve resource indexes and Android continuation rules', () => {
  const result = buildChapters([
    { label: 'Chapter one', href: 'chapter.xhtml#start' },
    { label: 'Duplicate chapter', href: 'chapter.xhtml#other' },
    { label: 'Ending', href: 'end.xhtml' },
    { label: 'Missing', href: 'missing.xhtml' },
  ], sections(), table, 'OPS/package.opf');

  assert.deepEqual(result.chapters.map(ch => [ch.title, ch.firstPosition, ch.lastPosition]), [
    [null, 1, 3],
    ['Chapter one', 4, 5],
    ['Ending', 6, 6],
  ]);
  assert.equal(result.chapterIndexByResource.get(1), 0);
  assert.equal(result.chapterIndexByResource.get(2), 1);
  assert.equal(result.chapterIndexByResource.get(3), 2);
  assert.equal(chapterAt(result.chapters, 4).title, 'Chapter one');
  assert.equal(chapterForLocation(result.chapters, result.chapterIndexByResource, 1, 6).title, 'Ending');
  assert.equal(chapterForLocation(result.chapters, result.chapterIndexByResource, 1, 4).title, 'Chapter one');
});

test('reading-order titles name resources without TOC entries', () => {
  const result = buildChapters([], sections([null, 'Reading order title']), table);
  assert.equal(result.chapters[1].title, 'Reading order title');
});

test('unusable resources are skipped without extending a chapter', () => {
  const result = buildChapters(
    [{ label: 'First', href: 'first.xhtml' }, { label: 'Last', href: 'last.xhtml' }],
    [{ id: 'first.xhtml' }, { id: 'empty.xhtml' }, { id: 'last.xhtml' }],
    { counts: [1, 0, 1], starts: [0, 1, 1], total: 2 },
  );
  assert.deepEqual(result.chapters.map(ch => [ch.title, ch.firstPosition, ch.lastPosition]), [
    ['First', 1, 1],
    ['Last', 2, 2],
  ]);
  assert.equal(result.chapterIndexByResource.has(1), false);
});

test('invalid position tables produce no chapters', () => {
  assert.deepEqual(buildChapters([], sections(), null), { chapters: [], chapterIndexByResource: new Map() });
  assert.deepEqual(buildChapters([], sections(), { counts: [1], starts: [0], total: 1 }), {
    chapters: [], chapterIndexByResource: new Map(),
  });
});

test('remaining-position wording handles plural, singular, and last page', () => {
  const chapter = { title: 'Chapter', firstPosition: 1, lastPosition: 3 };
  assert.equal(pagesLeftInChapter(chapter, 1), 2);
  assert.equal(pagesLeftInChapter(chapter, 2), 1);
  assert.equal(pagesLeftInChapter(chapter, 3), 0);
  assert.equal(pagesLeftInChapter({ ...chapter, title: null }, 2), null);
});
