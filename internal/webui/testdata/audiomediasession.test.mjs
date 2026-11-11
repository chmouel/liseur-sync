import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MEDIA_ACTIONS, mediaMetadata, installMediaSession, setMediaPlaybackState,
  updateMediaPosition, clearMediaSession,
} from "../static/audio-media-session.js";

function metadata(values) {
  return { kind: "metadata", ...values };
}

function session() {
  return {
    metadata: null,
    playbackState: "none",
    handlers: new Map(),
    positions: [],
    setActionHandler(action, handler) { this.handlers.set(action, handler); },
    setPositionState(state) { this.positions.push(state); },
  };
}

test("artwork keeps only entries with a source and copies sizes and type", () => {
  const built = mediaMetadata(metadata, {
    title: "A Book", artist: "An Author", album: "Chapter One",
    artwork: [{ src: "cover.jpg", sizes: "512x512", type: "image/jpeg" }, { sizes: "1x1" }, null],
  });
  assert.equal(built.title, "A Book");
  assert.equal(built.album, "Chapter One");
  assert.deepEqual(built.artwork, [{ src: "cover.jpg", sizes: "512x512", type: "image/jpeg" }]);
});

test("no MediaMetadata constructor yields nothing rather than throwing", () => {
  assert.equal(mediaMetadata(undefined, { title: "x" }), null);
  assert.equal(mediaMetadata(null, { title: "x" }), null);
});

test("installing registers handlers and leaves unset actions alone", () => {
  const s = session();
  const play = () => {};
  const installed = installMediaSession(s, {
    Metadata: metadata,
    metadata: { title: "Book" },
    handlers: { play, pause: null },
  });
  assert.equal(installed, true);
  assert.equal(s.metadata.title, "Book");
  assert.equal(s.handlers.get("play"), play);
  assert.equal(s.handlers.get("pause"), null);
  assert.equal(s.handlers.has("seekforward"), false);
});

test("a null or missing session is inert", () => {
  assert.equal(installMediaSession(null, { metadata: { title: "x" } }), false);
  assert.equal(setMediaPlaybackState(null, "playing"), false);
  assert.equal(updateMediaPosition(null, { duration: 1, position: 0 }), false);
  assert.equal(clearMediaSession(null), false);
});

test("position state is clamped and refused when there is no duration yet", () => {
  const s = session();
  assert.equal(updateMediaPosition(s, { duration: 100, position: 250, playbackRate: 1.5 }), true);
  assert.deepEqual(s.positions[0], { duration: 100, playbackRate: 1.5, position: 100 });
  assert.equal(updateMediaPosition(s, { duration: 0, position: 1 }), false);
  assert.equal(updateMediaPosition(s, { duration: 100, position: NaN }), false);
  assert.equal(updateMediaPosition(s, { duration: 100, position: -5 }), true);
  assert.equal(s.positions[1].position, 0);
  assert.equal(updateMediaPosition(s, { duration: 10, position: 1, playbackRate: 0 }), true);
  assert.equal(s.positions[2].playbackRate, 1);
});

test("a throwing session never propagates", () => {
  const s = session();
  s.setPositionState = () => { throw new Error("position > duration"); };
  s.setActionHandler = () => { throw new Error("unsupported action"); };
  assert.equal(updateMediaPosition(s, { duration: 10, position: 1 }), false);
  assert.equal(installMediaSession(s, { handlers: { seekto() {} } }), true);
});

test("clearing drops metadata, state and every known handler", () => {
  const s = session();
  for (const action of MEDIA_ACTIONS) s.handlers.set(action, () => {});
  clearMediaSession(s);
  assert.equal(s.metadata, null);
  assert.equal(s.playbackState, "none");
  for (const action of MEDIA_ACTIONS) assert.equal(s.handlers.get(action), null, action);
});
