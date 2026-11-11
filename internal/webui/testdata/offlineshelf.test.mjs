import { test } from "node:test";
import assert from "node:assert/strict";
import {
  OFFLINE_NOTE,
  authorFor,
  coverSource,
  formatLabel,
  initialFor,
  networkPresentation,
  progressClass,
  progressFor,
  progressLabel,
  removalGuard,
  savedLabel,
  sizeLabel,
  titleFor,
} from "../static/offline/offline-shelf-model.js";
import { bookCoverDataURL, offlineShelfURL } from "../static/offline-storage.js";

test("a saved card says what is known and leaves out what is not", () => {
  const snapshot = { title: "  The Left Hand of Darkness  ", author: " Ursula K. Le Guin " };
  assert.equal(titleFor(snapshot), "The Left Hand of Darkness");
  assert.equal(authorFor(snapshot), "Ursula K. Le Guin");
  assert.equal(initialFor(snapshot), "T");
  assert.equal(titleFor({}), "Untitled book");
  assert.equal(authorFor({}), "");
  assert.equal(initialFor({}), "U", "an untitled book still gets a tile");
  assert.equal(progressFor(snapshot), null);
  assert.equal(progressFor({ localPosition: { progression: 0.42 } }), 0.42);
  assert.equal(progressFor({
    localPosition: { progression: 0.42, locator: { locations: { totalProgression: 0.7 } } },
  }), 0.7);
  assert.equal(progressLabel(1), "Finished");
  assert.equal(progressLabel(0.423), "42% read");
  assert.equal(progressClass(0.423), "p40");
});

test("a size is on the card only once the device knows one", () => {
  assert.equal(sizeLabel(0), "");
  assert.equal(sizeLabel(undefined), "");
  assert.equal(sizeLabel(512), "512 B");
  assert.equal(sizeLabel(4200 * 1000), "4.2 MB");
  assert.equal(sizeLabel(420 * 1000 * 1000), "420 MB");
  assert.equal(sizeLabel(3_500_000_000), "3.5 GB");
});

test("a saved date is dated, and an older copy without one still reads", () => {
  assert.equal(savedLabel({}), "Saved on this device");
  assert.match(savedLabel({ readyAt: Date.UTC(2024, 3, 12) }), /^Saved .*2024/);
});

test("a cover is only the image the download kept", () => {
  assert.equal(coverSource({}), "");
  assert.equal(coverSource({ cover: "https://example.test/cover.png" }), "");
  assert.equal(coverSource({ cover: "data:image/png;base64,AAAA" }), "data:image/png;base64,AAAA");
  assert.equal(formatLabel({}), "Ebook");
  assert.equal(formatLabel({ mediaType: "audio/mp4" }), "Audiobook");
});

test("the header stops offering a library it cannot reach", () => {
  assert.deepEqual(networkPresentation(true), { library: "shown", brand: "link", note: "" });
  const offline = networkPresentation(false);
  assert.equal(offline.library, "hidden");
  assert.equal(offline.brand, "static");
  assert.equal(offline.note, OFFLINE_NOTE);
  // A browser that could not say either way keeps the link: the shell
  // must not claim to be offline just because nothing answered.
  assert.equal(networkPresentation(undefined).library, "shown");
});

test("removing a saved copy takes two presses", () => {
  const events = [];
  const guard = removalGuard({
    arm: () => events.push("arm"),
    disarm: () => events.push("disarm"),
    confirm: () => events.push("remove"),
  });
  assert.equal(guard.press(), "armed");
  assert.deepEqual(events, ["arm"]);
  assert.equal(guard.armed(), true);
  assert.equal(guard.press(), "remove");
  assert.deepEqual(events, ["arm", "remove"]);
  assert.equal(guard.armed(), false);
});

test("an unanswered question goes quiet again", () => {
  const timers = [];
  const events = [];
  const guard = removalGuard({
    arm: () => events.push("arm"),
    disarm: () => events.push("disarm"),
    confirm: () => events.push("remove"),
    timeout: 8000,
    setTimer: (fn, ms) => {
      timers.push({ fn, ms });
      return timers.length;
    },
    clearTimer: () => {},
  });
  guard.press();
  assert.equal(timers.length, 1);
  assert.equal(timers[0].ms, 8000);
  timers[0].fn();
  assert.deepEqual(events, ["arm", "disarm"]);
  assert.equal(guard.armed(), false);
  // An expired question cannot be answered by a later press.
  assert.equal(guard.press(), "armed");
});

test("keeping a copy cancels the question and re-arms nothing", () => {
  const events = [];
  let cleared = 0;
  const guard = removalGuard({
    arm: () => events.push("arm"),
    disarm: () => events.push("disarm"),
    confirm: () => events.push("remove"),
    setTimer: () => 7,
    clearTimer: () => { cleared++; },
  });
  guard.press();
  assert.equal(guard.cancel(), true);
  assert.deepEqual(events, ["arm", "disarm"]);
  assert.equal(cleared, 1, "the expiry must not fire after a kept copy");
  assert.equal(guard.cancel(), false);
});

function timedGuard({ held = () => false } = {}) {
  const timers = [];
  const events = [];
  const guard = removalGuard({
    arm: () => events.push("arm"),
    disarm: () => events.push("disarm"),
    confirm: () => events.push("remove"),
    hold: held,
    timeout: 8000,
    setTimer: (fn, ms) => {
      timers.push({ fn, ms });
      return timers.length;
    },
    clearTimer: () => {},
  });
  return { guard, timers, events };
}

test("a question a reader is still on does not expire under them", () => {
  let held = true;
  const { guard, timers, events } = timedGuard({ held: () => held });
  guard.press();
  assert.equal(timers.length, 1);
  timers[0].fn();
  assert.deepEqual(events, ["arm"], "the deadline passed with focus inside the question");
  assert.equal(guard.armed(), true, "the question is still there to answer");
  assert.equal(guard.press(), "remove", "and it can still be answered");
  assert.deepEqual(events, ["arm", "remove"]);
});

test("a deadline that ran out while held goes quiet when the reader leaves", () => {
  let held = true;
  const { guard, timers, events } = timedGuard({ held: () => held });
  guard.press();
  timers[0].fn();
  assert.equal(guard.refresh(), false, "still under the pointer or the focus");
  assert.equal(guard.armed(), true);
  held = false;
  assert.equal(guard.refresh(), true);
  assert.deepEqual(events, ["arm", "disarm"]);
  assert.equal(guard.armed(), false);
  assert.equal(guard.refresh(), false, "nothing left to take away");
});

test("a removal that failed leaves the question answerable", () => {
  const { guard, timers, events } = timedGuard();
  guard.press();
  assert.equal(guard.press(), "remove");
  assert.deepEqual(events, ["arm", "remove"]);
  assert.equal(guard.armed(), false, "the failing press cleared the guard");
  guard.rearm();
  assert.equal(guard.armed(), true, "the question on the card is armed again");
  assert.deepEqual(events, ["arm", "remove"], "re-arming asks nothing new");
  assert.equal(timers.length, 2, "and the deadline starts over");
  assert.equal(guard.cancel(), true, "Keep still works");
  assert.deepEqual(events, ["arm", "remove", "disarm"]);
});

test("a card redrawn mid-question leaves no timer behind", () => {
  const events = [];
  let cleared = 0;
  const guard = removalGuard({
    arm: () => events.push("arm"),
    disarm: () => events.push("disarm"),
    confirm: () => events.push("remove"),
    setTimer: () => 9,
    clearTimer: () => { cleared++; },
  });
  guard.press();
  guard.dispose();
  assert.equal(cleared, 1, "the expiry goes with the card it belonged to");
  assert.equal(guard.armed(), false);
  assert.deepEqual(events, ["arm"], "a redraw is not a reader answering the question");
  assert.equal(guard.press(), "armed", "the disposed guard still works for a new card");
});

function response(bytes, type = "image/png", extra = {}) {
  return {
    ok: true,
    headers: {
      get: name => ({
        "Content-Type": type,
        "Content-Length": String(bytes.byteLength),
      })[name] ?? extra[name] ?? null,
    },
    arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
  };
}

test("a cover is kept with the book as a data URL", async () => {
  const paths = [];
  const cover = await bookCoverDataURL({
    bookID: "book 1",
    request: async path => {
      paths.push(path);
      return response(new Uint8Array([0x89, 0x50, 0x4e, 0x47]));
    },
  });
  assert.deepEqual(paths, ["v1/books/book%201/cover?size=thumbnail"]);
  assert.equal(cover, "data:image/png;base64,iVBORw==");
  assert.equal(await bookCoverDataURL({
    bookID: "b",
    request: async () => ({ ...response(new Uint8Array([1])), ok: false }),
  }), "", "a book without a cover is no cover, not a failed download");
  assert.equal(await bookCoverDataURL({
    bookID: "b",
    request: async () => response(new Uint8Array([1]), "text/html"),
  }), "");
  assert.equal(await bookCoverDataURL({
    bookID: "b",
    request: async () => response(new Uint8Array(600 * 1024)),
  }), "", "a cover too large to be worth carrying is left behind");
  assert.equal(await bookCoverDataURL({
    bookID: "b",
    request: async () => { throw new Error("network down"); },
  }), "");
});

test("the shelf URL is absolute under the deployment prefix", () => {
  assert.equal(offlineShelfURL(new URL("https://reader.example/ui/books/book-1")),
    "/ui/offline/");
  assert.equal(offlineShelfURL(new URL("https://reader.example/sync/ui/library")),
    "/sync/ui/offline/");
});
