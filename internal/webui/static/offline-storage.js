import { decodeText, publicationHref, stripPublicationCode } from "./reader-publication.js";

export const OFFLINE_DB_NAME = "liseur-sync-offline";
export const OFFLINE_DB_VERSION = 3;
const SNAPSHOTS = "snapshots";
const RESOURCES = "resources";
const ACCOUNTS = "accounts";
const OUTBOX = "outbox";
const CHECKPOINTS = "checkpoints";
const ANNOTATIONS = "annotations";
const separator = "\u001f";

export class OfflineStorageError extends Error {
  constructor(message, code = "storage") {
    super(message);
    this.name = "OfflineStorageError";
    this.code = code;
  }
}

export class OfflineQuotaError extends OfflineStorageError {
  constructor() {
    super("The device does not have enough storage for this book.", "quota");
    this.name = "OfflineQuotaError";
  }
}

export function deploymentPrefix(here = globalThis.location) {
  const path = new URL(here.href || here, here.href || undefined).pathname;
  const marker = path.indexOf("/ui/");
  return marker < 0 ? "/" : path.slice(0, marker + 1);
}

export function storagePartition(here = globalThis.location) {
  const url = new URL(deploymentPrefix(here), new URL(here.href || here));
  return url.origin + url.pathname;
}

export function snapshotKey({ partition, account, bookID, digest, generation }) {
  return [partition, account, bookID, digest, generation].join(separator);
}

function requestResult(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new OfflineStorageError("IndexedDB request failed."));
  });
}

function transactionDone(transaction) {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error || new OfflineStorageError("IndexedDB transaction failed."));
    transaction.onabort = () => reject(transaction.error || new OfflineStorageError("IndexedDB transaction was aborted."));
  });
}

function transactionWork(db, stores, mode, work) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(stores, mode);
    let result;
    let failure = null;
    tx.oncomplete = () => resolve(result);
    tx.onerror = () => {
      if (!failure) failure = tx.error || new OfflineStorageError("IndexedDB transaction failed.");
    };
    tx.onabort = () => reject(failure || tx.error ||
      new OfflineStorageError("IndexedDB transaction was aborted."));
    const fail = error => {
      failure = error;
      tx.abort();
    };
    try {
      work(tx, value => { result = value; }, fail);
    } catch (error) {
      fail(error);
    }
  });
}

export function openOfflineDB() {
  if (!globalThis.indexedDB) {
    return Promise.reject(new OfflineStorageError("Offline storage is unavailable in this browser.", "unsupported"));
  }
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(OFFLINE_DB_NAME, OFFLINE_DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(SNAPSHOTS)) {
        const snapshots = db.createObjectStore(SNAPSHOTS, { keyPath: "key" });
        snapshots.createIndex("ready", ["partition", "account", "state"]);
        snapshots.createIndex("book", ["partition", "account", "bookID"]);
      }
      if (!db.objectStoreNames.contains(RESOURCES))
        db.createObjectStore(RESOURCES, { keyPath: ["snapshotKey", "href"] });
      if (!db.objectStoreNames.contains(ACCOUNTS))
        db.createObjectStore(ACCOUNTS, { keyPath: "key" });
      if (!db.objectStoreNames.contains(OUTBOX)) {
        const outbox = db.createObjectStore(OUTBOX, { keyPath: "key" });
        outbox.createIndex("account", ["partition", "account", "state", "createdAt"]);
        outbox.createIndex("kind", ["partition", "account", "kind", "state", "createdAt"]);
      }
      if (!db.objectStoreNames.contains(CHECKPOINTS))
        db.createObjectStore(CHECKPOINTS, { keyPath: "key" });
      if (!db.objectStoreNames.contains(ANNOTATIONS)) {
        const annotations = db.createObjectStore(ANNOTATIONS, { keyPath: "key" });
        annotations.createIndex("book", ["partition", "account", "bookID"]);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new OfflineStorageError("Could not open offline storage."));
  });
}

function quotaError(error) {
  return error?.name === "QuotaExceededError" || error?.code === 22;
}

async function withDB(fn) {
  const db = await openOfflineDB();
  try {
    return await fn(db);
  } finally {
    db.close();
  }
}

function randomGeneration() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function copyBytes(bytes) {
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
}

function keyForAccount(partition) {
  return partition;
}

function keyForOutbox({ partition, account, kind, id }) {
  return [partition, account, kind, id].join(separator);
}

function keyForCheckpoint({ partition, account, bookID }) {
  return [partition, account, bookID].join(separator);
}

function keyForAnnotation({ partition, account, bookID, id }) {
  return [partition, account, bookID, id].join(separator);
}

export async function accountVersion(partition) {
  return withDB(async db => {
    const marker = await requestResult(db.transaction(ACCOUNTS).objectStore(ACCOUNTS).get(partition));
    return marker?.epoch || null;
  });
}

export async function setActiveAccount(partition, account, expectedEpoch) {
  const epoch = await withDB(db => transactionWork(db, ACCOUNTS, "readwrite", (tx, set, fail) => {
    const store = tx.objectStore(ACCOUNTS);
    const request = store.get(keyForAccount(partition));
    request.onsuccess = () => {
      const old = request.result;
      if (expectedEpoch !== undefined && (old?.epoch || null) !== expectedEpoch) {
        fail(new OfflineStorageError("The offline account changed while signing in.", "auth"));
        return;
      }
      const record = { key: partition, partition, account,
        epoch: old?.account === account && old.epoch ? old.epoch : randomGeneration() };
      store.put(record);
      set(record.epoch);
    };
  }));
  notifyOfflineChange("account", partition);
  return epoch;
}

export async function clearActiveAccount(partition) {
  return withDB(async db => {
    const tx = db.transaction(ACCOUNTS, "readwrite");
    tx.objectStore(ACCOUNTS).put({ key: partition, partition, account: null, epoch: randomGeneration() });
    await transactionDone(tx);
    notifyOfflineChange("logout", partition);
  });
}
export async function activeAccount(partition) {
  return withDB(async db => {
    const result = await requestResult(db.transaction(ACCOUNTS).objectStore(ACCOUNTS).get(keyForAccount(partition)));
    return result?.account || null;
  });
}

export async function accountContext(partition, account) {
  return withDB(async db => {
    const record = await requestResult(db.transaction(ACCOUNTS).objectStore(ACCOUNTS).get(partition));
    if (!record?.epoch || record.account !== account)
      throw new OfflineStorageError("This offline account is no longer active.", "auth");
    return { partition, account, epoch: record.epoch };
  });
}

function guardedWork(db, stores, context, work) {
  return transactionWork(db, [...new Set([ACCOUNTS, ...[].concat(stores)])], "readwrite",
    (tx, set, fail) => {
      const request = tx.objectStore(ACCOUNTS).get(context.partition);
      request.onsuccess = () => {
        if (!context.epoch || request.result?.epoch !== context.epoch ||
            request.result?.account !== context.account) {
          fail(new OfflineStorageError("This offline account is no longer active.", "auth"));
          return;
        }
        try { work(tx, set, fail); } catch (error) { fail(error); }
      };
    });
}

export async function assertOfflineContext(context) {
  return withDB(db => guardedWork(db, [], context, () => {}));
}

export function claimOfflineReader(context, bookID) {
  if (!globalThis.navigator?.locks)
    return Promise.reject(new OfflineStorageError("This browser cannot safely claim an offline reading session."));
  return new Promise((resolve, reject) => {
    navigator.locks.request("offline-reader:" + keyForCheckpoint({ ...context, bookID }),
      { ifAvailable: true }, lock => {
        if (!lock) {
          reject(new OfflineStorageError("This book is already open in another tab."));
          return;
        }
        return new Promise(release => resolve(release));
      }).catch(reject);
  });
}

export function notifyOfflineChange(type = "write", partition = storagePartition()) {
  globalThis.dispatchEvent?.(new CustomEvent("offline-change", { detail: { type, partition } }));
  if (globalThis.BroadcastChannel) {
    const channel = new BroadcastChannel("liseur-offline");
    channel.postMessage({ type, partition });
    channel.close();
  }
}

export async function saveOfflinePosition({
  partition = storagePartition(), account, epoch, deviceID, bookID, op,
} = {}) {
  if (!partition || !account || !bookID || !op?.op_id)
    throw new OfflineStorageError("An offline position needs an account, book and operation.");
  return withDB(async db => {
    try {
      await guardedWork(db, [SNAPSHOTS, OUTBOX], { partition, account, epoch }, (tx, _, fail) => {
        const snapshots = tx.objectStore(SNAPSHOTS);
        const request = snapshots.index("book").getAll([partition, account, bookID]);
        request.onsuccess = () => {
          const current = request.result
            .filter(value => value.state === "ready")
            .sort((a, b) => (b.readyAt || 0) - (a.readyAt || 0))[0];
          if (!current) {
            fail(new OfflineStorageError("This book is not available offline.", "missing"));
            return;
          }
          current.localPosition = op;
          snapshots.put(current);
          tx.objectStore(OUTBOX).put({
            key: keyForOutbox({ partition, account, kind: "position", id: op.op_id }),
            partition, account, epoch, deviceID: deviceID || current.deviceID, bookID, kind: "position", id: op.op_id,
            payload: op, state: "pending", createdAt: Date.now(), attempts: 0,
          });
        };
      });
    } catch (error) {
      if (quotaError(error)) throw new OfflineQuotaError();
      throw error;
    }
  });
}

export async function saveOfflineSessionCheckpoint({
  partition = storagePartition(), account, epoch, bookID, checkpoint,
} = {}) {
  if (!partition || !account || !bookID || !checkpoint?.id)
    throw new OfflineStorageError("An offline session checkpoint is incomplete.");
  return withDB(async db => {
    await guardedWork(db, [CHECKPOINTS, OUTBOX], { partition, account, epoch }, tx => {
      const finalized = tx.objectStore(OUTBOX).get(
        keyForOutbox({ partition, account, kind: "session", id: checkpoint.id }));
      finalized.onsuccess = () => {
        if (!finalized.result) tx.objectStore(CHECKPOINTS).put({
          key: keyForCheckpoint({ partition, account, bookID }),
          partition, account, bookID, checkpoint, updatedAt: Date.now(),
        });
      };
    });
  });
}

export async function getOfflineSessionCheckpoint({
  partition = storagePartition(), account, bookID,
} = {}) {
  if (!partition || !account || !bookID) return null;
  return withDB(async db => {
    const result = await requestResult(db.transaction(CHECKPOINTS)
      .objectStore(CHECKPOINTS)
      .get(keyForCheckpoint({ partition, account, bookID })));
    return result?.checkpoint || null;
  });
}

export async function clearOfflineSessionCheckpoint({
  partition = storagePartition(), account, epoch, bookID, sessionID,
} = {}) {
  if (!partition || !account || !bookID) return;
  return withDB(async db => {
    await guardedWork(db, CHECKPOINTS, { partition, account, epoch }, tx => {
      const store = tx.objectStore(CHECKPOINTS);
      const key = keyForCheckpoint({ partition, account, bookID });
      const request = store.get(key);
      request.onsuccess = () => {
        if (request.result?.checkpoint.id === sessionID) store.delete(key);
      };
    });
  });
}

export async function finishOfflineSession({
  partition = storagePartition(), account, epoch, deviceID, bookID, payload,
} = {}) {
  if (!partition || !account || !bookID || !payload?.session_id)
    throw new OfflineStorageError("An offline session payload is incomplete.");
  const fingerprint = await sha256(new TextEncoder().encode(JSON.stringify(payload)));
  if (!fingerprint) throw new OfflineStorageError("Session identity hashing is unavailable.");
  return withDB(async db => {
    try {
      await guardedWork(db, [OUTBOX, CHECKPOINTS], { partition, account, epoch }, (tx, _, fail) => {
        const outbox = tx.objectStore(OUTBOX);
        const key = keyForOutbox({ partition, account, kind: "session", id: payload.session_id });
        const request = outbox.get(key);
        request.onsuccess = () => {
          if (request.result && (request.result.fingerprint
            ? request.result.fingerprint !== fingerprint
            : JSON.stringify(request.result.payload) !== JSON.stringify(payload))) {
            fail(new OfflineStorageError("A finalized session cannot be changed.", "conflict"));
            return;
          }
          if (!request.result) outbox.put({
            key, partition, account, epoch, deviceID, bookID, kind: "session", id: payload.session_id,
            payload, fingerprint, state: "pending", createdAt: Date.now(), attempts: 0,
          });
          const checkpoints = tx.objectStore(CHECKPOINTS);
          const checkpointKey = keyForCheckpoint({ partition, account, bookID });
          const checkpoint = checkpoints.get(checkpointKey);
          checkpoint.onsuccess = () => {
            if (checkpoint.result?.checkpoint.id === payload.session_id) checkpoints.delete(checkpointKey);
          };
        };
      });
    } catch (error) {
      if (quotaError(error)) throw new OfflineQuotaError();
      throw error;
    }
  });
}

export async function listOfflineOutbox({
  partition = storagePartition(), account, kind = "", state = "pending",
} = {}) {
  if (!partition || !account) return [];
  return withDB(async db => {
    const store = db.transaction(OUTBOX).objectStore(OUTBOX);
    const index = kind ? store.index("kind") : store.index("account");
    const allStates = state === null;
    const key = kind
      ? [partition, account, kind, allStates ? "" : state, 0]
      : [partition, account, allStates ? "" : state, 0];
    const upper = kind
      ? [partition, account, kind, allStates ? "\uffff" : state, Number.MAX_SAFE_INTEGER]
      : [partition, account, allStates ? "\uffff" : state, Number.MAX_SAFE_INTEGER];
    const records = await requestResult(index.getAll(IDBKeyRange.bound(key, upper)));
    return records.filter(record => record.state !== "delivered")
      .sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0));
  });
}

export async function markOfflineOutbox({
  partition = storagePartition(), account, epoch, kind, id, state = "pending", error = "", details = null,
} = {}) {
  if (!partition || !account || !kind || !id) return;
  return withDB(async db => {
    await guardedWork(db, OUTBOX, { partition, account, epoch }, tx => {
      const store = tx.objectStore(OUTBOX);
      const request = store.get(keyForOutbox({ partition, account, kind, id }));
      request.onsuccess = () => {
        const record = request.result;
        if (!record) return;
        record.state = state;
        record.error = error;
        record.details = details;
        record.attempts = (record.attempts || 0) + 1;
        record.updatedAt = Date.now();
        store.put(record);
      };
    });
  });
}

export async function listOfflineAnnotations({
  partition = storagePartition(), account, bookID,
} = {}) {
  if (!partition || !account || !bookID) return [];
  return withDB(async db => {
    const records = await requestResult(db.transaction(ANNOTATIONS)
      .objectStore(ANNOTATIONS)
      .index("book")
      .getAll([partition, account, bookID]));
    return records
      .map(record => record.annotation)
      .sort((a, b) => (a.progression || 0) - (b.progression || 0));
  });
}

export async function queueOfflineAnnotation({
  partition = storagePartition(), account, epoch, deviceID, bookID, annotation, payload, queueID = randomGeneration(),
} = {}) {
  if (!partition || !account || !bookID || !annotation?.id || !payload)
    throw new OfflineStorageError("An offline annotation is incomplete.");
  return withDB(async db => {
    if (payload.method === "write" && annotation.kind === "note" && !annotation.body?.trim())
      throw new OfflineStorageError("A note requires a body.", "invalid");
    await guardedWork(db, [ANNOTATIONS, OUTBOX], { partition, account, epoch }, tx => {
      const outbox = tx.objectStore(OUTBOX);
      let predecessor = false;
      const cursorRequest = outbox.index("account").openCursor(IDBKeyRange.bound(
        [partition, account, "", 0],
        [partition, account, "\uffff", Number.MAX_SAFE_INTEGER],
      ));
      cursorRequest.onerror = () => tx.abort();
      cursorRequest.onsuccess = () => {
        const cursor = cursorRequest.result;
        if (cursor) {
          const record = cursor.value;
          if (record.bookID === bookID && record.kind === "annotation" &&
              record.annotationID === annotation.id) {
            if (record.attempted && record.state === "pending") predecessor = true;
            else cursor.delete();
          }
          cursor.continue();
          return;
        }
        tx.objectStore(ANNOTATIONS).put({
          key: keyForAnnotation({ partition, account, bookID, id: annotation.id }),
          partition, account, bookID, annotation, intent: queueID, updatedAt: Date.now(),
        });
        if (payload.method === "delete" && !payload.rev && !predecessor) return;
        outbox.put({
          key: keyForOutbox({ partition, account, kind: "annotation", id: queueID }),
          partition, account, epoch, deviceID, bookID, kind: "annotation", id: queueID,
          annotationID: annotation.id, payload, state: "pending",
          createdAt: Date.now(), attempts: 0,
        });
      };
    });
  });
}

export async function updateOfflineAnnotation({
  partition = storagePartition(), account, epoch, bookID, annotation,
} = {}) {
  if (!partition || !account || !bookID || !annotation?.id) return;
  return withDB(async db => {
    await guardedWork(db, ANNOTATIONS, { partition, account, epoch }, tx => tx.objectStore(ANNOTATIONS).put({
      key: keyForAnnotation({ partition, account, bookID, id: annotation.id }),
      partition, account, bookID, annotation, updatedAt: Date.now(),
    }));
  });
}

export async function removeOfflineAnnotation({
  partition = storagePartition(), account, epoch, bookID, id, removeAnnotation = true,
} = {}) {
  if (!partition || !account || !bookID || !id) return;
  return withDB(async db => {
    await guardedWork(db, [ANNOTATIONS, OUTBOX], { partition, account, epoch }, tx => {
      if (removeAnnotation)
        tx.objectStore(ANNOTATIONS).delete(keyForAnnotation({ partition, account, bookID, id }));
      const outbox = tx.objectStore(OUTBOX);
      const cursorRequest = outbox.index("account").openCursor(IDBKeyRange.bound(
        [partition, account, "", 0],
        [partition, account, "\uffff", Number.MAX_SAFE_INTEGER],
      ));
      cursorRequest.onerror = () => tx.abort();
      cursorRequest.onsuccess = () => {
        const cursor = cursorRequest.result;
        if (cursor) {
          const record = cursor.value;
          if (record.bookID === bookID && record.kind === "annotation" &&
              record.annotationID === id) cursor.delete();
          cursor.continue();
        }
      };
    });
  });
}

export async function removeOfflineOutbox({
  partition = storagePartition(), account, epoch, kind, id,
} = {}) {
  if (!partition || !account || !kind || !id) return;
  return withDB(async db => {
    await guardedWork(db, OUTBOX, { partition, account, epoch }, tx => {
      const store = tx.objectStore(OUTBOX);
      const key = keyForOutbox({ partition, account, kind, id });
      if (kind !== "session") { store.delete(key); return; }
      const request = store.get(key);
      request.onsuccess = () => {
        if (request.result) {
          const delivered = { ...request.result, state: "delivered" };
          if (delivered.fingerprint) delete delivered.payload;
          store.put(delivered);
        }
      };
    });
  });
}

// Mark before transport: an uncertain request is immutable until acknowledged.
export async function attemptOfflineRecord(context, key) {
  return withDB(db => guardedWork(db, OUTBOX, context, (tx, set) => {
    const store = tx.objectStore(OUTBOX);
    const request = store.getAll();
    request.onsuccess = () => {
      const record = request.result.find(value => value.key === key);
      if (!record || record.state !== "pending" || record.epoch !== context.epoch) return;
      if (record.kind === "annotation" && request.result.some(value =>
        value.key !== key && value.partition === record.partition && value.account === record.account &&
        value.annotationID === record.annotationID && value.attempted)) return;
      record.attempted = true;
      store.put(record);
      set(record);
    };
  }));
}

export async function acknowledgeOfflineAnnotation(context, record, server) {
  return withDB(db => guardedWork(db, [ANNOTATIONS, OUTBOX], context, tx => {
    const outbox = tx.objectStore(OUTBOX);
    const annotations = tx.objectStore(ANNOTATIONS);
    const request = outbox.get(record.key);
    request.onsuccess = () => {
      if (!request.result) return;
      outbox.delete(record.key);
      const localRequest = annotations.get(keyForAnnotation({
        ...context, bookID: record.bookID, id: record.annotationID,
      }));
      localRequest.onsuccess = () => {
        const local = localRequest.result;
        if (!local) return;
        if (local.intent === record.id) local.annotation = { ...server, pending: false };
        else {
          local.annotation.rev = server.rev;
          local.annotation.seq = server.seq;
          const nextRequest = outbox.getAll();
          nextRequest.onsuccess = () => {
            for (const next of nextRequest.result) {
              if (next.partition !== context.partition || next.account !== context.account ||
                  next.annotationID !== record.annotationID || next.attempted) continue;
              if (next.payload.method === "delete") next.payload.rev = server.rev;
              else next.payload.annotation.base_rev = server.rev;
              outbox.put(next);
            }

          };
        }
        annotations.put(local);
      };
    };
  }));
}

// Work annotations are a complete live set. Absence removes a cached server
// annotation, but never a local mutation (including a pending tombstone).
export async function reconcileOfflineBook(context, book, request, current = () => true) {
  if (!book.workID) return;
  const base = "v1/works/" + encodeURIComponent(book.workID);
  const [positions, annotations] = await Promise.all([
    request(base + "/positions?limit=1"), request(base + "/annotations"),
  ]);
  if (!positions.ok || !annotations.ok) throw new OfflineStorageError("Reading state could not be refreshed.");
  const [positionData, annotationData] = await Promise.all([positions.json(), annotations.json()]);
  if (!current(positions) || !current(annotations)) throw new OfflineStorageError("The reading credential changed.", "auth");
  if (!Array.isArray(positionData.ops) || !Array.isArray(annotationData.annotations))
    throw new OfflineStorageError("The reading state response is incomplete.");
  await withDB(db => guardedWork(db, [SNAPSHOTS, ANNOTATIONS, OUTBOX], context, tx => {
    const outbox = tx.objectStore(OUTBOX).getAll();
    outbox.onsuccess = () => {
      const pending = outbox.result.filter(value => value.partition === context.partition &&
        value.account === context.account && value.bookID === book.bookID);
      const snapshots = tx.objectStore(SNAPSHOTS);
      const copies = snapshots.index("book").getAll([context.partition, context.account, book.bookID]);
      copies.onsuccess = () => {
        // Queued positions survive removing and downloading the book again.
        const position = pending.filter(value => value.kind === "position")
          .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0))[0];
        for (const copy of copies.result) {
          copy.localPosition = position ? position.payload : positionData.ops[0] || null;
          snapshots.put(copy);
        }
      };
      const store = tx.objectStore(ANNOTATIONS);
      const locals = store.index("book").getAll([context.partition, context.account, book.bookID]);
      locals.onsuccess = () => {
        const dirty = new Set(pending.filter(value => value.kind === "annotation").map(value => value.annotationID));
        for (const local of locals.result) {
          if (!dirty.has(local.annotation.id)) store.delete(local.key);
        }
        for (const annotation of annotationData.annotations) {
          if (!dirty.has(annotation.id)) store.put({
            key: keyForAnnotation({ ...context, bookID: book.bookID, id: annotation.id }),
            partition: context.partition, account: context.account, bookID: book.bookID, annotation,
          });
        }
      };
    };
  }));
}

export async function clearOfflineAccount(partition, account) {
  if (!partition || !account) return;
  return withDB(async db => {
    await transactionWork(db, [
      SNAPSHOTS, RESOURCES, OUTBOX, CHECKPOINTS, ANNOTATIONS, ACCOUNTS,
    ], "readwrite", tx => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const resources = tx.objectStore(RESOURCES);
      const outbox = tx.objectStore(OUTBOX);
      const checkpoints = tx.objectStore(CHECKPOINTS);
      const annotations = tx.objectStore(ANNOTATIONS);
      let snapshotRecords, outboxRecords, checkpointRecords, annotationRecords;
      let remaining = 4;
      const clear = () => {
        if (--remaining) return;
        for (const snapshot of snapshotRecords) {
          for (const href of snapshot.resourceHrefs || [])
            resources.delete([snapshot.key, href]);
          snapshots.delete(snapshot.key);
        }
        for (const record of outboxRecords) outbox.delete(record.key);
        for (const checkpoint of checkpointRecords) {
          if (checkpoint.partition === partition && checkpoint.account === account)
            checkpoints.delete(checkpoint.key);
        }
        for (const annotation of annotationRecords) annotations.delete(annotation.key);
        const marker = tx.objectStore(ACCOUNTS).get(keyForAccount(partition));
        marker.onsuccess = () => {
          if (marker.result?.account === account)
            tx.objectStore(ACCOUNTS).put({ key: partition, partition, account: null, epoch: randomGeneration() });
        };
      };
      const snapshotRequest = snapshots.index("book").getAll(IDBKeyRange.bound(
        [partition, account, ""], [partition, account, "\uffff"],
      ));
      snapshotRequest.onsuccess = () => {
        snapshotRecords = snapshotRequest.result;
        clear();
      };
      const outboxRequest = outbox.index("account").getAll(IDBKeyRange.bound(
        [partition, account, "", 0],
        [partition, account, "\uffff", Number.MAX_SAFE_INTEGER],
      ));
      outboxRequest.onsuccess = () => {
        outboxRecords = outboxRequest.result;
        clear();
      };
      const checkpointRequest = checkpoints.getAll();
      checkpointRequest.onsuccess = () => {
        checkpointRecords = checkpointRequest.result;
        clear();
      };
      const annotationRequest = annotations.index("book").getAll(IDBKeyRange.bound(
        [partition, account, ""], [partition, account, "\uffff"],
      ));
      annotationRequest.onsuccess = () => {
        annotationRecords = annotationRequest.result;
        clear();
      };
    });
    notifyOfflineChange("logout", partition);
  });
}

function resourcePath(url, prefix) {
  const parsed = new URL(url);
  if (parsed.search || !parsed.pathname.startsWith(prefix)) return null;
  const href = parsed.pathname.slice(prefix.length);
  if (!href || href.split("/").some(part => part === ".." || part === ".")) return null;
  return href;
}

function publicationLinks(value, visit) {
  if (Array.isArray(value)) {
    for (const child of value) publicationLinks(child, visit);
    return;
  }
  if (!value || typeof value !== "object") return;
  if (typeof value.href === "string") visit(value.href, value.type || "");
  for (const child of Object.values(value)) publicationLinks(child, visit);
}

export function buildPublicationPlan(manifest, {
  manifestURL,
  expectedDigest = "",
} = {}) {
  const packageLink = (manifest.links || []).find(link =>
    (Array.isArray(link.rel) ? link.rel : [link.rel]).includes("package"));
  const positionLink = (manifest.links || []).find(link =>
    (Array.isArray(link.rel) ? link.rel : [link.rel]).includes("http://readium.org/position-list"));
  if (!packageLink || !positionLink || typeof manifestURL !== "string") {
    throw new OfflineStorageError("The publication has no complete resource graph.", "invalid-publication");
  }
  const transportURL = href => publicationTransportURL(href, manifestURL);
  const packageURL = new URL(transportURL(packageLink.href));
  const resourcesMarker = "/resources/";
  const marker = packageURL.pathname.indexOf(resourcesMarker);
  if (marker < 0 || packageURL.search || packageURL.origin !== new URL(manifestURL).origin) {
    throw new OfflineStorageError("The publication resource prefix is invalid.", "invalid-publication");
  }
  const resourcePrefix = packageURL.pathname.slice(0, marker + resourcesMarker.length);
  const digestMatch = packageURL.pathname.match(/\/publication\/([^/]+)\/resources\//);
  const digest = digestMatch?.[1] || "";
  if (!digest || (expectedDigest && digest !== expectedDigest)) {
    throw new OfflineStorageError("The publication changed while it was being downloaded.", "changed");
  }
  const packageHref = resourcePath(packageURL.href, resourcePrefix);
  const positionURL = new URL(transportURL(positionLink.href));
  if (!packageHref || positionURL.origin !== packageURL.origin || positionURL.search ||
      !positionURL.pathname.endsWith(`/publication/${digest}/positions.json`)) {
    throw new OfflineStorageError("The publication links are invalid.", "invalid-publication");
  }
  const resources = new Map();
  const add = (href, type) => {
    const key = resourcePath(href, resourcePrefix);
    if (!key) {
      if (href.startsWith("data:") || href.startsWith("#")) return;
      throw new OfflineStorageError("The publication contains a non-local resource link.", "invalid-publication");
    }
    resources.set(key, { href: key, type: type || "application/octet-stream" });
  };
  for (const collection of [manifest.readingOrder, manifest.resources, manifest.toc]) {
    publicationLinks(collection, (href, type) => add(transportURL(href), type));
  }
  publicationLinks(manifest, (href, type) => {
    if (href === packageLink.href || href === positionLink.href) return;
    const parsed = new URL(transportURL(href));
    if (parsed.origin === packageURL.origin && parsed.pathname.startsWith(resourcePrefix)) {
      add(parsed.href, type);
    }
  });
  resources.set(packageHref, { href: packageHref, type: packageLink.type || "application/oebps-package+xml" });
  return {
    digest,
    manifestURL,
    resourcePrefix,
    packageHref,
    packageURL: packageURL.href,
    positionURL: positionURL.href,
    resources,
  };
}

export function publicationTransportURL(href, manifestURL) {
  const base = new URL(manifestURL);
  const url = new URL(href, base);
  const marker = base.pathname.indexOf("/v1/");
  if (url.origin === base.origin && marker >= 0 && url.pathname.startsWith("/v1/"))
    url.pathname = base.pathname.slice(0, marker) + url.pathname;
  return url.href;
}

function linkedPublicationReferences(bytes, type, base) {
  const text = decodeText(bytes, { css: type === "text/css" });
  if (type === "text/css") {
    return [...text.matchAll(/(?:url|@import)\s*\(\s*["']?([^)"']+)["']?\s*\)/gi)]
      .map(match => ({
        value: publicationHref(match[1], base),
        type: inferType(match[1]),
      }))
      .filter(reference => reference.value);
  }
  if (!globalThis.DOMParser) return [];
  const document = new DOMParser().parseFromString(text, "application/xml");
  if (document.querySelector("parsererror")) return [];
  stripPublicationCode(document);
  return [...document.querySelectorAll("[href], [src], [poster], [background], [srcset]")]
    .filter(element => {
      const name = element.localName.toLowerCase();
      return name !== "a" && (name !== "link" ||
        /^(stylesheet|icon)$/i.test(element.getAttribute("rel") || ""));
    })
    .flatMap(element => {
      const values = [];
      for (const name of ["href", "src", "poster", "background"]) {
        if (element.hasAttribute(name)) values.push(element.getAttribute(name));
      }
      if (element.hasAttribute("srcset")) {
        values.push(...element.getAttribute("srcset").split(",").map(value => value.trim().split(/\s+/)[0]));
      }
      return values.map(value => ({
        value: publicationHref(value, base),
        type: element.getAttribute("media-type") || inferType(value),
      })).filter(reference => reference.value);
    });
}

function inferType(href) {
  const extension = href.split(/[?#]/)[0].split(".").pop()?.toLowerCase();
  return {
    css: "text/css",
    gif: "image/gif",
    html: "application/xhtml+xml",
    jpeg: "image/jpeg",
    jpg: "image/jpeg",
    otf: "font/otf",
    png: "image/png",
    svg: "image/svg+xml",
    ttf: "font/ttf",
    webp: "image/webp",
    woff: "font/woff",
    woff2: "font/woff2",
    xhtml: "application/xhtml+xml",
    xml: "application/xml",
  }[extension] || "application/octet-stream";
}
async function sha256(bytes) {
  if (!globalThis.crypto?.subtle) return "";
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join("");
}

async function putStagingSnapshot(meta) {
  return withDB(async db => {
    try {
      await guardedWork(db, SNAPSHOTS, meta, tx => tx.objectStore(SNAPSHOTS).put(meta));
    } catch (error) {
      if (quotaError(error)) throw new OfflineQuotaError();
      throw error;
    }
  });
}

async function putStagedResource(context, snapshotKeyValue, resource) {
  return withDB(async db => {
    try {
      await guardedWork(db, [SNAPSHOTS, RESOURCES], context, (tx, _, fail) => {
        const snapshots = tx.objectStore(SNAPSHOTS);
        const request = snapshots.get(snapshotKeyValue);
        request.onsuccess = () => {
          const snapshot = request.result;
          if (!snapshot || snapshot.state !== "staging") {
            fail(new OfflineStorageError("The download was cancelled."));
            return;
          }
          if (!snapshot.resourceHrefs.includes(resource.href))
            snapshot.resourceHrefs.push(resource.href);
          snapshots.put(snapshot);
          tx.objectStore(RESOURCES).put({
            snapshotKey: snapshotKeyValue,
            href: resource.href,
            type: resource.type,
            bytes: copyBytes(resource.bytes),
            digest: resource.digest,
            size: resource.bytes.byteLength,
          });
        };
      });
    } catch (error) {
      if (quotaError(error)) throw new OfflineQuotaError();
      throw error;
    }
  });
}

async function updateStagingManifest(context, snapshotKeyValue, manifest) {
  return withDB(async db => {
    await guardedWork(db, SNAPSHOTS, context, (tx, _, fail) => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const request = snapshots.get(snapshotKeyValue);
      request.onsuccess = () => {
        const snapshot = request.result;
        if (!snapshot || snapshot.state !== "staging") {
          fail(new OfflineStorageError("The download was cancelled."));
          return;
        }
        snapshot.manifest = manifest;
        snapshots.put(snapshot);
      };
    });
  });
}

async function deleteSnapshotKey(snapshotKeyValue) {
  return withDB(async db => {
    await transactionWork(db, [SNAPSHOTS, RESOURCES], "readwrite", tx => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const resources = tx.objectStore(RESOURCES);
      const request = snapshots.get(snapshotKeyValue);
      request.onsuccess = () => {
        const snapshot = request.result;
        for (const href of snapshot?.resourceHrefs || [])
          resources.delete([snapshotKeyValue, href]);
        snapshots.delete(snapshotKeyValue);
      };
    });
  });
}

async function commitSnapshot(context, snapshotKeyValue) {
  return withDB(async db => {
    return guardedWork(db, [SNAPSHOTS, RESOURCES], context, (tx, set, fail) => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const resources = tx.objectStore(RESOURCES);
      const currentRequest = snapshots.get(snapshotKeyValue);
      currentRequest.onsuccess = () => {
        const current = currentRequest.result;
        if (!current || current.state !== "staging") {
          fail(new OfflineStorageError("The download was cancelled."));
          return;
        }
        const readyRequest = snapshots.index("ready").getAll([
          current.partition, current.account, "ready",
        ]);
        readyRequest.onsuccess = () => {
          for (const old of readyRequest.result) {
            if (old.bookID === current.bookID && old.key !== current.key) {
              for (const href of old.resourceHrefs || [])
                resources.delete([old.key, href]);
              snapshots.delete(old.key);
            }
          }
          current.state = "ready";
          current.readyAt = Date.now();
          snapshots.put(current);
          set(current);
        };
      };
    });
  });
}

export async function listReadySnapshots(partition, account) {
  return withDB(async db => {
    const snapshots = await requestResult(db.transaction(SNAPSHOTS).objectStore(SNAPSHOTS)
      .index("ready").getAll([partition, account, "ready"]));
    return snapshots.sort((a, b) => (b.readyAt || 0) - (a.readyAt || 0));
  });
}

export async function getReadySnapshot({ partition = storagePartition(), account, bookID, digest = "" } = {}) {
  const selectedAccount = account || await activeAccount(partition);
  if (!selectedAccount) return null;
  return withDB(async db => {
    const snapshots = await requestResult(db.transaction(SNAPSHOTS).objectStore(SNAPSHOTS)
      .index("book").getAll([partition, selectedAccount, bookID]));
    const snapshot = snapshots
      .filter(value => value.state === "ready" && (!digest || value.digest === digest))
      .sort((a, b) => (b.readyAt || 0) - (a.readyAt || 0))[0];
    if (!snapshot) return null;
    const resources = await requestResult(db.transaction(RESOURCES).objectStore(RESOURCES)
      .getAll(IDBKeyRange.bound([snapshot.key, ""], [snapshot.key, "\uffff"])));
    const byHref = new Map(resources.map(resource => [resource.href, resource]));
    return {
      ...snapshot,
      request: (path, options) => localPublicationRequest(snapshot, byHref, path, options),
    };
  });
}

function jsonResponse(value, url) {
  const body = new TextEncoder().encode(JSON.stringify(value));
  return responseLike(body, "application/json", url);
}

function responseLike(bytes, type, url) {
  return {
    ok: true,
    status: 200,
    url,
    headers: new Headers({ "Content-Type": type, "Content-Length": String(bytes.byteLength) }),
    arrayBuffer: async () => copyBytes(bytes),
    json: async () => JSON.parse(new TextDecoder().decode(bytes)),
    blob: async () => new Blob([bytes], { type }),
  };
}

export function localPublicationRequest(snapshot, resources, path) {
  const clean = path.replace(/^\/+/, "");
  if (clean.endsWith("/publication/manifest.json")) {
    return Promise.resolve(jsonResponse(snapshot.manifest, clean));
  }
  if (clean.includes("/publication/") && clean.endsWith("/positions.json")) {
    return Promise.resolve(jsonResponse(snapshot.positions, clean));
  }
  const marker = "/resources/";
  const index = clean.indexOf(marker);
  if (index >= 0) {
    const rawHref = clean.slice(index + marker.length).split("#")[0];
    const resource = resources.get(rawHref) ||
      [...resources.values()].find(value => {
        try { return decodeURIComponent(value.href) === decodeURIComponent(rawHref); }
        catch { return value.href === rawHref; }
      });
    if (!resource) return Promise.resolve({ ok: false, status: 404, url: clean });
    return Promise.resolve(responseLike(new Uint8Array(resource.bytes), resource.type, clean));
  }
  return Promise.resolve({ ok: false, status: 404, url: clean });
}

export async function removeBookSnapshots({
  partition = storagePartition(), account, epoch, bookID,
} = {}) {
  const selectedAccount = account || await activeAccount(partition);
  if (!selectedAccount) return;
  return withDB(async db => {
    await guardedWork(db, [SNAPSHOTS, RESOURCES], {
      partition, account: selectedAccount, epoch,
    }, tx => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const resources = tx.objectStore(RESOURCES);
      let records;
      const clear = () => {
        for (const snapshot of records) {
          for (const href of snapshot.resourceHrefs || [])
            resources.delete([snapshot.key, href]);
          snapshots.delete(snapshot.key);
        }
      };
      const snapshotRequest = snapshots.index("book").getAll([
        partition, selectedAccount, bookID,
      ]);
      snapshotRequest.onsuccess = () => {
        records = snapshotRequest.result;
        clear();
      };
    });
  });
}

async function removeStagingSnapshots(context, bookID) {
  return withDB(async db => {
    await guardedWork(db, [SNAPSHOTS, RESOURCES], context, tx => {
      const snapshots = tx.objectStore(SNAPSHOTS);
      const resources = tx.objectStore(RESOURCES);
      const request = snapshots.index("book").getAll([
        context.partition, context.account, bookID,
      ]);
      request.onsuccess = () => {
        for (const snapshot of request.result) {
          if (snapshot.state !== "staging") continue;
          for (const href of snapshot.resourceHrefs || [])
            resources.delete([snapshot.key, href]);
          snapshots.delete(snapshot.key);
        }
      };
    });
  });
}


export async function downloadPublication({
  request,
  current = () => true,
  partition = storagePartition(),
  account,
  epoch,
  bookID,
  workID = "",
  deviceID = "",
  supportsActiveMs = false,
  expectedDigest = "",
  signal,
  concurrency = 4,
  onProgress = () => {},
} = {}) {
  if (!request || !account || !bookID) throw new OfflineStorageError("A publication download needs an account and book.");
  const context = { partition, account, epoch };
  await assertOfflineContext(context);
  const manifestPath = `v1/books/${encodeURIComponent(bookID)}/publication/manifest.json`;
  const manifestResponse = await request(manifestPath, { signal });
  if (!manifestResponse.ok) throw new OfflineStorageError("The publication manifest could not be downloaded.", "download");
  const manifest = await manifestResponse.json();
  if (!current(manifestResponse)) throw new OfflineStorageError("The reading credential changed.", "auth");
  const plan = buildPublicationPlan(manifest, {
    manifestURL: manifestResponse.url ||
      new URL(manifestPath, new URL(deploymentPrefix(), location.href)).href,
    expectedDigest,
  });
  const transportPath = url => new URL(url).pathname.replace(/^.*?\/(?=v1\/)/, "");
  const positionsPath = transportPath(plan.positionURL);
  const positionsResponse = await request(positionsPath, { signal });
  if (!positionsResponse.ok || !current(positionsResponse) ||
      (positionsResponse.url &&
       new URL(positionsResponse.url).href !== new URL(plan.positionURL).href)) {
    throw new OfflineStorageError("Publication positions could not be downloaded.", "download");
  }
  const positions = await positionsResponse.json();
  const generation = randomGeneration();
  const key = snapshotKey({ partition, account, bookID, digest: plan.digest, generation });
  const snapshot = {
    key, partition, account, epoch, bookID, workID, deviceID, supportsActiveMs,
    digest: plan.digest, generation,
    state: "staging", resourceHrefs: [], manifest, positions,
    packageHref: plan.packageHref, title: manifest.metadata?.title || bookID,
    author: (manifest.metadata?.author || {}).name || "",
    createdAt: Date.now(),
  };
  await removeStagingSnapshots(context, bookID);
  await putStagingSnapshot(snapshot);
  const queue = [...plan.resources.values()];
  const seen = new Set(queue.map(resource => resource.href));
  let completed = 0;
  try {
    while (queue.length) {
      signal?.throwIfAborted();
      const batch = queue.splice(0, concurrency);
      const downloaded = await Promise.all(batch.map(async entry => {
        const path = transportPath(new URL(plan.resourcePrefix + entry.href, plan.manifestURL));
        const response = await request(path, { signal });
        if (!response.ok || !current(response) ||
            (response.url && new URL(response.url).href !==
              new URL(plan.resourcePrefix + entry.href, plan.manifestURL).href)) {
          throw new OfflineStorageError("A publication resource could not be downloaded.", "download");
        }
        const bytes = new Uint8Array(await response.arrayBuffer());
        const length = response.headers?.get("Content-Length");
        // Fetch decodes compressed bodies but retains their encoded length.
        const encoding = response.headers?.get("Content-Encoding")?.trim().toLowerCase();
        if ((!encoding || encoding === "identity") && length && Number(length) !== bytes.byteLength) {
          throw new OfflineStorageError("A publication resource was truncated.", "download");
        }
        return {
          ...entry,
          type: (() => {
            const responseType = response.headers?.get("Content-Type")?.split(";")[0];
            return responseType && responseType !== "application/octet-stream"
              ? responseType : entry.type;
          })(),
          bytes,
          digest: await sha256(bytes),
        };
      }));
      let manifestChanged = false;
      for (const resource of downloaded) {
        await putStagedResource(context, key, resource);
        completed++;
        onProgress({ completed, total: Math.max(completed, seen.size), bytes: resource.bytes.byteLength });
        for (const reference of linkedPublicationReferences(resource.bytes, resource.type, resource.href)) {
          const href = (typeof reference === "string" ? reference : reference.value)?.split("#")[0];
          if (!href || seen.has(href)) continue;
          seen.add(href);
          const linked = {
            href,
            type: typeof reference === "string"
              ? (resource.type === "text/css" ? "text/css" : inferType(href))
              : reference.type,
          };
          plan.resources.set(href, linked);
          manifest.resources ||= [];
          if (!manifest.resources.some(value => value.href === href)) {
            manifest.resources.push(linked);
            manifestChanged = true;
          }
          queue.push(linked);
        }
      }
      if (manifestChanged) await updateStagingManifest(context, key, manifest);
    }
    await reconcileOfflineBook(context, snapshot, request, current);
    const committed = await commitSnapshot(context, key);
    return committed;
  } catch (error) {
    await deleteSnapshotKey(key).catch(() => {});
    if (error instanceof OfflineStorageError) throw error;
    if (quotaError(error)) throw new OfflineQuotaError();
    throw error;
  }
}
