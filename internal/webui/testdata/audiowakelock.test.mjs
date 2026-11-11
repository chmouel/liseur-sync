import { test } from "node:test";
import assert from "node:assert/strict";
import { screenWakeLock } from "../static/audio-wake-lock.js";

function fakeNavigator({ refuse = false } = {}) {
  const requests = [];
  const created = [];
  return {
    requests,
    created,
    wakeLock: {
      request: async () => {
        requests.push("screen");
        if (refuse) throw new Error("NotAllowedError");
        const listeners = new Map();
        const sentinel = {
          released: 0,
          addEventListener(event, fn) {
            if (!listeners.has(event)) listeners.set(event, []);
            listeners.get(event).push(fn);
          },
          emit(event) { for (const fn of listeners.get(event) || []) fn(); },
          async release() { this.released++; this.emit("release"); },
        };
        created.push(sentinel);
        return sentinel;
      },
    },
  };
}

test("a requested lock is held while the page is visible", async () => {
  const nav = fakeNavigator();
  const lock = screenWakeLock({ nav, doc: { hidden: false } });
  await lock.sync(true);
  assert.deepEqual(nav.requests, ["screen"]);
  assert.equal(lock.held(), true);
});

test("a hidden page never requests, and hiding releases what is held", async () => {
  const nav = fakeNavigator();
  const doc = { hidden: true };
  const lock = screenWakeLock({ nav, doc });
  await lock.sync(true);
  assert.deepEqual(nav.requests, []);
  assert.equal(lock.held(), false);

  doc.hidden = false;
  await lock.sync(true);
  assert.equal(lock.held(), true);
  assert.equal(nav.requests.length, 1);

  doc.hidden = true;
  await lock.sync(true);
  assert.equal(lock.held(), false);
  assert.equal(nav.created[0].released, 1);
});

test("re-syncing visible after the browser dropped the lock requests it again", async () => {
  const nav = fakeNavigator();
  const doc = { hidden: false };
  const lock = screenWakeLock({ nav, doc });
  await lock.sync(true);
  assert.equal(lock.held(), true);
  // WebKit drops the sentinel when the page is hidden and fires release.
  nav.created[0].emit("release");
  assert.equal(lock.held(), false);
  doc.hidden = true;
  await lock.sync(true);
  doc.hidden = false;
  await lock.sync(true);
  assert.equal(nav.requests.length, 2);
  assert.equal(lock.held(), true);
});

test("pausing releases the sentinel", async () => {
  const nav = fakeNavigator();
  const lock = screenWakeLock({ nav, doc: { hidden: false } });
  await lock.sync(true);
  await lock.sync(false);
  assert.equal(lock.held(), false);
  assert.equal(nav.created[0].released, 1);
});

test("a refused request or a missing API is silently ignored", async () => {
  const nav = fakeNavigator({ refuse: true });
  const lock = screenWakeLock({ nav, doc: { hidden: false } });
  await lock.sync(true);
  assert.equal(lock.held(), false);

  const none = screenWakeLock({ nav: {}, doc: { hidden: false } });
  await none.sync(true);
  assert.equal(none.held(), false);
});

test("a request answered after the reason went away releases immediately", async () => {
  const nav = fakeNavigator();
  const doc = { hidden: false };
  const lock = screenWakeLock({ nav, doc });
  const pending = lock.sync(true);
  doc.hidden = true;
  await pending;
  assert.equal(lock.held(), false);
  assert.equal(nav.created[0].released, 1, "the late sentinel must be released");
});
