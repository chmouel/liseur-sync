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
      // showModal's own focusing lands on the first control, which in
      // these dialogs is the Close button. They were opened to fill a
      // form in; the native return of focus on close is already right.
      const field = dialog.querySelector('input:not([type="hidden"]), select, textarea');
      if (field) field.focus();
    }
  }

  document.addEventListener('click', function (e) {
    const trigger = e.target.closest && e.target.closest('[data-dialog-open]');
    if (!trigger) return;
    openDialog(trigger.dataset.dialogOpen);
  });

  if (dialogSupported) {
    document.querySelectorAll('dialog[data-auto-open]').forEach(function (dialog) {
      openDialog(dialog.id);
      if (window.history && window.history.replaceState && window.URL) {
        const url = new URL(window.location.href);
        if (url.searchParams.get('onboarding') === 'folder') {
          url.searchParams.delete('onboarding');
          window.history.replaceState({}, document.title, url.toString());
        }
      }
    });
  }

  // "/" puts the cursor in the search box.
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
    }
  });

  // Choosing a library, or a span on the insights page, goes there at once.
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

  // The worker is scoped to the data-free offline shelf only. The
  // runtime-relative base keeps registration under a reverse-proxy prefix.
  const pwaBase = document.body && document.body.dataset.pwaBase;
  if (pwaBase && navigator.storage?.persist) {
    navigator.storage.persist().catch(function () {});
  }
  if (pwaBase && 'serviceWorker' in navigator) {
    navigator.serviceWorker.register(pwaBase + 'sw.js', { scope: pwaBase })
      .catch(function (err) {
        console.warn('offline shelf could not be installed', err);
      });
  }

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
    const original = btn.dataset.copyLabel || btn.textContent;
    btn.dataset.copyLabel = original;
    const flash = function (text) {
      // One timer per button: a second click inside the window
      // restarts the countdown rather than racing the first one, and a
      // button an htmx swap has since replaced is left alone.
      clearTimeout(btn._copyTimer);
      btn.textContent = text;
      btn._copyTimer = setTimeout(function () {
        if (btn.isConnected) btn.textContent = original;
      }, 1500);
    };
    navigator.clipboard.writeText(target.textContent).then(function () {
      flash('Copied');
    }, function () {
      flash('Copy failed');
    });
  });

  // The settings form's "use the example prompt" button. The example
  // lives on the button because the page's policy is script-src 'self':
  // there is nowhere to write it inline, and it is the server's text
  // rather than this file's.
  document.addEventListener('click', function (e) {
    const btn = e.target.closest && e.target.closest('.prompt-example');
    if (!btn) return;
    const field = document.getElementById(btn.dataset.promptTarget);
    if (!field) return;
    field.value = btn.dataset.prompt || '';
    field.focus();
  });

  // hx-confirm only asks its question on requests htmx issues. On a
  // plain form — and every destructive form here is one — the
  // attribute asked nothing and the submit went straight through. Ask
  // it for them, and once a form is definitely going, take its
  // submitter away: a second click on a delete is a second delete.
  // htmx-managed forms are left to htmx, which asks and locks on its
  // own (hx-confirm, hx-disabled-elt).
  document.addEventListener('submit', function (e) {
    const form = e.target;
    if (!(form instanceof HTMLFormElement)) return;
    if (form.closest('[hx-post],[hx-get],[hx-put],[hx-patch],[hx-delete]')) return;
    const question = form.getAttribute('hx-confirm');
    if (question && !window.confirm(question)) {
      e.preventDefault();
      return;
    }
    // Defer past every other submit listener: one that vetoes (the
    // offline sign-out check can) must not leave a dead button behind.
    const submitter = e.submitter;
    setTimeout(function () {
      if (e.defaultPrevented) return;
      if (submitter) submitter.disabled = true;
      form.classList.add('working');
      if (form.classList.contains('upload')) {
        const status = form.querySelector('.uploadstatus');
        if (status) status.textContent = 'Sending. A large book can take a moment.';
      }
    }, 0);
  });

  // An htmx fragment request follows a redirect invisibly, so an
  // expired session would paste the sign-in page into whichever small
  // region asked. Take the whole page there instead.
  document.addEventListener('htmx:beforeSwap', function (e) {
    const url = e.detail && e.detail.xhr && e.detail.xhr.responseURL;
    if (!url) return;
    if (new URL(url).pathname.endsWith('/login')) {
      e.preventDefault();
      window.location.assign(url);
    }
  });

  // A fragment request that failed swaps nothing — correctly, the form
  // stays exactly as the reader left it — but without this they are
  // told nothing at all. One message, the same for everything that
  // asks, said where it cannot be missed.
  function htmxTrouble(e) {
    let region = document.getElementById('htmx-trouble');
    if (!region) {
      region = document.createElement('p');
      region.id = 'htmx-trouble';
      region.className = 'flash-error';
      region.setAttribute('role', 'alert');
      document.body.appendChild(region);
    }
    const status = e.detail && e.detail.xhr && e.detail.xhr.status;
    region.textContent = 'That change could not be made' +
      (status ? ' (the server answered ' + status + ')' : '') +
      '. Nothing on this page changed — please try again.';
    clearTimeout(region._troubleTimer);
    region._troubleTimer = setTimeout(function () { region.remove(); }, 8000);
  }
  document.addEventListener('htmx:responseError', htmxTrouble);
  document.addEventListener('htmx:sendError', htmxTrouble);

  // A scan holds its answer until the pass is done — up to two
  // minutes. The spinner is visual; this says the same fact to a
  // screen reader. The finishing word comes from the page the scan
  // redirects back to.
  document.addEventListener('htmx:beforeRequest', function (e) {
    const form = e.target.closest && e.target.closest('.scanform');
    if (!form) return;
    const say = form.querySelector('.scansay');
    if (say) say.textContent = 'Scanning. This can take a minute.';
  });

})();
