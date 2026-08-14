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

  // Choosing a library goes there at once. Without this the form still
  // works — the <noscript> button submits it — so this is the same
  // behaviour the onchange attribute used to give, minus the attribute
  // the CSP now refuses.
  document.addEventListener('change', function (e) {
    const select = e.target;
    if (select && select.id === 'library-pick' && select.form) {
      select.form.submit();
    }
  });
})();
