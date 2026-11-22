// The library's CSV exports, as dialogs instead of downloads.
//
// A button is a real link to a real file, and this module is the
// enhancement: it intercepts the click, opens the dialog named on the
// button and puts that file's text in it, so the reader can look at what
// they are about to take, copy it straight onto the clipboard, or
// download it from the link in the dialog. Nothing here is required for
// an export to work — a browser without modules, or a dialog primitive,
// simply downloads the file the link named. The two exports share this
// module and every hook in it, which is why they are found by data
// attribute rather than by id.
//
// The text is fetched rather than rendered into the page: the shelf can
// be thousands of rows, and a reader who never opens the dialog should
// not be sent all of it inside the markup of every library page.

function dialogFor(id) {
  const dialog = document.getElementById(id);
  if (!dialog || typeof dialog.showModal !== "function") return null;
  return dialog;
}

async function load(dialog, href) {
  const text = dialog.querySelector("[data-csv-text]");
  const hint = dialog.querySelector("[data-csv-hint]");
  const copyBtn = dialog.querySelector("[data-csv-copy]");
  if (!text) return;
  text.value = "";
  if (copyBtn) copyBtn.disabled = true;
  try {
    const response = await fetch(href, {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "text/csv" },
    });
    if (response.redirected) {
      // The session ended underneath the shelf. A CSV full of a sign-in
      // page would be worse than saying so.
      window.location.assign(response.url);
      return;
    }
    if (!response.ok) throw new Error(String(response.status));
    text.value = await response.text();
    if (copyBtn) copyBtn.disabled = !text.value;
    if (hint) hint.textContent = countLabel(text.value);
  } catch {
    text.value = "";
    if (copyBtn) copyBtn.disabled = true;
    if (hint) hint.textContent = "The export could not be built. Try the download link.";
  }
}

// countLabel says how much is in the file, since the one thing a table
// of text does not tell you at a glance is whether it is the whole shelf.
// It counts records, not newlines: a quoted field may hold one, and a
// book whose title did should not be counted twice.
function countLabel(csv) {
  let records = 0;
  let quoted = false;
  let lineHasContent = false;
  for (let i = 0; i < csv.length; i += 1) {
    const ch = csv[i];
    if (quoted) {
      if (ch === '"') {
        // A doubled quote inside a quoted field is the escape for one.
        if (csv[i + 1] === '"') i += 1;
        else quoted = false;
      }
      continue;
    }
    if (ch === '"') {
      quoted = true;
      lineHasContent = true;
      continue;
    }
    if (ch === "\n") {
      records += 1;
      lineHasContent = false;
      continue;
    }
    if (ch !== "\r") lineHasContent = true;
  }
  if (lineHasContent) records += 1;
  const books = Math.max(records - 1, 0);
  return books === 1 ? "1 book." : `${books} books.`;
}

async function copy(dialog) {
  const text = dialog.querySelector("[data-csv-text]");
  const status = dialog.querySelector("[data-csv-status]");
  if (!text || !text.value) return;
  let copied = true;
  try {
    await navigator.clipboard.writeText(text.value);
  } catch {
    // Clipboard access needs a secure context and a permission; the
    // selection underneath the copy still works without either.
    text.removeAttribute("readonly");
    text.select();
    copied = document.execCommand("copy");
    text.setAttribute("readonly", "");
    text.setSelectionRange(0, 0);
    text.blur();
  }
  if (status) status.textContent = copied ? "Copied to the clipboard." : "Could not copy.";
}

document.addEventListener("click", event => {
  const trigger = event.target.closest?.("[data-csv-open]");
  if (trigger) {
    const dialog = dialogFor(trigger.dataset.csvOpen);
    // No dialog here: the link does what it says and the file downloads.
    if (!dialog) return;
    event.preventDefault();
    if (!dialog.open) dialog.showModal();
    const status = dialog.querySelector("[data-csv-status]");
    if (status) status.textContent = "";
    void load(dialog, trigger.href);
    return;
  }
  const copyButton = event.target.closest?.("[data-csv-copy]");
  if (copyButton) {
    event.preventDefault();
    void copy(copyButton.closest("dialog"));
  }
});
