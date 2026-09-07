import { readerAuth } from "./reader-auth.js";
import { uploadSessions } from "./reader-session-upload.js";
import {
  assertOfflineContext, attemptOfflineRecord, acknowledgeOfflineAnnotation,
  listOfflineOutbox, listReadySnapshots, markOfflineOutbox, removeOfflineOutbox,
  reconcileOfflineBook,
} from "./offline-storage.js";

export async function drainOfflineOutbox(context, request) {
  const records = await listOfflineOutbox({ ...context });
  for (const queued of records) {
    if (queued.deviceID !== context.deviceID) continue;
    const record = await attemptOfflineRecord(context, queued.key);
    if (!record) continue;
    const options = { method: "POST", headers: { "Content-Type": "application/json" } };
    const done = () => removeOfflineOutbox({ ...context, kind: record.kind, id: record.id });
    const failed = (state, error, details = null) => markOfflineOutbox({
      ...context, kind: record.kind, id: record.id, state, error, details,
    });
    if (record.kind === "session") {
      await uploadSessions([record.payload], {
        canSend: () => true,
        send: body => request("v1/sessions", { ...options, body }),
        responseCurrent: () => true,
        accepted: done,
        refused: (_, code) => failed("failed", code || "session rejected"),
        deferred: (status, code) => failed(
          "pending", code || `HTTP ${status || "unknown"}`,
        ),
      });
      continue;
    }
    const annotation = record.kind === "annotation";
    const deletion = annotation && record.payload.method === "delete";
    const path = deletion
      ? "v1/annotations/" + encodeURIComponent(record.annotationID) + "?rev=" + record.payload.rev
      : annotation ? "v1/annotations" : "v1/ops";
    const response = await request(path, deletion ? { method: "DELETE" } : {
      ...options, body: JSON.stringify(annotation
        ? { annotations: [record.payload.annotation] } : { ops: [record.payload] }),
    });
    const body = await response.json().catch(() => null);
    await assertOfflineContext(context);
    if (deletion && (response.ok || response.status === 404)) {
      await acknowledgeOfflineAnnotation(context, record, {
        id: record.annotationID, deleted: true, rev: body?.rev || record.payload.rev + 1, seq: body?.seq || 0,
      });
      continue;
    }
    if (!response.ok) {
      if (response.status === 409) await failed("conflict", "revision conflict", body?.server);
      else if ([400, 403, 404, 413, 422].includes(response.status))
        await failed("failed", body?.error || `HTTP ${response.status}`);
      else await failed("pending", body?.error || `HTTP ${response.status}`);
      continue;
    }
    const result = body?.results?.find(value => (annotation ? value.id : value.op_id) ===
      (annotation ? record.annotationID : record.id));
    if (!result) {
      await failed("pending", "missing acknowledgement");
      continue;
    }
    if (["applied", "duplicate"].includes(result.status)) {
      if (annotation) {
        const server = { ...record.payload.annotation, rev: result.rev, seq: result.seq };
        delete server.base_rev;
        await acknowledgeOfflineAnnotation(context, record, server);
      } else await done();
    } else if (result.status === "conflict") {
      await failed("conflict", result.reason || "revision conflict", result.server || result);
    } else await failed("failed", result.reason || "change rejected", result);
  }
}

export function offlineSync({ context, base, onChange = () => {}, onStatus = () => {} }) {
  let csrf = "", stopped = false, flight = null, timer = null, retries = 0, again = false;
  const auth = readerAuth({ apiBase: base, tokenURL: base + "ui/reader/token",
    csrf: () => csrf, onExhausted: () => {} });
  const authorize = async identity => {
    await assertOfflineContext(context);
    if (identity.account !== context.account || identity.device !== context.deviceID)
      throw new Error("Sign in on the original account and device to sync offline changes.");
  };
  const request = async (path, options) => {
    const response = await auth.request(path, { ...options, authorize });
    await authorize(auth.responseIdentity(response));
    if (!auth.responseCurrent(response)) throw new Error("The reading credential changed.");
    return response;
  };
  const run = async () => {
    const response = await fetch(base + "ui/offline/account", {
      cache: "no-store", credentials: "same-origin", signal: AbortSignal.timeout(30000),
    });
    if (!response.ok || response.redirected)
      throw new Error("Sign in again to sync offline changes.");
    const account = await response.json();
    if (account.account !== context.account) throw new Error("Sign in as the account that saved these books.");
    if (typeof account.csrf !== "string" || !account.csrf)
      throw new Error("Sign in again to sync offline changes.");
    csrf = account.csrf;
    auth.resume();
    await authorize(await auth.acquire());
    await drainOfflineOutbox(context, request);
    for (const book of await listReadySnapshots(context.partition, context.account)) {
      if (book.deviceID === context.deviceID)
        await reconcileOfflineBook(context, book, request);
    }
    await onChange();
    const pending = await listOfflineOutbox({ ...context, state: null });
    onStatus(pending.length ? `${pending.length} offline change(s) waiting; retry sync or review conflicts.` : "");
    return pending.some(record => record.state === "pending" && record.deviceID === context.deviceID);
  };
  const trigger = (reset = true) => {
    if (stopped) return;
    if (reset) retries = 0;
    if (flight) { again = true; return flight; }
    clearTimeout(timer);
    if (globalThis.document?.hidden || globalThis.navigator?.onLine === false) return;
    const locked = () => navigator.locks
      ? navigator.locks.request("offline-sync:" + context.partition + context.account, run) : run();
    flight = Promise.resolve().then(locked).catch(error => {
      onStatus(error.message || "Offline changes are waiting to sync.");
      return true;
    }).then(pending => {
      if ((pending || again) && !stopped && retries < 4) {
        again = false;
        timer = setTimeout(() => trigger(false), [1000, 3000, 10000, 30000][retries++]);
      }
    }).finally(() => { flight = null; });
    return flight;
  };
  const changed = event => {
    if (event.detail?.partition && event.detail.partition !== context.partition) return;
    trigger();
  };
  const channel = globalThis.BroadcastChannel ? new BroadcastChannel("liseur-offline") : null;
  if (channel) channel.onmessage = event => changed({ detail: event.data });
  globalThis.addEventListener?.("online", changed);
  globalThis.addEventListener?.("offline-change", changed);
  globalThis.document?.addEventListener("visibilitychange", changed);
  return { trigger, stop() {
    stopped = true;
    clearTimeout(timer);
    auth.stop();
    channel?.close();
    globalThis.removeEventListener?.("online", changed);
    globalThis.removeEventListener?.("offline-change", changed);
    globalThis.document?.removeEventListener("visibilitychange", changed);
  } };
}
