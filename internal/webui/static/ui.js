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

  // Multi-file drag and drop upload coordinator for library upload dialog.
  (function initUploadDialog() {
    const dialog = document.getElementById('upload-dialog');
    const form = document.getElementById('upload-dialog-form');
    const dropzone = document.getElementById('upload-dropzone');
    const fileInput = document.getElementById('upload-dialog-file');
    const queue = document.getElementById('upload-queue');
    const queueList = document.getElementById('upload-queue-list');
    const queueCount = document.getElementById('upload-queue-count');
    const clearBtn = document.getElementById('upload-queue-clear');
    const submitBtn = document.getElementById('upload-dialog-submit');
    const statusSpan = form ? form.querySelector('.uploadstatus') : null;

    if (!dialog || !form || !dropzone || !fileInput) return;

    let queuedFiles = [];
    let isUploading = false;

    function formatBytes(bytes) {
      if (!bytes || bytes <= 0) return '0 B';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / 1048576).toFixed(1) + ' MB';
    }

    function updateQueueUI() {
      if (queuedFiles.length === 0) {
        if (queue) queue.hidden = true;
        if (queueList) queueList.innerHTML = '';
        if (submitBtn) {
          submitBtn.textContent = 'Send a book';
          submitBtn.disabled = false;
        }
        fileInput.value = '';
        return;
      }

      if (queue) queue.hidden = false;
      if (queueCount) {
        queueCount.textContent = queuedFiles.length === 1
          ? '1 book selected'
          : queuedFiles.length + ' books selected';
      }

      if (submitBtn && !isUploading) {
        submitBtn.textContent = queuedFiles.length === 1
          ? 'Upload 1 book'
          : 'Upload ' + queuedFiles.length + ' books';
        submitBtn.disabled = false;
      }

      if (queueList) {
        queueList.innerHTML = '';
        queuedFiles.forEach(function (item, index) {
          const li = document.createElement('li');
          li.className = 'upload-queue-item' + (item.status ? ' ' + item.status : '');

          const nameSpan = document.createElement('span');
          nameSpan.className = 'upload-item-name';
          nameSpan.textContent = item.file.name;
          nameSpan.title = item.file.name;

          const sizeSpan = document.createElement('span');
          sizeSpan.className = 'upload-item-size muted';
          sizeSpan.textContent = formatBytes(item.file.size);

          const statusBadge = document.createElement('span');
          statusBadge.className = 'upload-item-badge';
          if (item.status === 'uploading') {
            statusBadge.textContent = 'Uploading…';
          } else if (item.status === 'done') {
            statusBadge.textContent = item.duplicate ? 'Duplicate' : 'Added';
          } else if (item.status === 'error') {
            statusBadge.textContent = item.error || 'Failed';
          } else {
            statusBadge.textContent = 'Ready';
          }

          li.appendChild(nameSpan);
          li.appendChild(sizeSpan);
          li.appendChild(statusBadge);

          if (!isUploading && item.status !== 'done') {
            const removeBtn = document.createElement('button');
            removeBtn.type = 'button';
            removeBtn.className = 'upload-item-remove';
            removeBtn.setAttribute('aria-label', 'Remove ' + item.file.name);
            removeBtn.textContent = '×';
            removeBtn.addEventListener('click', function (e) {
              e.stopPropagation();
              e.preventDefault();
              queuedFiles.splice(index, 1);
              updateQueueUI();
            });
            li.appendChild(removeBtn);
          }

          queueList.appendChild(li);
        });
      }
    }

    function addFiles(files) {
      if (!files || files.length === 0) return;
      for (let i = 0; i < files.length; i++) {
        const file = files[i];
        const exists = queuedFiles.some(function (q) {
          return q.file.name === file.name && q.file.size === file.size;
        });
        if (!exists) {
          queuedFiles.push({ file: file, status: 'ready' });
        }
      }
      updateQueueUI();
    }

    ['dragenter', 'dragover'].forEach(function (eventName) {
      dropzone.addEventListener(eventName, function (e) {
        e.preventDefault();
        e.stopPropagation();
        dropzone.classList.add('dragover');
      });
    });

    ['dragleave', 'dragend', 'drop'].forEach(function (eventName) {
      dropzone.addEventListener(eventName, function (e) {
        e.preventDefault();
        e.stopPropagation();
        dropzone.classList.remove('dragover');
      });
    });

    dropzone.addEventListener('drop', function (e) {
      if (e.dataTransfer && e.dataTransfer.files) {
        addFiles(e.dataTransfer.files);
      }
    });

    fileInput.addEventListener('change', function () {
      if (fileInput.files) {
        addFiles(fileInput.files);
      }
    });

    if (clearBtn) {
      clearBtn.addEventListener('click', function (e) {
        e.preventDefault();
        if (isUploading) return;
        queuedFiles = [];
        if (statusSpan) statusSpan.textContent = '';
        updateQueueUI();
      });
    }

    form.addEventListener('submit', async function (e) {
      if (queuedFiles.length === 0) {
        if (fileInput.files && fileInput.files.length > 0) {
          addFiles(fileInput.files);
        } else {
          e.preventDefault();
          return;
        }
      }

      e.preventDefault();
      e.stopPropagation();
      if (isUploading) return;
      isUploading = true;
      if (submitBtn) submitBtn.disabled = true;

      const uploadURL = form.action;
      let successCount = 0;
      let errorCount = 0;

      for (let i = 0; i < queuedFiles.length; i++) {
        const item = queuedFiles[i];
        if (item.status === 'done') {
          successCount++;
          continue;
        }

        item.status = 'uploading';
        updateQueueUI();
        if (statusSpan) {
          statusSpan.textContent = 'Uploading ' + (i + 1) + ' of ' + queuedFiles.length + '…';
        }

        try {
          const fd = new FormData();
          fd.append('file', item.file, item.file.name);
          const resp = await fetch(uploadURL, {
            method: 'POST',
            body: fd,
            headers: { 'Accept': 'application/json' },
          });

          if (resp.ok) {
            const data = await resp.json().catch(function () { return {}; });
            item.status = 'done';
            item.duplicate = !!data.duplicate;
            successCount++;
          } else {
            const data = await resp.json().catch(function () { return {}; });
            item.status = 'error';
            item.error = data.error || 'Rejected by server';
            errorCount++;
          }
        } catch (err) {
          item.status = 'error';
          item.error = 'Network error';
          errorCount++;
        }

        updateQueueUI();
      }

      isUploading = false;
      if (statusSpan) {
        if (errorCount === 0) {
          statusSpan.textContent = 'Finished! Added ' + successCount + ' book' + (successCount === 1 ? '' : 's') + '.';
        } else {
          statusSpan.textContent = 'Uploaded ' + successCount + ', ' + errorCount + ' failed.';
        }
      }

      if (submitBtn) {
        submitBtn.textContent = 'Done';
        submitBtn.disabled = false;
      }

      if (successCount > 0) {
        setTimeout(function () {
          window.location.reload();
        }, 1200);
      }
    });
  })();

})();
