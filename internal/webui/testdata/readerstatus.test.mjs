// Unit tests for the two things the browser reader adds to a position
// (ADR-0066): whether the recording it was mapped from covers the book,
// and the act that takes back an explicit unread choice. Run by
// TestReaderStatusHelpers through `node --test`; no browser.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";

const source = readFileSync(new URL("../static/reader-app.js", import.meta.url), "utf8");

function reader({ readingChoice = "", mapping = null, fromAudio = null, lastSynced = -1 } = {}) {
  const context = createContext({
    cfg: { readingChoice },
    audioInfo: mapping ? { mapping } : null,
    audioSyncLocator: fromAudio,
    audioLastSyncedProg: lastSynced,
    finite: Number.isFinite,
  });
  runInContext(
    source.slice(source.indexOf("function pendingCoverage"), source.indexOf("async function pushPosition")),
    context,
  );
  return context;
}

const locator = totalProgression => ({ locations: { totalProgression } });

test("an ebook page carries no coverage", () => {
  const r = reader();
  assert.equal(r.pendingCoverage(locator(0.5)), null);
});

test("a position worked out from the recording reports its span", () => {
  const partial = reader({ mapping: { full_narrative: false }, fromAudio: locator(0.9) });
  assert.equal(partial.pendingCoverage(locator(0.9)), "partial");
  const full = reader({ mapping: { full_narrative: true }, fromAudio: locator(0.9) });
  assert.equal(full.pendingCoverage(locator(0.9)), "full");
});

test("the page narration moved the view to is still the recording's place", () => {
  // Following along clears the audio locator, but the fraction it left
  // behind is the mapped one and must keep its coverage.
  const r = reader({ mapping: { full_narrative: false }, fromAudio: null, lastSynced: 0.998 });
  assert.equal(r.pendingCoverage(locator(0.998)), "partial");
  assert.equal(r.pendingCoverage(locator(0.4)), null);
});

test("resuming withdraws an unread choice, once, under a derived id", () => {
  const op = { op_id: "p-1", work_id: "w-1", client_ts: "t", progression: 0.3, edition_sha: "sha" };
  const none = reader();
  assert.equal(none.withdrawalAct(op), null);

  const unread = reader({ readingChoice: "unread" });
  const act = unread.withdrawalAct(op);
  assert.equal(act.kind, "status");
  assert.equal(act.reading_status, "automatic");
  assert.equal(act.work_id, "w-1");
  assert.equal(act.progression, 0.3);
  assert.equal(act.edition_sha, "sha");
  assert.equal(act.locator, undefined);
  // Derived from the position, so a replayed push repeats the same act.
  assert.equal(unread.withdrawalAct(op).op_id, act.op_id);
  assert.equal(act.op_id, "unread:p-1");

  runInContext("readingChoiceWithdrawn = true", unread);
  assert.equal(unread.withdrawalAct(op), null);
});
