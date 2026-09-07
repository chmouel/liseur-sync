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

  const updateReadyState = async (button, account) => {
    const snapshot = await getReadySnapshot({
      partition: storagePartition(),
      account,
      bookID: button.dataset.offlineBook,
      digest: button.dataset.offlineDigest,
    });
    button.textContent = snapshot ? "Remove offline copy" : "Save offline";
    button.dataset.offlineReady = snapshot ? "1" : "";
    say(button, snapshot ? "Saved offline" : "");
  };

  for (const button of buttons) {
    activeAccount(storagePartition())
      .then(account => account && updateReadyState(button, account))
      .catch(error => console.warn("offline download state could not be loaded", error));
    button.addEventListener("click", async () => {
      if (active) {
        active.abort();
        return;
      }
      button.disabled = false;
      const controller = new AbortController();
      active = controller;
      let operationAccount = "";
      button.textContent = "Cancel download";
      say(button, "Preparing offline copy…");
      try {
        const identity = await auth.acquire();
        operationAccount = identity.account;
        const partition = storagePartition();
        const context = await accountContext(partition, identity.account);
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
        if (existing) {
          await removeBookSnapshots({
            partition,
            account: identity.account,
            bookID: button.dataset.offlineBook,
          });
          button.dataset.offlineReady = "";
          button.textContent = "Save offline";
          say(button, "Removed from this device");
          notifyOfflineChange();
        } else {
          const resolve = await auth.request(
            "v1/books/" + encodeURIComponent(button.dataset.offlineBook) + "/resolve",
            {
              authorize,
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: "{}",
            },
          );
          if (!resolve.ok || !auth.responseCurrent(resolve))
            throw new Error("The book could not be prepared for offline sync.");
          const resolved = await resolve.json();
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
            signal: controller.signal,
            onProgress: ({ completed, total }) => say(button, `Downloading ${completed}/${total}…`),
          });
          await updateReadyState(button, identity.account);
          notifyOfflineChange();
        }
      } catch (error) {
        if (error.name === "AbortError") say(button, "Download cancelled");
        else {
          const message = error.code === "quota"
            ? error.message : error.message || "Offline download failed";
          let restored = false;
          if (operationAccount) {
            try {
              await updateReadyState(button, operationAccount);
              restored = true;
            } catch (stateError) {
              console.warn("offline download state could not be restored", stateError);
            }
          }
          if (!restored) button.textContent = "Save offline";
          say(button, message, true);
        }
      } finally {
        active = null;
      }
    });
  }
}
