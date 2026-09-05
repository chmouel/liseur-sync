// Unit tests for the reader's session accounting (reader-session.js).
// Run by TestReaderSessionAccounting through `node --test`; no browser.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { openSession, IDLE_AFTER_MS, MIN_ACTIVE_MS } from '../static/reader-session.js';

const MIN = 60 * 1000;
const t0 = new Date('2026-09-04T10:00:00Z');
const wall = (ms) => new Date(t0.getTime() + ms);
const open = (fraction = 0.1, now = 1000) =>
  openSession({ id: 's1', workID: 'w1', startedAt: t0, now, fraction });

test('a page every minute is all reading', () => {
  const s = open();
  for (let m = 1; m <= 20; m++) s.activity(1000 + m * MIN, 0.1 + m * 0.01);
  const out = s.close(1000 + 21 * MIN, wall(21 * MIN), 0.31);
  assert.equal(out.idle_ms, 0);
  assert.equal(out.start_progression, 0.1);
  assert.equal(out.end_progression, 0.31);
  assert.equal(out.started_at, t0.toISOString());
  assert.equal(out.ended_at, wall(21 * MIN).toISOString());
  assert.equal(out.session_id, 's1');
  assert.equal(out.work_id, 'w1');
});

test('a long gap credits the threshold and counts the rest idle', () => {
  const s = open();
  s.activity(1000 + 1 * MIN, 0.11);
  s.activity(1000 + 11 * MIN, 0.12); // ten minutes on one page
  const out = s.close(1000 + 12 * MIN, wall(12 * MIN), 0.13);
  assert.equal(out.idle_ms, 10 * MIN - IDLE_AFTER_MS);
});

test('silence before closing is idle past the threshold', () => {
  const s = open();
  s.activity(1000 + 1 * MIN, 0.11);
  const out = s.close(1000 + 21 * MIN, wall(21 * MIN), 0.11);
  assert.equal(out.idle_ms, 20 * MIN - IDLE_AFTER_MS);
});

test('a sitting under the minimum is not sent', () => {
  const s = open();
  s.activity(1000 + 3000, 0.2);
  assert.equal(s.close(1000 + MIN_ACTIVE_MS - 1, wall(MIN_ACTIVE_MS - 1), 0.2), null);
});

test('a page left open is credited the threshold and no more', () => {
  const s = open();
  // Thirty minutes on screen with no page turned: one threshold of
  // reading, the rest idle.
  const out = s.close(1000 + 30 * MIN, wall(30 * MIN), 0.1);
  assert.equal(out.idle_ms, 30 * MIN - IDLE_AFTER_MS);
  const span = Date.parse(out.ended_at) - Date.parse(out.started_at);
  assert.equal(span - out.idle_ms, IDLE_AFTER_MS);
});

test('a NaN fraction never becomes a progression', () => {
  const s = open(NaN);
  s.activity(1000 + MIN, NaN);
  s.activity(1000 + 2 * MIN, 0.4);
  s.activity(1000 + 3 * MIN, Infinity);
  const out = s.close(1000 + 4 * MIN, wall(4 * MIN), undefined);
  assert.equal(out.start_progression, 0.4);
  assert.equal(out.end_progression, 0.4);
});

test('a sitting that never measured a position says nothing', () => {
  const s = open(NaN);
  assert.equal(s.close(1000 + 5 * MIN, wall(5 * MIN), NaN), null);
});

test('progressions are clamped to [0,1]', () => {
  const s = open(-0.2);
  const out = s.close(1000 + MIN, wall(MIN), 1.5);
  assert.equal(out.start_progression, 0);
  assert.equal(out.end_progression, 1);
});

test('a second close yields nothing and late activity is ignored', () => {
  const s = open();
  const out = s.close(1000 + MIN, wall(MIN), 0.5);
  assert.ok(out);
  assert.equal(s.close(1000 + 2 * MIN, wall(2 * MIN), 0.9), null);
  s.activity(1000 + 3 * MIN, 0.9);
  assert.equal(s.close(1000 + 3 * MIN, wall(3 * MIN), 0.9), null);
});

test('ended_at is never before started_at and idle never exceeds the span', () => {
  // Wall clock jumped backwards while the monotonic clock ran on.
  const s = open();
  const out = s.close(1000 + 30 * MIN, wall(-5 * MIN), 0.2);
  assert.equal(out.ended_at, t0.toISOString());
  assert.equal(out.idle_ms, 0);
});

test('a wall clock corrected forward lands in idle, not in reading', () => {
  // One minute of reading, page turns throughout, but the wall clock
  // was put right by an hour in the middle of it. The server derives
  // active time as span minus idle, so the hour must be in idle_ms.
  const s = open();
  s.activity(1000 + 30000, 0.15);
  const out = s.close(1000 + MIN, wall(61 * MIN), 0.2);
  const span = Date.parse(out.ended_at) - Date.parse(out.started_at);
  assert.equal(span, 61 * MIN);
  assert.equal(span - out.idle_ms, MIN);
});

test('negotiated active time survives a backward wall-clock correction', () => {
  const s = openSession({
    id: 'measured', workID: 'w1', startedAt: t0, now: 0,
    fraction: 0.1, supportsActiveMs: true,
  });
  for (let m = 1; m <= 30; m++) s.activity(m * MIN, 0.1 + m / 100);
  const out = s.close(30 * MIN, wall(10 * MIN), 0.4);
  assert.equal(out.active_ms, 30 * MIN);
  assert.equal(Date.parse(out.ended_at) - Date.parse(out.started_at), 10 * MIN);
  assert.equal(out.idle_ms, 0);
});

test('measured time retains idle accounting and does not fabricate timestamps', () => {
  const s = openSession({
    id: 'idle', workID: 'w1', startedAt: t0, now: 0,
    fraction: 0.1, supportsActiveMs: true,
  });
  const out = s.close(30 * MIN, wall(-MIN), 0.1);
  assert.equal(out.active_ms, IDLE_AFTER_MS);
  assert.equal(out.ended_at, out.started_at);
  assert.equal(out.idle_ms, 0);
});

test('a legacy payload never gains active_ms without capability negotiation', () => {
  const out = open().close(1000 + MIN, wall(MIN), 0.2);
  assert.equal(Object.hasOwn(out, 'active_ms'), false);
});

test('a relocate the reader did not cause is not activity', () => {
  const s = open();
  // Twenty minutes of the engine re-anchoring on resizes, no input.
  for (let m = 1; m <= 20; m++) s.progress(0.1 + m * 0.001);
  const out = s.close(1000 + 20 * MIN, wall(20 * MIN), 0.13);
  assert.equal(out.idle_ms, 20 * MIN - IDLE_AFTER_MS);
  assert.equal(out.end_progression, 0.13);
  assert.equal(out.start_progression, 0.1);
});

test('progress supplies the first position when the open had none', () => {
  const s = open(NaN);
  s.progress(0.3);
  s.activity(1000 + MIN, NaN);
  const out = s.close(1000 + 2 * MIN, wall(2 * MIN), NaN);
  assert.equal(out.start_progression, 0.3);
  assert.equal(out.end_progression, 0.3);
});

test('a monotonic clock that steps back adds nothing', () => {
  const s = open(0.1, 5000);
  s.activity(1000, 0.2); // before the open moment
  const out = s.close(5000 + MIN, wall(MIN), 0.3);
  assert.equal(out.idle_ms, 0);
  assert.equal(out.end_progression, 0.3);
});

test('server invariants hold for random event sequences', () => {
  let seed = 42;
  const rnd = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 500; i++) {
    const s = openSession({ id: 'r', workID: 'w', startedAt: t0, now: 0, fraction: rnd() });
    let at = 0;
    const n = Math.floor(rnd() * 30);
    for (let k = 0; k < n; k++) {
      at += Math.floor(rnd() * 10 * MIN);
      s.activity(at, rnd() < 0.2 ? NaN : rnd() * 1.2 - 0.1);
    }
    at += Math.floor(rnd() * 10 * MIN);
    const drift = Math.floor((rnd() - 0.5) * 2000);
    const out = s.close(at, wall(at + drift), rnd());
    if (!out) continue;
    const started = Date.parse(out.started_at);
    const ended = Date.parse(out.ended_at);
    assert.ok(ended >= started);
    assert.ok(out.idle_ms >= 0 && out.idle_ms <= ended - started, `${out.idle_ms} > ${ended - started}`);
    for (const p of [out.start_progression, out.end_progression]) {
      assert.ok(Number.isFinite(p) && p >= 0 && p <= 1);
    }
  }
});
