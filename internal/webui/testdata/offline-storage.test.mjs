import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPublicationPlan,
  localPublicationRequest,
  snapshotKey,
  storagePartition,
} from "../static/offline-storage.js";

const root = "https://reader.example/sync/";
const manifestURL = `${root}v1/books/book-1/publication/manifest.json`;
const resource = (name) => `${root}v1/books/book-1/publication/abc123/resources/${name}`;

test("publication plans keep one digest-pinned, archive-local graph", () => {
  const manifest = {
    links: [
      { rel: ["self"], href: manifestURL },
      { rel: ["http://readium.org/position-list"], href: `${root}v1/books/book-1/publication/abc123/positions.json` },
      { rel: ["package"], href: resource("OEBPS/package.opf"), type: "application/oebps-package+xml" },
    ],
    readingOrder: [{ href: resource("OEBPS/chapter.xhtml"), type: "application/xhtml+xml" }],
    resources: [{ href: resource("OEBPS/style.css"), type: "text/css" }],
  };
  const plan = buildPublicationPlan(manifest, { manifestURL, expectedDigest: "abc123" });
  assert.equal(plan.digest, "abc123");
  assert.equal(plan.packageHref, "OEBPS/package.opf");
  assert.deepEqual([...plan.resources.keys()].sort(), [
    "OEBPS/chapter.xhtml",
    "OEBPS/package.opf",
    "OEBPS/style.css",
  ]);
});

test("publication plans reject a changed digest and non-local resources", () => {
  const manifest = {
    links: [
      { rel: ["http://readium.org/position-list"], href: `${root}v1/books/book-1/publication/abc123/positions.json` },
      { rel: ["package"], href: resource("OEBPS/package.opf") },
    ],
    readingOrder: [{ href: resource("OEBPS/chapter.xhtml") }],
  };
  assert.throws(
    () => buildPublicationPlan(manifest, { manifestURL, expectedDigest: "other" }),
    /changed/i,
  );
  assert.throws(
    () => buildPublicationPlan({
      ...manifest,
      readingOrder: [{ href: "https://publisher.example/chapter.xhtml" }],
    }, { manifestURL, expectedDigest: "abc123" }),
    /non-local|invalid/i,
  );
});

test("local publication requests serve digest-pinned positions", async () => {
  const positions = { positions: [{ href: "OEBPS/chapter.xhtml" }] };
  const response = await localPublicationRequest(
    { positions },
    new Map(),
    "/v1/books/book-1/publication/abc123/positions.json",
  );
  assert.equal(response.ok, true);
  assert.deepEqual(await response.json(), positions);
});

test("private snapshot identity includes deployment, account, book, digest and generation", () => {
  const key = snapshotKey({
    partition: "https://reader.example/sync/",
    account: "account-a",
    bookID: "book-1",
    digest: "abc123",
    generation: "generation-1",
  });
  assert.match(key, /reader\.example\/sync/);
  assert.match(key, /account-a/);
  assert.match(key, /book-1/);
  assert.match(key, /abc123/);
  assert.match(key, /generation-1/);
  assert.equal(storagePartition(new URL("https://reader.example/sync/ui/offline/")), "https://reader.example/sync/");
});
