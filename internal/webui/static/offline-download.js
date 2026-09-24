import { readerAuth } from "./reader-auth.js";
import {
  activeAccount,
  accountContext,
  assertOfflineContext,
  deploymentPrefix,
  downloadPublication,
  getReadySnapshot,
  removeBookSnapshots,
  storagePartition,
  notifyOfflineChange,
} from "./offline-storage.js";

const buttons = [...document.querySelectorAll("[data-offline-book]")];
if (buttons.length) {
  const offlineCapable = globalThis.isSecureContext &&
    !!globalThis.navigator?.serviceWorker && !!globalThis.navigator?.locks;
  const prefix = deploymentPrefix();
  const csrf = document.querySelector('input[name="csrf"]')?.value || "";
  const auth = readerAuth({
    apiBase: prefix,
    tokenURL: prefix + "ui/reader/token",
    csrf,
    onExhausted: error => console.warn("offline download authentication ended", error),
  });
  let active = null;

  const statusFor = button => document.querySelector(
    `[data-offline-status="${CSS.escape(button.dataset.offlineBook)}"]`,
  );
  const say = (button, text, error = false) => {
    const status = statusFor(button);
    if (status) {
      status.textContent = text;
      status.classList.toggle("problem", error);
    }
  };

  // Every caller passes a guard, re-checked after the read: a state read
  // that settles after the user clicked again must not overwrite the
  // newer operation's label.
  const updateReadyState = async (button, account, guard) => {
    const snapshot = await getReadySnapshot({
      partition: storagePartition(),
      account,
      bookID: button.dataset.offlineBook,
      digest: button.dataset.offlineDigest,
    });
    if (!guard()) return false;
    button.textContent = snapshot ? "Remove offline copy" : "Save offline";
    button.dataset.offlineReady = snapshot ? "1" : "";
    say(button, snapshot ? "Saved offline" : "");
    return true;
  };
  // No operation newer than the caller is using this button.
  const buttonFree = button => () => !active || active.button !== button;
  // After a cancel or failure the button says what is actually stored,
  // never what the interrupted operation was expected to leave behind.
  const restore = async (button, account, message = null, error = false) => {
    const free = buttonFree(button);
    try {
      const reader = account || await activeAccount(storagePartition());
      if (reader) {
        if (!await updateReadyState(button, reader, free)) return;
      } else if (free()) {
        button.textContent = "Save offline";
        button.dataset.offlineReady = "";
      }
    } catch (stateError) {
      console.warn("offline download state could not be restored", stateError);
      if (!free()) return;
      button.textContent = "Save offline";
    }
    if (message !== null && free()) say(button, message, error);
  };

  for (const button of buttons) {
    if (!offlineCapable) {
      button.disabled = true;
      say(button, "Offline reading requires a secure browser connection.", true);
      continue;
    }
    activeAccount(storagePartition())
      .then(account => account && updateReadyState(button, account, buttonFree(button)))
      .catch(error => {
        console.warn("offline download state could not be loaded", error);
        // The local store is unreadable, so a download could not be
        // kept either. Say so rather than offering a button that lies.
        button.disabled = true;
        say(button, "Offline reading is unavailable in this browser.", true);
      });
    button.addEventListener("click", async () => {
      if (active) {
        // One download at a time. This click cancels the one in flight,
        // which may be another book's button; that operation puts its
        // own button back once it has stopped.
        const running = active;
        active = null;
        running.controller.abort();
        say(running.button, "Cancelling…");
        return;
      }
      button.disabled = false;
      const controller = new AbortController();
      const { signal } = controller;
      active = { controller, button };
      const owns = () => active !== null && active.controller === controller;
      let operationAccount = "";
      button.textContent = "Cancel download";
      say(button, "Preparing offline copy…");
      try {
        const identity = await auth.acquire();
        signal.throwIfAborted();
        operationAccount = identity.account;
        const partition = storagePartition();
        const context = await accountContext(partition, identity.account);
        signal.throwIfAborted();
        const authorize = async current => {
          await assertOfflineContext(context);
          if (current.account !== identity.account || current.device !== identity.device)
            throw new Error("The reading account or device changed.");
        };
        const existing = await getReadySnapshot({
          partition,
          account: identity.account,
          bookID: button.dataset.offlineBook,
          digest: button.dataset.offlineDigest,
        });
        signal.throwIfAborted();
        if (existing) {
          // Removal cannot be interrupted once it starts, so this is the
          // last point at which a cancel keeps the copy.
          await removeBookSnapshots({
            partition,
            account: identity.account,
            epoch: context.epoch,
            bookID: button.dataset.offlineBook,
          });
          notifyOfflineChange();
          if (owns()) {
            button.dataset.offlineReady = "";
            button.textContent = "Save offline";
            say(button, "Removed from this device");
          } else {
            await restore(button, identity.account, "Removed from this device");
          }
        } else {
          const resolve = await auth.request(
            "v1/books/" + encodeURIComponent(button.dataset.offlineBook) + "/resolve",
            {
              authorize,
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: "{}",
              signal,
            },
          );
          if (!resolve.ok || !auth.responseCurrent(resolve))
            throw new Error("The book could not be prepared for offline sync.");
          const resolved = await resolve.json();
          signal.throwIfAborted();
          if (!resolved.work_id)
            throw new Error("The book has no reading work to sync.");
          await downloadPublication({
            ...context,
            request: (path, options) => auth.request(path, { ...options, authorize }),
            current: response => auth.responseCurrent(response),
            partition,
            account: identity.account,
            bookID: button.dataset.offlineBook,
            workID: resolved.work_id,
            deviceID: identity.device || "",
            supportsActiveMs: identity.supportsActiveMs === true,
            expectedDigest: button.dataset.offlineDigest,
            signal,
            onProgress: ({ completed, total }) => {
              if (owns()) say(button, `Downloading ${completed}/${total}…`);
            },
          });
          notifyOfflineChange();
          if (owns()) await updateReadyState(button, identity.account, owns);
          else await restore(button, identity.account);
        }
      } catch (error) {
        // This operation is over; give the slot back before restoring so
        // the restore's own guard sees the button as free.
        const owned = owns();
        if (owned) active = null;
        if (error.name === "AbortError") {
          await restore(button, operationAccount, "Download cancelled");
        } else if (owned || buttonFree(button)()) {
          const message = error.code === "quota"
            ? error.message : error.message || "Offline download failed";
          await restore(button, operationAccount, message, true);
        } else {
          console.warn("superseded offline download failed", error);
        }
      } finally {
        // A download started after this one was cancelled owns the
        // slot now; only the operation that took it gives it back.
        if (owns()) active = null;
      }
    });
  }
}
