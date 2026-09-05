import { test } from 'node:test';
import assert from 'node:assert/strict';
import { uploadSessions } from '../static/reader-session-upload.js';

const item = (id) => ({ session_id: id, work_id: 'work', idle_ms: 0 });
const reply = (status, body) => new Response(JSON.stringify(body), { status });
const ok = (body) => reply(200, { accepted: JSON.parse(body).sessions.length });

async function run(pending, responses, extra = {}) {
  const requests = [], failures = [];
  await uploadSessions(pending.slice(), {
    canSend: () => true,
    responseCurrent: () => true,
    deferred: () => {},
    send: async (body) => {
      requests.push(body);
      const response = responses.shift();
      assert.ok(response, 'unexpected request');
      return typeof response === 'function' ? response(body) : response;
    },
    accepted: (batch) => {
      for (const value of batch) {
        const index = pending.indexOf(value);
        if (index >= 0) pending.splice(index, 1);
      }
    },
    refused: (value, code) => {
      failures.push({ value, code });
      const index = pending.indexOf(value);
      if (index >= 0) pending.splice(index, 1);
    },
    ...extra,
  });
  return { requests: requests.map(JSON.parse), failures };
}

test('a named refusal removes only the bad sitting and retries its peers', async () => {
  const pending = [item('a'), item('bad'), item('b')];
  const result = await run(pending, [
    reply(409, { code: 'id_reused', item_index: 1, session_id: 'bad' }),
    reply(200, { accepted: 2 }),
  ]);
  assert.deepEqual(result.requests[1].sessions.map((v) => v.session_id), ['a', 'b']);
  assert.equal(result.failures.length, 1);
  assert.deepEqual(pending, []);
});

test('an item index takes precedence over an ambiguous id', async () => {
  const pending = [item('same'), item('same')];
  const first = pending[0];
  const result = await run(pending, [
    reply(409, { code: 'id_reused', item_index: 1, session_id: 'same' }),
    reply(200, { accepted: 1 }),
  ]);
  assert.notEqual(result.failures[0].value, first);
  assert.deepEqual(pending, []);
});

test('a refused oversized batch is split to the server limit', async () => {
  const pending = ['a', 'b', 'c'].map(item);
  const result = await run(pending, [
    reply(400, { code: 'batch_too_large', limit: 1 }),
    ok, ok, ok,
  ]);
  assert.deepEqual(result.requests.map((v) => v.sessions.length), [3, 1, 1, 1]);
  assert.deepEqual(pending, []);
});

test('a proxy HTML 413 splits instead of discarding the batch', async () => {
  const pending = [item('a'), item('b')];
  await run(pending, [new Response('<html>large</html>', { status: 413 }),
    ok, ok]);
  assert.deepEqual(pending, []);
});

test('a singleton still refused for size remains queued', async () => {
  const pending = [item('a')];
  await run(pending, [reply(413, {})]);
  assert.equal(pending.length, 1);
});

test('retryable and unknown refusals leave even named items queued', async () => {
  for (const [status, code] of [[429, 'limited'], [503, 'unavailable'],
    [400, 'new_refusal'], [400, 'unknown_work'], [403, 'forbidden'], [409, undefined]]) {
    const pending = [item('a'), item('b')];
    await run(pending, [reply(status, { code, item_index: 0, session_id: 'a' })]);
    assert.equal(pending.length, 2, `${status} ${code}`);
  }
});

test('an unnamed permanent refusal bisects and preserves the good half', async () => {
  const pending = [item('a'), item('bad')];
  const result = await run(pending, [
    reply(400, { code: 'bad_time' }), ok,
    reply(400, { code: 'bad_time' }),
  ]);
  assert.deepEqual(result.failures.map((v) => v.value.session_id), ['bad']);
  assert.deepEqual(pending, []);
});

test('an obsolete credential response settles nothing', async () => {
  const pending = [item('a')];
  await run(pending, [ok], { responseCurrent: () => false });
  assert.equal(pending.length, 1);
});

test('account changes during refusal parsing settle nothing', async () => {
  const pending = [item('a')];
  let current = true;
  const response = { status: 400, ok: false, json: async () => {
    current = false;
    return { code: 'bad_time', item_index: 0 };
  } };
  await run(pending, [response], { responseCurrent: () => current });
  assert.equal(pending.length, 1);
});

test('a success only settles the captured batch, not a newly closed sitting', async () => {
  const pending = [item('a')];
  await run(pending, [(body) => {
    pending.push(item('new'));
    return ok(body);
  }]);
  assert.deepEqual(pending.map((v) => v.session_id), ['new']);
});

test('network failure keeps payloads identical for the next attempt', async () => {
  const pending = [item('a')];
  let first;
  await assert.rejects(run(pending, [(body) => {
    first = body;
    throw new TypeError('offline');
  }]), /offline/);
  await run(pending, [(body) => {
    assert.equal(body, first);
    return ok(body);
  }]);
  assert.deepEqual(pending, []);
});

test('initial batches stay below the keepalive body budget, including unicode', async () => {
  const pending = Array.from({ length: 250 }, (_, i) => ({
    ...item(String(i)), work_id: '\u00e9'.repeat(250),
  }));
  let requests = 0;
  await run(pending, [], {
    send: async (body) => {
      requests++;
      assert.ok(new TextEncoder().encode(body).length <= 60000);
      return ok(body);
    },
  });
  assert.ok(requests > 1);
  assert.deepEqual(pending, []);
});

test('a malformed success acknowledgement does not discard pending reading', async () => {
  for (const body of [{}, { accepted: 0 }, { accepted: 2 }, { accepted: '1' }]) {
    const pending = [item('a')];
    const deferred = [];
    await run(pending, [reply(200, body)], {
      deferred: (...reason) => deferred.push(reason),
    });
    assert.equal(pending.length, 1);
    assert.deepEqual(deferred, [[200, 'malformed_acknowledgement']]);
  }
});

test('unknown refusals report why reading remains queued', async () => {
  const pending = [item('a')];
  const deferred = [];
  await run(pending, [reply(400, { code: 'unknown_reason' })], {
    deferred: (...reason) => deferred.push(reason),
  });
  assert.deepEqual(deferred, [[400, 'unknown_reason']]);
  assert.equal(pending.length, 1);
});

test('an unhandled status with a JSON refusal code forwards that code', async () => {
  const pending = [item('a')];
  const deferred = [];
  await run(pending, [reply(429, { code: 'rate_limited' })], {
    deferred: (...reason) => deferred.push(reason),
  });
  assert.deepEqual(deferred, [[429, 'rate_limited']]);
  assert.equal(pending.length, 1);
});

test('a flush reports its request budget only when it leaves batches pending', async () => {
  const pending = Array.from({ length: 33 }, (_, i) => ({
    ...item(String(i)), work_id: 'w'.repeat(40000),
  }));
  const deferred = [];
  let requests = 0;
  await run(pending, [], {
    send: async (body) => { requests++; return ok(body); },
    deferred: (...reason) => deferred.push(reason),
  });
  assert.equal(requests, 32);
  assert.equal(pending.length, 1);
  assert.deepEqual(deferred, [[null, 'request_budget']]);
});

test('active duration refusal isolates its item without claiming budget exhaustion', async () => {
  const pending = [item('bad'), item('good')];
  const deferred = [];
  const result = await run(pending, [
    reply(400, { code: 'active_out_of_range', item_index: 0 }), ok,
  ], { deferred: (...reason) => deferred.push(reason) });
  assert.deepEqual(pending, []);
  assert.deepEqual(result.failures.map((v) => v.value.session_id), ['bad']);
  assert.deepEqual(deferred, []);
});
