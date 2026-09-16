// The prompt a reader copies for a passage they have highlighted.
//
// The template is the account's own text, written on the settings page
// and never interpreted by the server. This module is the whole of what
// "interpreting" it means: substituting a fixed set of names, and
// leaving everything else exactly as it was typed.

// PROMPT_PLACEHOLDERS is every name a template may use. It is exported
// so that nothing has to keep a second copy of the list in order to
// gather the values.
export const PROMPT_PLACEHOLDERS = [
  "title", "author", "series", "chapter", "page", "pages", "percent", "text",
];

/**
 * fillPrompt substitutes {name} for the value under that name.
 *
 * Two rules, both of them about not surprising somebody who is writing
 * prose rather than code:
 *
 * - A name this module does not know is left alone, braces and all. A
 *   prompt may legitimately contain "{}" or "{like this}", and a
 *   template is not a program that should fail over it.
 * - A name it knows but cannot answer becomes an empty string. A book
 *   with no series is a book with no series; it is not a book whose
 *   prompt says "undefined".
 */
export function fillPrompt(template, values) {
  if (typeof template !== "string" || !template) return "";
  const known = new Set(PROMPT_PLACEHOLDERS);
  return template.replace(/\{(\w+)\}/g, (whole, name) => {
    if (!known.has(name)) return whole;
    const value = values ? values[name] : undefined;
    return value === undefined || value === null ? "" : String(value);
  });
}
