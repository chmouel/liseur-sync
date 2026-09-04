// The footer's page number, counted the way the Android app counts it.
//
// A page here is a Readium "position": a fixed slice of the book as it
// is stored, so the number does not move when the font does and — the
// point of this module — it is the same number on the phone and in the
// browser. Readium 3.3.0 computes it with
// `ReflowableStrategy.recommended`, which is
// `ArchiveEntryLength(pageLength = 1024)`: for each item of the reading
// order, `max(1, ceil(archiveEntryLength / 1024))` positions, summed.
// The archive entry length is the *stored* length — the compressed one
// for a deflated entry, the plain one for a stored entry — and falls
// back to the resource's own length when the container cannot say.
//
// The engine has its own answer to this question (`location.current` and
// `location.total`, slices of 1500 uncompressed bytes) and it is a
// perfectly honest measure of a book; it is simply a different unit, and
// a reader holding two devices does not want two units. The engine's
// numbers remain the fallback for a book this recipe cannot measure.
//
// Nothing here is sent anywhere. The progression fraction the reader
// syncs is the engine's, untouched.

/** Bytes per position, from Readium's recommended strategy. */
export const PAGE_LENGTH = 1024;

/**
 * positionTable counts the positions of each section of an open book and
 * where each section starts, from `view.book.sections`.
 *
 * Non-linear sections get none: Readium drops them from the reading
 * order before it counts anything, so a book's total must not include
 * them either. A book with nothing measurable in it returns null, which
 * is this module saying it has no opinion rather than an opinion of
 * zero.
 */
export function positionTable(sections) {
  if (!Array.isArray(sections) || sections.length === 0) return null;
  const counts = [];
  const starts = [];
  let total = 0;
  for (const section of sections) {
    starts.push(total);
    const count = positionCount(section);
    counts.push(count);
    total += count;
  }
  if (total <= 0) return null;
  return { counts, starts, total };
}

// positionCount is Readium's per-resource count for one section.
function positionCount(section) {
  if (!section || section.linear === "no") return 0;
  // The archive entry length, else the resource's own length — the same
  // fallback chain Readium walks. `compressedSize` is absent only for a
  // book that did not come out of an archive.
  const length = finite(section.compressedSize)
    ? section.compressedSize
    : finite(section.size)
      ? section.size
      : 0;
  if (length <= 0) return 1;
  return Math.max(1, Math.ceil(length / PAGE_LENGTH));
}

/**
 * pageAt is the 1-based page for a spot in the book, given the section
 * the reader is in and how far into it they are.
 *
 * Readium spreads a resource's positions evenly across it — position
 * `p` of a resource starts at `(p - 1) / positionCount` — so the
 * position containing a fraction is the inverse of that.
 *
 * A section with no positions of its own (a non-linear one, which the
 * reader can still reach by following a link into it) yields the page of
 * the boundary it sits on rather than a number outside the book.
 */
export function pageAt(table, index, fractionInSection) {
  if (!table) return null;
  const { counts, starts, total } = table;
  if (!finite(index) || index < 0 || index >= counts.length) return null;
  const count = counts[index];
  if (count <= 0) return clamp(starts[index], 1, total);
  const fraction = finite(fractionInSection)
    ? Math.min(Math.max(fractionInSection, 0), 1)
    : 0;
  // A fraction of exactly 1 is the end of the section, which is the last
  // of its positions and not the first of the next one's.
  const within = Math.min(Math.floor(fraction * count), count - 1);
  return clamp(starts[index] + within + 1, 1, total);
}

const finite = (v) => typeof v === "number" && Number.isFinite(v);

const clamp = (v, lo, hi) => Math.min(Math.max(v, lo), hi);
