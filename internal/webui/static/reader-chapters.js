// Chapter ranges for the reader footer.
//
// The resource-to-chapter rules mirror the Android reader: a named resource
// starts a chapter, while an unnamed resource continues the previous one.

const VIRTUAL_ORIGIN = "https://publication.invalid/";

/**
 * @typedef {Object} BookChapter
 * @property {string|null} title
 * @property {number} firstPosition
 * @property {number} lastPosition
 */

function archiveKey(reference, base = "") {
  if (typeof reference !== "string" || !reference.trim()) return null;
  if (/^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(reference.trim())) return null;
  if (/%(?:2f|5c)/i.test(reference)) return null;
  try {
    const url = new URL(reference, new URL(base || ".", new URL(VIRTUAL_ORIGIN)));
    if (url.origin !== VIRTUAL_ORIGIN.slice(0, -1) || url.username || url.password) return null;
    const path = decodeURIComponent(url.pathname).replace(/^\/+/, "");
    if (!path || path.split("/").some(part => part === "..")) return null;
    const parts = [];
    for (const part of path.split("/")) {
      if (!part || part === ".") continue;
      if (part === "..") {
        if (!parts.length) return null;
        parts.pop();
      } else {
        parts.push(part);
      }
    }
    return parts.join("/") || null;
  } catch {
    return null;
  }
}

function resourceIndexByHref(sections) {
  const indexes = new Map();
  sections.forEach((section, index) => {
    const key = archiveKey(section?.id);
    if (key) indexes.set(key, index);
  });
  return indexes;
}

function tocItems(items, sections, packageHref) {
  const indexes = resourceIndexByHref(sections);
  const packageBase = packageHref ? packageHref.slice(0, packageHref.lastIndexOf("/") + 1) : "";
  const titles = new Map();
  const visit = list => {
    if (!Array.isArray(list)) return;
    for (const item of list) {
      const label = typeof item?.label === "string" ? item.label.trim() : "";
      if (label && item.href) {
        const direct = archiveKey(item.href);
        const relative = archiveKey(item.href, packageBase);
        const index = indexes.has(direct) ? indexes.get(direct) : indexes.get(relative);
        if (index !== undefined && !titles.has(index)) titles.set(index, label);
      }
      visit(item?.subitems);
    }
  };
  visit(items);
  return titles;
}

function usableTable(table, sections) {
  if (!table || !Array.isArray(table.counts) || !Array.isArray(table.starts) ||
      table.counts.length !== sections.length || table.starts.length !== sections.length ||
      !Number.isFinite(table.total) || table.total < 0) return false;
  let total = 0;
  for (let i = 0; i < sections.length; i++) {
    if (!Number.isFinite(table.counts[i]) || table.counts[i] < 0 ||
        !Number.isFinite(table.starts[i]) || table.starts[i] < 0 ||
        table.starts[i] !== total) return false;
    total += table.counts[i];
  }
  return total === table.total;
}

/**
 * @returns {{chapters: Array<BookChapter>, chapterIndexByResource: Map<number, number>}}
 */
export function buildChapters(toc, sections, table, packageHref = "") {
  if (!Array.isArray(sections) || !usableTable(table, sections)) {
    return { chapters: [], chapterIndexByResource: new Map() };
  }
  const titles = tocItems(toc, sections, packageHref);
  const chapters = [];
  const chapterIndexByResource = new Map();
  for (let index = 0; index < sections.length; index++) {
    const count = table.counts[index];
    if (count <= 0) continue;
    const firstPosition = table.starts[index] + 1;
    const lastPosition = table.starts[index] + count;
    const title = titles.get(index) ?? sections[index]?.title ?? null;
    const previous = chapters.at(-1);
    if (title === null && previous) {
      previous.lastPosition = lastPosition;
    } else {
      chapters.push({ title, firstPosition, lastPosition, href: sections[index]?.id || null });
    }
    chapterIndexByResource.set(index, chapters.length - 1);
  }
  return { chapters, chapterIndexByResource };
}

export function pagesLeftInChapter(chapter, position) {
  if (!chapter || !chapter.title) return null;
  if (position < chapter.firstPosition || position > chapter.lastPosition) return null;
  return chapter.lastPosition - position;
}

export function chapterAt(chapters, position) {
  if (!Array.isArray(chapters) || !Number.isInteger(position)) return null;
  return chapters.find(ch => position >= ch.firstPosition && position <= ch.lastPosition) || null;
}

export function chapterForLocation(chapters, chapterIndexByResource, sectionIndex, position) {
  const positionChapter = chapterAt(chapters, position);
  const resourceChapter = Number.isInteger(sectionIndex)
    ? chapters[chapterIndexByResource?.get(sectionIndex)]
    : null;
  return resourceChapter && position >= resourceChapter.firstPosition &&
    position <= resourceChapter.lastPosition ? resourceChapter : positionChapter;
}

export { archiveKey };
