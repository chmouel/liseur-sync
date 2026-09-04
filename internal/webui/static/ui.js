// The web UI's only script. It is a separate file rather than a tag in
// the page so that /ui can carry a Content-Security-Policy with no
// 'unsafe-inline' anywhere: everything that executes here is served
// from this origin, and a description or a title that manages to smuggle
// markup past the sanitizer still cannot run.
//
// It is also, deliberately, very little. Every page works without it —
// the shortcuts are shortcuts, the library picker keeps its submit
// button behind <noscript>, and the card menus are <details> elements
// that open on their own. This file only makes those things nicer.

(function () {
  'use strict';

  // Native dialogs provide focus trapping and Escape handling without
  // adding another UI dependency. Keep the inline form visible when a
  // browser cannot provide that primitive.
  const dialogSupported = typeof HTMLDialogElement !== 'undefined' &&
    typeof HTMLDialogElement.prototype.showModal === 'function';
  if (dialogSupported) document.documentElement.classList.add('dialog-supported');

  function openDialog(id) {
    const dialog = document.getElementById(id);
    if (!dialog) return;
    if (typeof dialog.showModal === 'function') {
      if (!dialog.open) dialog.showModal();
    }
  }

  document.addEventListener('click', function (e) {
    const trigger = e.target.closest && e.target.closest('[data-dialog-open]');
    if (!trigger) return;
    openDialog(trigger.dataset.dialogOpen);
  });

  document.querySelectorAll('dialog[data-auto-open]').forEach(function (dialog) {
    openDialog(dialog.id);
    if (window.history && window.history.replaceState) {
      const url = new URL(window.location.href);
      if (url.searchParams.get('onboarding') === 'folder') {
        url.searchParams.delete('onboarding');
        window.history.replaceState({}, document.title, url);
      }
    }
  });

  // "/" puts the cursor in the search box, Escape closes the mobile nav.
  document.addEventListener('keydown', function (e) {
    const active = document.activeElement;
    const tag = active && active.tagName;
    if (e.key === '/' && tag !== 'INPUT' && tag !== 'TEXTAREA' &&
      !(active && active.isContentEditable)) {
      const input = document.querySelector('header form input[name="q"]');
      if (input) {
        e.preventDefault();
        input.focus();
      }
      return;
    }
    if (e.key === 'Escape') {
      const toggle = document.getElementById('nav-toggle');
      if (toggle && toggle.checked) toggle.checked = false;
    }
  });

  // Choosing a library, or a span on the dashboard, goes there at once.
  // Without this the forms still work — their <noscript> buttons submit
  // them — so this is the same behaviour the onchange attribute used to
  // give, minus the attribute the CSP now refuses.
  const goOnChange = ['folder-pick', 'span-pick'];
  document.addEventListener('change', function (e) {
    const select = e.target;
    if (!select || !select.form) return;
    if (goOnChange.indexOf(select.id) !== -1) {
      select.form.submit();
      return;
    }
    if (select.id === 'group-series-toggle') {
      select.form.requestSubmit();
    }
  });

  // The heatmap opens at its far end: the reader came to see how this
  // week went, not how last January did.
  document.querySelectorAll('[data-scroll-end]').forEach(function (el) {
    el.scrollLeft = el.scrollWidth;
  });

  // A shown-once secret (token, pairing code, capability URL) is
  // otherwise only recoverable by drag-selecting text that a long
  // random string forces to wrap (word-break: break-all), which is
  // exactly how a stray space or newline gets included in what a user
  // pastes elsewhere. This copies the exact text content instead.
  document.addEventListener('click', function (e) {
    const btn = e.target.closest && e.target.closest('.copy-btn');
    if (!btn) return;
    const target = document.getElementById(btn.dataset.copyFor);
    if (!target || !navigator.clipboard) return;
    const original = btn.textContent;
    navigator.clipboard.writeText(target.textContent).then(function () {
      btn.textContent = 'Copied';
      setTimeout(function () { btn.textContent = original; }, 1500);
    });
  });

})();
