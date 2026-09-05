const permanentCodes = new Set([
  "id_reused", "missing_field", "bad_time", "time_in_future",
  "progression_out_of_range", "idle_out_of_range", "active_out_of_range",
]);
const encoder = new TextEncoder();
const maxBatch = 1000;
// Leave space below fetch's 64 KiB keepalive body limit.
const maxBodyBytes = 60000;
const maxRequests = 32;

function batches(sessions) {
  const result = [];
  let batch = [], bytes = encoder.encode('{"sessions":[]}').length;
  for (const session of sessions) {
    const size = encoder.encode(JSON.stringify(session)).length + (batch.length ? 1 : 0);
    if (batch.length && (batch.length === maxBatch || bytes + size > maxBodyBytes)) {
      result.push(batch);
      batch = [];
      bytes = encoder.encode('{"sessions":[]}').length;
    }
    batch.push(session);
    bytes += size;
  }
  if (batch.length) result.push(batch);
  return result;
}

function namedIndex(body, batch) {
  if (Number.isInteger(body.item_index) &&
      body.item_index >= 0 && body.item_index < batch.length) return body.item_index;
  if (typeof body.session_id !== "string") return -1;
  return batch.findIndex((session) => session.session_id === body.session_id);
}

// Refusal is atomic: settle only successful batches or a specifically
// permanent bad item. Everything else stays with the caller for retry.
export async function uploadSessions(sessions, {
  send, canSend, responseCurrent, accepted, refused, deferred,
}) {
  const queue = batches(sessions);
  for (let requests = 0; queue.length && requests < maxRequests; requests++) {
    if (!canSend()) return;
    const batch = queue.shift();
    const response = await send(JSON.stringify({ sessions: batch }));
    if (!responseCurrent(response)) return;
    if (!response.ok && ![400, 409, 413].includes(response.status)) {
      deferred(response.status);
      return;
    }
    let body;
    try {
      body = await response.json();
    } catch (err) {
      if (!(err instanceof SyntaxError)) throw err;
    }
    if (!responseCurrent(response)) return;
    body = body && typeof body === "object" ? body : {};
    if (response.ok) {
      if (body.accepted !== batch.length) {
        deferred(response.status, "malformed_acknowledgement");
        return;
      }
      accepted(batch);
      continue;
    }
    if (response.status === 413 || body.code === "batch_too_large") {
      if (batch.length === 1) {
        deferred(response.status, body.code);
        return;
      }
      const limit = Number.isInteger(body.limit) && body.limit > 0 && body.limit < batch.length
        ? body.limit : Math.ceil(batch.length / 2);
      const split = [];
      for (let i = 0; i < batch.length; i += limit) split.push(batch.slice(i, i + limit));
      queue.unshift(...split);
      continue;
    }
    if (!permanentCodes.has(body.code)) {
      deferred(response.status, body.code);
      return;
    }
    const index = namedIndex(body, batch);
    if (index >= 0) {
      refused(batch[index], body.code);
      const rest = batch.filter((_, i) => i !== index);
      if (rest.length) queue.unshift(rest);
    } else if (batch.length === 1) {
      refused(batch[0], body.code);
    } else {
      const middle = Math.ceil(batch.length / 2);
      queue.unshift(batch.slice(0, middle), batch.slice(middle));
    }
  }
  if (queue.length) deferred(null, "request_budget");
}
