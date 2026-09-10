// Where to reopen a book, given what some client wrote down about where
// its reader was.
//
// The clients do not agree on much. This reader records a CFI and a
// spine index; the Android app records a resource, a fraction of that
// resource, and sometimes a quote of the passage on screen. The one
// thing everything writes is a fraction of the whole book — and that is
// the least useful of them, because the two clients do not compute it
// the same way. This one interpolates the server's position list by
// byte weight; the app interpolates Readium's synthetic positions. On a
// book whose chapters are evenly sized the two agree to within a
// rounding error. On a book with a few large chapters — the ordinary
// shape of an EPUB 2 — they do not, and a page turned on the phone
// reopens here in the wrong part of the wrong chapter.
//
// So the fraction is the last thing tried, never the first. Above it
// sits the resource the writing client actually named, which both
// clients spell from the same manifest and which lands in the right
// chapter whoever wrote it.
//
// Nothing here touches the DOM or the network: it is given a small view
// of the open publication and returns a ladder for the caller to
// descend.

const finite = (value) => typeof value === "number" && Number.isFinite(value);

// agreedEdition is the digest of the bytes on screen, but only when the
// publication that was opened and the catalog record for it say the same
// thing. They can disagree: the file may be replaced between the
// manifest request and the catalog lookup. A position filed under the
// wrong edition is worse than one filed under none, because the other
// client will trust it and go somewhere the reader never was.
export function agreedEdition(opened, catalog) {
  return opened && catalog && opened === catalog ? opened : "";
}

// sameEdition answers whether a locator written elsewhere describes the
// bytes open here. An op that names no edition is treated as eligible:
// that is what every op said before edition digests were written down,
// and refusing them all would make this reader worse at restoring old
// positions than it was. Not knowing which bytes are open has the same
// answer for the same reason.
export function sameEdition(named, opened) {
  if (!named || !opened) return true;
  return named === opened;
}

// sectionHref maps a foreign locator's href onto this publication's own
// spelling, or null when this book has no such resource. Another client
// writes the path its own toolkit gave it: the same resource can arrive
// percent-encoded differently, or with a leading slash this manifest
// does not use. Comparing the raw strings would reject a chapter that
// is plainly here and drop the reader to a percentage instead, which is
// the failure this whole module exists to avoid.
export function sectionHrefIn(href, sections, resolveKey) {
  if (typeof href !== "string" || !href) return null;
  const path = href.split("#")[0].split("?")[0];
  const ids = new Set((sections || []).map((section) => section && section.id));
  const resolve = typeof resolveKey === "function" ? resolveKey : () => null;
  const has = (value) => (value && ids.has(value) ? value : null);
  const bare = path.replace(/^\/+/, "");
  return (
    has(path) ??
    has(resolve(path)) ??
    has(bare) ??
    has(resolve(bare))
  );
}

// startCandidates lists where to try opening, best pointer first.
//
// A stored pointer is only offered after the spine step resolves against
// *this* copy of the book. That is all this can check: a CFI's path
// inside the chapter, and a quote of the passage, are both walked lazily
// once the chapter loads, and either can still fail there against a
// pointer from another engine. That is why this returns a ladder for the
// caller to descend rather than a single answer.
//
// `view` supplies: editionSHA (the agreed digest, or ""), sections (the
// reading order), resolveKey (equivalent-spelling lookup), cfiResolves
// (whether a CFI names a chapter here).
export function startCandidates(op, view) {
  if (!op || !view) return [];
  const out = [];
  const locations = (op.locator && op.locator.locations) || {};
  const named = typeof op.edition_sha === "string" ? op.edition_sha : "";
  const href = sectionHrefIn(
    op.locator && op.locator.href,
    view.sections,
    view.resolveKey,
  );
  // A pointer inside a chapter only means anything in the bytes it was
  // written against. When the op names a different edition than the one
  // open here, its chapter and its offset describe someone else's file,
  // and only the fraction survives — every client agrees that is a
  // fraction of whatever it happens to be reading.
  if (sameEdition(named, view.editionSHA)) {
    const cfi = cfiOf(op);
    if (cfi && view.cfiResolves(cfi)) out.push(cfi);
    if (href) {
      const locator = structuredClone(op.locator);
      locator.href = href;
      locator.locations = { ...locations };
      // This reader writes `position` as the spine index plus one, and
      // relies on it to reopen a page it saved with no CFI. The app on
      // a phone writes Readium's synthetic position into the same field
      // — a count of pages through the whole book, which indexes into
      // somewhere else entirely here. The field is kept only while it
      // still says what this reader means by it, which a foreign one
      // will not. The resource and the progression within it are what
      // travel between clients.
      const spine = (view.sections || []).findIndex((s) => s && s.id === href);
      if (locator.locations.position !== spine + 1) delete locator.locations.position;
      out.push(locator);
    }
  }
  const fraction = finite(locations.totalProgression)
    ? locations.totalProgression
    : op.progression;
  if (finite(fraction) && fraction >= 0) {
    out.push({ fraction: Math.min(0.999, fraction) });
  }
  // The chapter with nothing said about where in it, which is still the
  // right chapter. Offered even for a foreign edition's spelling if this
  // book happens to have that resource, because by now the alternative
  // is opening at page one.
  if (href) out.push(href);
  return out;
}

export function cfiOf(op) {
  const fragments =
    (op && op.locator && op.locator.locations && op.locator.locations.fragments) || [];
  if (!Array.isArray(fragments)) return null;
  for (const fragment of fragments) {
    if (typeof fragment === "string" && fragment.indexOf("epubcfi(") === 0) return fragment;
  }
  return null;
}
