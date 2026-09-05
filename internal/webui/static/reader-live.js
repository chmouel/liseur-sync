// Live events are hints. Neither this parser nor the reconnect loop keeps a cursor.
export const MAX_FRAME_BYTES = 64 * 1024;

export function eventParser(onTopics, limit = MAX_FRAME_BYTES) {
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let line = "", event = "", data = [], bytes = 0, cr = false;
  const finishLine = () => {
    if (!line) {
      if (event === "invalidate" && data.length) {
        const value = JSON.parse(data.join("\n"));
        if (!Array.isArray(value?.topics)) throw new Error("invalid live topics");
        const topics = [...new Set(value.topics.filter(
          (t) => t === "positions" || t === "annotations",
        ))];
        if (topics.length) onTopics(topics);
      }
      event = ""; data = []; bytes = 0;
    } else if (!line.startsWith(":")) {
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      if (field === "data") data.push(value);
    }
    line = "";
  };
  return {
    feed(chunk) {
      // Inspect bytes before decoding: even an unterminated line is bounded.
      for (const byte of chunk) {
        if (cr && byte === 10) {
          cr = false;
          if (bytes && ++bytes > limit) throw new Error("live frame too large");
          continue;
        }
        cr = false;
        if (++bytes > limit) throw new Error("live frame too large");
        if (byte === 10 || byte === 13) {
          line += decoder.decode();
          finishLine();
          cr = byte === 13;
        } else {
          line += decoder.decode(Uint8Array.of(byte), { stream: true });
        }
      }
    },
    end() {
      decoder.decode(); // Refuse a truncated UTF-8 code point.
      // SSE requires a blank line; EOF does not dispatch a partial frame.
    },
  };
}

export function retryDelay(attempt, retryAfter, now = Date.now(), random = Math.random) {
  const cap = Math.min(60000, 1000 * 2 ** Math.min(attempt, 6));
  const jitter = cap * (0.5 + random() * 0.5);
  if (!retryAfter) return jitter;
  const seconds = /^\d+$/.test(retryAfter.trim()) ? Number(retryAfter) : NaN;
  const wait = Number.isFinite(seconds) ? seconds * 1000 : Date.parse(retryAfter) - now;
  return Math.max(jitter, Number.isFinite(wait) ? Math.max(0, wait) : 0);
}

export function liveStream({
  request, current, onTopics, onError = () => {}, now = Date.now,
  random = Math.random, setTimer = setTimeout, clearTimer = clearTimeout,
  idleMS = 60000,
}) {
  let active = false, controller = null, timer = null, generation = 0, attempts = 0;
  const connect = async (run) => {
    const abort = new AbortController();
    controller = abort;
    let watchdog = null, reader = null, retryAfter = null;
    const valid = () => active && generation === run && !abort.signal.aborted;
    const touch = () => {
      clearTimer(watchdog);
      watchdog = setTimer(() => abort.abort(), idleMS);
    };
    const began = now();
    let supported = true, healthy = false;
    try {
      touch(); // Also bounds credential acquisition / waiting for response headers.
      const resp = await request("v1/events", {
        signal: abort.signal,
        headers: { Accept: "text/event-stream" },
        cache: "no-store",
      });
      if (!valid()) { await resp.body?.cancel(); return; }
      retryAfter = resp.headers.get("Retry-After");
      if ([401, 403, 404, 501].includes(resp.status)) {
        supported = false; // Probe again only at the next visibility boundary.
        await resp.body?.cancel();
        return;
      }
      if (!resp.ok || !resp.headers.get("Content-Type")?.toLowerCase().startsWith("text/event-stream")) {
        await resp.body?.cancel();
        throw new Error("live stream unavailable");
      }
      const parser = eventParser((topics) => {
        if (valid() && current(resp)) onTopics(topics);
      });
      reader = resp.body.getReader();
      for (;;) {
        const { value, done } = await reader.read();
        if (!valid() || !current(resp)) break;
        if (done) { parser.end(); break; }
        if (value.length) {
          touch();
          parser.feed(value);
          if (now() - began >= idleMS) healthy = true;
        }
      }
    } catch (err) {
      if (active && generation === run) onError(err);
      if (err?.terminal) supported = false;
    } finally {
      clearTimer(watchdog);
      abort.abort();
      if (reader) {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      if (controller === abort) controller = null;
      if (active && generation === run && supported) {
        // A quick 200 followed by EOF is still a failure, not a recovery.
        if (healthy) attempts = 0;
        const delay = retryDelay(attempts++, retryAfter, now(), random);
        // Long Retry-After values must not overflow the platform's timer.
        const deadline = now() + delay;
        const wait = () => {
          if (!active || generation !== run) return;
          const left = deadline - now();
          if (left > 0) timer = setTimer(wait, Math.min(left, 2147483647));
          else { timer = null; void connect(run); }
        };
        wait();
      }
    }
  };
  return {
    start() {
      if (active) return;
      active = true;
      attempts = 0;
      void connect(++generation);
    },
    stop() {
      active = false;
      generation++;
      clearTimer(timer);
      timer = null;
      controller?.abort();
    },
  };
}
