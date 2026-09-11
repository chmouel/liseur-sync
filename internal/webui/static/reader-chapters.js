// Chapter extraction and position calculation for the reader footer.
//
// Chapters are extracted from the EPUB's table of contents and mapped to
// Readium positions. The footer can show how many positions remain in the
// current chapter, matching the Android app's behavior (ADR-0029).

import { pageAt } from "./reader-positions.js";

/**
 * BookChapter represents a chapter with its position range.
 * @typedef {Object} BookChapter
 * @property {string|null} title - Chapter title, or null for unnamed chapters
 * @property {number} firstPosition - Starting Readium position (1-based)
 * @property {number} lastPosition - Ending Readium position (1-based)
 */

/**
 * buildChapters extracts chapters from the EPUB's table of contents and
 * maps them to Readium position ranges.
 *
 * The TOC is a nested structure where each item has label, href, and subitems.
 * A chapter is an entry with a label (named chapter) that references a resource.
 * Chapters are keyed by their resource href; multiple TOC entries pointing to
 * the same resource collapse into one chapter.
 *
 * A nameless chapter (one without a label or with only whitespace) is excluded,
 * matching the Android app logic.
 *
 * @param {Array} toc - The publication's table of contents from view.book.toc
 * @param {Array} sections - The publication's reading order from view.book.sections
 * @param {Object} table - The position table from positionTable(sections)
 * @returns {Array<BookChapter>} Array of chapters sorted by position, or [] if no chapters
 */
export function buildChapters(toc, sections, table) {
  if (!toc || !sections || !table) return [];

  const chapters = [];
  const seenHrefs = new Set();

  /**
   * Flatten the nested TOC tree and extract chapters.
   * We only include items that have both a label and an href, and we skip
   * duplicates (same href appears in multiple places in the TOC).
   */
  const flattenTOC = (items) => {
    if (!Array.isArray(items)) return;
    for (const item of items) {
      // Include only items with both a label (named chapter) and an href (resource reference)
      const label = (item.label || "").trim();
      if (label && item.href && !seenHrefs.has(item.href)) {
        seenHrefs.add(item.href);
        chapters.push({ label, href: item.href });
      }
      // Recurse into subitems
      if (item.subitems) flattenTOC(item.subitems);
    }
  };

  flattenTOC(toc);

  if (chapters.length === 0) return [];

  // Map chapters to position ranges.
  // Each chapter starts at the resource it references and ends just before
  // the next chapter (or at the end of the book).
  const bookChapters = [];
  for (let i = 0; i < chapters.length; i++) {
    const chapter = chapters[i];
    const sectionIndex = sections.findIndex((s) => s && s.id === chapter.href);
    if (sectionIndex < 0) continue; // Chapter's resource not in reading order, skip it

    // First position is the start of this chapter's resource
    const firstPosition = (table.starts[sectionIndex] || 0) + 1; // 1-based

    // Last position is just before the next chapter's resource, or the end of the book
    let lastPosition;
    if (i + 1 < chapters.length) {
      // Find the next chapter's starting position
      const nextChapterHref = chapters[i + 1].href;
      const nextSectionIndex = sections.findIndex((s) => s && s.id === nextChapterHref);
      if (nextSectionIndex > 0) {
        // Last position is the last position of the section before the next chapter
        lastPosition = (table.starts[nextSectionIndex] || 0);
      } else {
        // Next chapter's resource not found, go to end of book
        lastPosition = table.total;
      }
    } else {
      // This is the last chapter, goes to end of book
      lastPosition = table.total;
    }

    // Ensure lastPosition is valid (at least equal to firstPosition)
    lastPosition = Math.max(lastPosition, firstPosition);

    bookChapters.push({
      title: chapter.label,
      firstPosition,
      lastPosition,
    });
  }

  return bookChapters;
}

/**
 * pagesLeftInChapter calculates how many Readium positions remain in the
 * current chapter, or null if there is no chapter or the position is out of range.
 *
 * Following ADR-0029:
 * - A nameless chapter (title is null) counts as no chapter
 * - A position out of the chapter's range returns null
 * - Zero means the reader is on the last position of the chapter
 *
 * @param {BookChapter|null} chapter - The chapter containing the current position
 * @param {number} position - The current Readium position (1-based)
 * @returns {number|null} Remaining positions in chapter, or null
 */
export function pagesLeftInChapter(chapter, position) {
  if (!chapter) return null;
  if (!chapter.title) return null; // Nameless chapters don't count
  if (position < chapter.firstPosition || position > chapter.lastPosition) return null;
  return chapter.lastPosition - position;
}

/**
 * chapterAt finds the chapter containing the given Readium position.
 *
 * @param {Array<BookChapter>} chapters - Array of chapters
 * @param {number} position - The Readium position to look up (1-based)
 * @returns {BookChapter|null} The chapter at this position, or null
 */
export function chapterAt(chapters, position) {
  if (!Array.isArray(chapters) || !Number.isInteger(position)) return null;
  return chapters.find((ch) => position >= ch.firstPosition && position <= ch.lastPosition) || null;
}

/**
 * chapterForLocation finds the chapter for a location in the reader,
 * using the section index as a hint.
 *
 * When the resource (section) and position disagree about which chapter the
 * reader is in, we return null, matching the Android app (ADR-0029).
 *
 * @param {Array<BookChapter>} chapters - Array of chapters
 * @param {Array} sections - Reading order sections
 * @param {number|null} sectionIndex - Current section index
 * @param {number} position - Current Readium position
 * @returns {BookChapter|null} The chapter at this location
 */
export function chapterForLocation(chapters, sections, sectionIndex, position) {
  const positionChapter = chapterAt(chapters, position);

  if (sectionIndex !== null && sectionIndex !== undefined && Number.isInteger(sectionIndex)) {
    const section = sections && sections[sectionIndex];
    if (section && section.id) {
      const sectionChapter = chapters.find((ch) => ch && ch.href === section.id) || null;
      // If section and position agree, return it
      if (sectionChapter && sectionChapter === positionChapter) {
        return sectionChapter;
      }
      // If they disagree, report nothing to avoid false claims
      if (sectionChapter && !positionChapter) return null;
    }
  }

  return positionChapter;
}
