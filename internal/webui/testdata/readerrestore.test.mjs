// Unit tests for the restore ladder. Issue #184: a page turned in one
// client reopened in the wrong part of the book in the other, because
// the only thing both clients agreed on was a fraction of the whole
// book, and they compute that fraction differently. The rungs above the
// fraction are what these tests are about.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  agreedEdition, sameEdition, sectionHrefIn, startCandidates, cfiOf,
} from "../static/reader-restore.js";

const SPINE = [
  { id: "OEBPS/cover.xhtml" },
  { id: "OEBPS/ch1.xhtml" },
  { id: "OEBPS/ch2 the%20long one.xhtml" },
];

const view = (over = {}) => ({
  editionSHA: "a".repeat(64),
  sections: SPINE,
  resolveKey: () => null,
  cfiResolves: () => true,
  ...over,
});

const phoneOp = (over = {}) => ({
  op_id: "op-1",
  work_id: "work",
  progression: 0.31,
  edition_sha: "a".repeat(64),
  locator: {
    href: "OEBPS/ch2 the%20long one.xhtml",
    type: "application/xhtml+xml",
    locations: { progression: 0.74, totalProgression: 0.31, position: 812 },
  },
  ...over,
});

test("a digest is only agreed when both sides say the same thing", () => {
  assert.equal(agreedEdition("abc", "abc"), "abc");
  assert.equal(agreedEdition("abc", "def"), "");
  assert.equal(agreedEdition("", "abc"), "");
  assert.equal(agreedEdition("abc", ""), "");
  assert.equal(agreedEdition(undefined, undefined), "");
});

test("an edition nobody named is still eligible", () => {
  // Every op said nothing about its edition before this existed.
  // Refusing them all would make this reader worse at reopening old
  // positions than it was before the fix.
  assert.equal(sameEdition("", "abc"), true);
  assert.equal(sameEdition("abc", ""), true);
  assert.equal(sameEdition("abc", "abc"), true);
  assert.equal(sameEdition("abc", "def"), false);
});

test("a chapter is found through another client's spelling of it", () => {
  assert.equal(sectionHrefIn("OEBPS/ch1.xhtml", SPINE), "OEBPS/ch1.xhtml");
  assert.equal(sectionHrefIn("/OEBPS/ch1.xhtml", SPINE), "OEBPS/ch1.xhtml");
  assert.equal(sectionHrefIn("OEBPS/ch1.xhtml#p42", SPINE), "OEBPS/ch1.xhtml");
  assert.equal(sectionHrefIn("/OEBPS/ch1.xhtml?v=2", SPINE), "OEBPS/ch1.xhtml");
  // An equivalent percent-encoding is the publication's business.
  assert.equal(
    sectionHrefIn("OEBPS/ch2%20the%20long%20one.xhtml", SPINE, (key) =>
      key === "OEBPS/ch2%20the%20long%20one.xhtml" ? SPINE[2].id : null),
    SPINE[2].id,
  );
  assert.equal(sectionHrefIn("OEBPS/nowhere.xhtml", SPINE), null);
  assert.equal(sectionHrefIn("", SPINE), null);
  assert.equal(sectionHrefIn(undefined, SPINE), null);
});

test("the chapter is tried before the fraction", () => {
  const out = startCandidates(phoneOp(), view());
  assert.equal(out[0].href, "OEBPS/ch2 the%20long one.xhtml");
  assert.equal(out[0].locations.progression, 0.74);
  assert.deepEqual(out[1], { fraction: 0.31 });
});

test("another client's page number does not come along", () => {
  // 812 is Readium's synthetic position on a phone: a count of pages
  // through the whole book by the phone's reckoning, which is not the
  // list this reader was served. The engine keys the open by its own
  // list and the resource named here.
  const out = startCandidates(phoneOp(), view());
  assert.equal("position" in out[0].locations, false);
});

test("this reader's own page number does not come along either", () => {
  // A page saved here carries the position the list of that day gave
  // it. A list can be regenerated; the resource and the progression
  // within it are what reopen the page, whoever wrote them.
  const own = phoneOp({
    edition_sha: undefined,
    locator: {
      href: "OEBPS/ch2 the%20long one.xhtml",
      locations: { progression: 0.74, totalProgression: 0.31, position: 285 },
    },
  });
  const out = startCandidates(own, view());
  assert.equal("position" in out[0].locations, false);
  assert.equal(out[0].locations.progression, 0.74);
});

test("a fragment spelled on the href is kept as a fragment", () => {
  // The href is canonicalized to this book's spelling, which drops the
  // fragment; an element it named must still be there for the engine
  // to walk to.
  const out = startCandidates(
    phoneOp({ locator: { href: "/OEBPS/ch2 the%20long one.xhtml#p42", locations: {} } }),
    view(),
  );
  assert.equal(out[0].href, "OEBPS/ch2 the%20long one.xhtml");
  assert.deepEqual(out[0].locations.fragments, ["p42"]);
  // A CFI already in `fragments` is the finer pointer and is left alone.
  const cfi = startCandidates(
    phoneOp({
      locator: {
        href: "/OEBPS/ch2 the%20long one.xhtml#p42",
        locations: { fragments: ["epubcfi(/6/4!/4/2)"] },
      },
    }),
    view(),
  );
  assert.equal(cfi[0], "epubcfi(/6/4!/4/2)");
  assert.deepEqual(cfi[1].locations.fragments, ["epubcfi(/6/4!/4/2)"]);
});

test("a position in another edition falls to the fraction", () => {
  const out = startCandidates(phoneOp({ edition_sha: "b".repeat(64) }), view());
  assert.deepEqual(out[0], { fraction: 0.31 });
  // The bare chapter is still offered last: by then the alternative is
  // opening at page one.
  assert.equal(out.at(-1), "OEBPS/ch2 the%20long one.xhtml");
  assert.equal(out.some((c) => c && c.locations), false);
});

test("a CFI this reader wrote is tried first, and only if it resolves", () => {
  const withCFI = phoneOp({
    locator: {
      href: "OEBPS/ch1.xhtml",
      locations: { progression: 0.5, totalProgression: 0.2, fragments: ["epubcfi(/6/4!/4/2)"] },
    },
  });
  assert.equal(startCandidates(withCFI, view())[0], "epubcfi(/6/4!/4/2)");
  const out = startCandidates(withCFI, view({ cfiResolves: () => false }));
  assert.equal(out[0].href, "OEBPS/ch1.xhtml");
});

test("a chapter this book does not have is skipped, not followed", () => {
  const out = startCandidates(
    phoneOp({ locator: { href: "OTHER/ch9.xhtml", locations: { progression: 0.5 } } }),
    view(),
  );
  assert.deepEqual(out, [{ fraction: 0.31 }]);
});

test("a broken fraction is not read as the beginning", () => {
  for (const bad of [null, undefined, NaN, Infinity, "0.5", -0.2]) {
    const out = startCandidates(
      { progression: bad, locator: { href: "OTHER/x.xhtml", locations: {} } },
      view(),
    );
    assert.deepEqual(out, []);
  }
  // Zero is a real place: the top of the book.
  assert.deepEqual(
    startCandidates({ progression: 0, locator: { href: "OTHER/x.xhtml", locations: {} } }, view()),
    [{ fraction: 0 }],
  );
});

test("the source op is never modified", () => {
  const op = phoneOp();
  const before = JSON.stringify(op);
  startCandidates(op, view());
  assert.equal(JSON.stringify(op), before);
});

test("a CFI is the epubcfi fragment and only that", () => {
  assert.equal(cfiOf(phoneOp()), null);
  assert.equal(cfiOf({ locator: { locations: { fragments: ["page=3", "epubcfi(/6)"] } } }), "epubcfi(/6)");
  assert.equal(cfiOf({ locator: { locations: { fragments: "epubcfi(/6)" } } }), null);
  assert.equal(cfiOf(null), null);
});

test("nothing at all is an empty ladder, not a throw", () => {
  assert.deepEqual(startCandidates(null, view()), []);
  assert.deepEqual(startCandidates(phoneOp(), null), []);
  assert.deepEqual(startCandidates({}, view()), []);
});

test("an op the phone actually wrote climbs the ladder here", () => {
  // The literal shape the Android app emits with a passage anchor, from
  // ExactLocatorAnchor.mark(): the marker and selector live under
  // `locations`, the quote under `text`, and `position` is Readium's
  // synthetic count of pages through the whole book. This is the other
  // half of the interoperability pin — the app's own test suite pins a
  // locator this reader emits. Neither reader can verify the other's
  // anchor without the other's engine, so what both can check is that
  // the bytes one writes are the bytes the other reads.
  const fromPhone = JSON.parse(`{
    "op_id": "b7f1c0e2-0000-3000-8000-000000000001",
    "work_id": "w-moby",
    "client_ts": "2026-01-02T03:04:05Z",
    "progression": 0.31,
    "edition_sha": "${"a".repeat(64)}",
    "locator": {
      "href": "/OEBPS/ch1.xhtml",
      "type": "application/xhtml+xml",
      "locations": {
        "progression": 0.74,
        "totalProgression": 0.31,
        "position": 812,
        "liseurAnchor": 1,
        "cssSelector": "body > p:nth-of-type(3)"
      },
      "text": { "before": "and then ", "highlight": "Ishmael", "after": " said nothing" }
    }
  }`);

  const out = startCandidates(fromPhone, view({
    // The app writes the href Readium gave it, which carries a leading
    // slash this manifest does not use.
    resolveKey: (href) => (href === "/OEBPS/ch1.xhtml" ? "OEBPS/ch1.xhtml" : null),
  }));

  // No CFI: the phone does not write one, and the fraction must not be
  // the first thing tried.
  assert.equal(cfiOf(fromPhone), null);
  assert.equal(out[0].href, "OEBPS/ch1.xhtml");
  assert.equal(out[0].locations.progression, 0.74);
  // 812 is a page number in the phone's book, not in this list.
  assert.equal(out[0].locations.position, undefined);
  // The anchor is carried through untouched for the engine to walk.
  assert.equal(out[0].locations.cssSelector, "body > p:nth-of-type(3)");
  assert.equal(out[0].text.highlight, "Ishmael");
  // Then the fraction, then the bare chapter.
  assert.deepEqual(out[1], { fraction: 0.31 });
  assert.equal(out[2], "OEBPS/ch1.xhtml");
});

test("the same op from a different file keeps only its fraction", () => {
  const out = startCandidates(phoneOp({ edition_sha: "b".repeat(64) }), view());
  assert.deepEqual(out[0], { fraction: 0.31 });
});
