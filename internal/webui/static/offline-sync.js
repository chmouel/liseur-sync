import { readerAuth } from "./reader-auth.js";
import { permanentCodes, uploadSessions } from "./reader-session-upload.js";
import {
  assertOfflineContext, attemptOfflineRecord, acknowledgeOfflineAnnotation,
  discardOfflineOutbox, listOfflineOutbox, listReadySnapshots, markOfflineOutbox,
  removeOfflineOutbox, reconcileOfflineBook, replaceOfflineOutboxPayload,
} from "./offline-storage.js";

/**
 * Verdicts that are permanent but that the reader could still answer.
 *
 * `docs/integrating.md` gives both of these a recovery: `unknown_work`
 * is repaired by re-resolving the book and rebuilding the change under
 * the fresh work, and `locator_too_large` by sending the same op again
 * without its locator. The latter is handled below; unknown work still
 * remains visible for the reader to resolve.
 */
const answerableVerdicts = new Set(["unknown_work", "locator_too_large"]);

/**
 * spentVerdicts are the refusals that cost nothing to forget.
 *
 * They are `permanentCodes` minus the two above, kept as one list so
 * the sender and the queue cannot drift apart about what is permanent.
 * `id_reused` is the one that matters, and it is easily misread: it
 * does not say the change was lost. It says the server already holds
 * that id, so what is queued here is a variant of something already
 * accepted — the reading is recorded and this copy is worth nothing.
 * The rest name a payload that is malformed and that no retry and no
 * reader can turn into a delivery.
 */
const spentVerdicts = new Set(
  [...permanentCodes].filter(code => !answerableVerdicts.has(code)),
);

/**
 * reportable decides what the stuck panel names to the reader.
 *
 * An annotation *conflict* is not one of them, because it has a better
 * answer: `resolveStoredAnnotationConflicts` runs first, on every drain,
 * and settles it with both versions in hand. Everything else the server
 * refused for good — including an annotation it called invalid — belongs
 * in the panel, or it becomes exactly the silent permanent resident this
 * whole change exists to abolish.
 */
const reportable = record => record.kind !== "annotation" || record.state !== "conflict";

export async function drainOfflineOutbox(context, request, { keepalive = false } = {}) {
  const records = await listOfflineOutbox({ ...context });
  const blockedWorks = new Set();
  for (const queued of records) {
    if (queued.deviceID !== context.deviceID) continue;
    const positionWork = queued.payload.work_id || queued.bookID;
    if (queued.kind === "position" && blockedWorks.has(positionWork)) continue;
    const record = await attemptOfflineRecord(context, queued.key);
    if (!record) continue;
    const options = { method: "POST", headers: { "Content-Type": "application/json" }, keepalive };
    const done = () => removeOfflineOutbox({ ...context, kind: record.kind, id: record.id });
    // An annotation is the reader's own words, and it is never thrown
    // away on the server's say-so: a refused one waits to be answered.
    // A position and a sitting are records of something that happened,
    // and once the server has refused them for good there is nobody to
    // ask and nothing to decide.
    const spent = code => record.kind !== "annotation" && spentVerdicts.has(code);
    const drop = code => {
      console.warn("A reading change was refused for good and dropped:",
        record.kind, record.id, code);
      return discardOfflineOutbox({ ...context, kind: record.kind, id: record.id });
    };
    const failed = (state, error, details = null) => {
      // Sending the next page before this one is acknowledged lets a later
      // retry of the older page become the server's newest position.
      if (record.kind === "position" && state === "pending") blockedWorks.add(positionWork);
      return markOfflineOutbox({
        ...context, kind: record.kind, id: record.id, state, error, details,
      });
    };
    if (record.kind === "session") {
      await uploadSessions([record.payload], {
        canSend: () => true,
        send: body => request("v1/sessions", { ...options, body }),
        responseCurrent: () => true,
        accepted: done,
        refused: (_, code) => spent(code)
          ? drop(code) : failed("failed", code || "session rejected"),
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
    const response = await request(path, deletion ? { method: "DELETE", keepalive } : {
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
      if (record.kind === "position" && body?.code === "locator_too_large" &&
          record.payload?.locator) {
        const { locator: _dropped, ...bare } = record.payload;
        const replaced = await replaceOfflineOutboxPayload({
          ...context, kind: record.kind, id: record.id, payload: bare,
        });
        if (replaced) continue;
      }
      if (spent(body?.code)) await drop(body.code);
      else if (response.status === 409) await failed("conflict", "revision conflict", body?.server);
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
      // A conflict never says the change landed. It says the id is
      // already spent on a *different* payload, so this change was not
      // stored and sending the same bytes again cannot store it. The
      // protocol's answer is to send it under a fresh id, which this
      // queue does not do, so the reader is told instead of being told
      // nothing.
      await failed("conflict", annotation
        ? (result.reason || "revision conflict")
        : (result.reason || "this position id is already used by a different position"),
      result.server || result);
    } else await failed(result.status === "invalid" ? "failed" : "pending",
      result.reason || "change not acknowledged", result);
  }
}

/**
 * syncCoordinator owns the parts of synchronization that have nothing to
 * do with what is being synchronized: the foreground retry ladder, the
 * cross-tab lock that keeps one writer at a time, and the events worth
 * waking up for.
 *
 * The lock is named after the account's queue rather than after the page
 * holding it, so an ordinary reader tab, a second reader tab and the
 * installed offline app all take turns on the same outbox instead of
 * racing each other into a reordered op log.
 *
 * `run` returns true when work is still owed, which shortens the next
 * delay; throwing is the same as owing work, with the message shown.
 */
export function syncCoordinator({ lock, run, onStatus = () => {}, partition, waiting }) {
  let stopped = false, flight = null, timer = null, retries = 0, again = false;
  const trigger = (reset = true) => {
    if (stopped) return;
    if (reset) retries = 0;
    if (flight) { again = true; return flight; }
    clearTimeout(timer);
    // A timer-driven retry stays out of a background tab, but something
    // that actually happened is delivered whether or not the tab is on
    // screen: hiding it is exactly when a sitting ends.
    if (globalThis.navigator?.onLine === false) return;
    if (!reset && globalThis.document?.hidden) return;
    const locked = () => navigator.locks ? navigator.locks.request(lock, run) : run();
    let owed = false;
    flight = Promise.resolve().then(locked).catch(error => {
      onStatus(error.message || waiting);
      return true;
    }).then(pending => { owed = !!pending; }).finally(() => {
      flight = null;
      if (stopped) return;
      // Something arrived while this pass was running. Go again straight
      // away rather than through the retry timer: the change may have
      // been a sitting ending as the tab was hidden, which the timer
      // declines to chase.
      if (again) { again = false; trigger(); return; }
      // Keep checking in the foreground, including after the network comes
      // back without an online event and while another device is reading.
      const delay = owed ? [1000, 3000, 10000, 30000][Math.min(retries++, 3)] : 30000;
      timer = setTimeout(() => trigger(false), delay);
    });
    return flight;
  };
  const changed = event => {
    if (event.detail?.partition && event.detail.partition !== partition) return;
    trigger();
  };
  const channel = globalThis.BroadcastChannel ? new BroadcastChannel("liseur-offline") : null;
  if (channel) channel.onmessage = event => changed({ detail: event.data });
  globalThis.addEventListener?.("online", changed);
  globalThis.addEventListener?.("offline-change", changed);
  globalThis.document?.addEventListener("visibilitychange", changed);
  return {
    trigger,
    busy: () => !!flight,
    stopped: () => stopped,
    stop() {
      stopped = true;
      clearTimeout(timer);
      channel?.close();
      globalThis.removeEventListener?.("online", changed);
      globalThis.removeEventListener?.("offline-change", changed);
      globalThis.document?.removeEventListener("visibilitychange", changed);
    },
  };
}

// One lock per account queue, shared by every page that can drain it.
// Exported so a page that edits the queue rather than draining it still
// takes its turn instead of racing a send already in flight.
export const outboxLock = context => "offline-sync:" + context.partition + context.account;

export function offlineSync({ context, base, onChange = () => {}, onStatus = () => {},
  onStuck = () => {} }) {
  let csrf = "";
  const auth = readerAuth({ apiBase: base, tokenURL: base + "ui/reader/token",
    csrf: () => csrf, onExhausted: () => {} });
  const authorize = async identity => {
    if (coordinator.stopped()) throw new Error("Offline synchronization stopped.");
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
    // A stopped coordinator may still be waiting for another tab's lock.
    if (coordinator.stopped() || globalThis.document?.hidden ||
        globalThis.navigator?.onLine === false) return false;
    const response = await fetch(base + "ui/offline/account", {
      cache: "no-store", credentials: "same-origin", signal: AbortSignal.timeout(30000),
    });
    if (!response.ok || response.redirected)
      throw new Error("Sign in again to sync offline changes.");
    const account = await response.json();
    if (coordinator.stopped()) return false;
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
    const waiting = pending.filter(record => record.state === "pending");
    // The installed app answers for the whole account, not for one open
    // book, so nothing is filtered by book here.
    await onStuck(pending.filter(record => record.state !== "pending" &&
      reportable(record) && record.deviceID === context.deviceID));
    onStatus(waiting.length ? `${waiting.length} offline change(s) waiting to sync.` : "");
    return waiting.some(record => record.deviceID === context.deviceID);
  };
  const coordinator = syncCoordinator({
    lock: outboxLock(context), run, onStatus, partition: context.partition,
    waiting: "Offline changes are waiting to sync.",
  });
  return { trigger: coordinator.trigger, stop() { coordinator.stop(); auth.stop(); } };
}

/**
 * readingSync drains the same queue for an online reader.
 *
 * The reader writes every position and finished sitting to the outbox
 * before anything is sent, and this is the only thing that sends them.
 * A page turn that was never acknowledged is therefore still on disk
 * after a crash, a reload or a closed tab, and goes out under the next
 * credential rather than being lost — which is the whole difference
 * between this and posting straight from the page.
 *
 * `request` is the reader's own authenticated transport, so a token
 * renewal mid-drain is the auth layer's problem and not a second
 * credential racing the first.
 *
 * `onStuck` receives the records this book still owes that the server
 * refused in a way no retry resolves by itself. It is given the records
 * rather than a sentence, because the page has to offer something to do
 * about them: a queue that can only complain is a queue nobody can
 * empty.
 */
export function readingSync({
  context, request, bookID = "", onChange = () => {}, onStatus = () => {},
  onStuck = () => {},
}) {
  const mine = record => record.deviceID === context.deviceID;
  const run = async () => {
    // No hidden-tab check here: a sitting ends when the tab is hidden,
    // and that is precisely the change worth delivering. The coordinator
    // is what keeps a background tab from retrying on a timer.
    if (coordinator.stopped() || globalThis.navigator?.onLine === false) return false;
    await assertOfflineContext(context);
    await drainOfflineOutbox(context, request);
    if (coordinator.stopped()) return false;
    await onChange();
    const pending = await listOfflineOutbox({ ...context, state: null });
    // Only this book's stuck changes are the reader's business here.
    // A change belonging to a book they are not reading is not
    // something to answer on top of the page they are.
    await onStuck(pending.filter(record => mine(record) &&
      record.state !== "pending" && reportable(record) &&
      (!bookID || record.bookID === bookID)));
    return pending.some(record => record.state === "pending" && mine(record));
  };
  const coordinator = syncCoordinator({
    lock: outboxLock(context), run, onStatus, partition: context.partition,
    waiting: "Reading changes are waiting to sync.",
  });
  return coordinator;
}
