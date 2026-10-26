import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bookAt } from '../static/book-audio.js';

const mapping = {
  segments: [
    { a0: 0, a1: 60, b0: 0.1, b1: 0.5 },
    { a0: 60, a1: 120, b0: 0.5, b1: 1 },
  ],
};

test('a sitting that starts at the top of the audio starts at its first segment', () => {
  assert.equal(bookAt(mapping, 0), 0.1);
});

test('audio time interpolates within its segment, as the server does', () => {
  assert.ok(Math.abs(bookAt(mapping, 30) - 0.3) < 1e-9);
  assert.equal(bookAt(mapping, 60), 0.5);
});

test('the end stays short of finished unless the mapping is the full narrative', () => {
  assert.equal(bookAt(mapping, 120), 0.998);
  assert.equal(bookAt({ ...mapping, full_narrative: true }, 120), 1);
});

test('outside every segment there is no answer', () => {
  assert.ok(Number.isNaN(bookAt(mapping, 200)));
  assert.ok(Number.isNaN(bookAt(null, 10)));
});
