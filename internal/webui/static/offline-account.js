import {
  clearOfflineAccount,
  accountVersion,
  listOfflineOutbox,
  setActiveAccount,
  storagePartition,
  deploymentPrefix,
} from "./offline-storage.js";

const account = document.body?.dataset.offlineAccount;
const partition = storagePartition();
if (account) {
  accountVersion(partition).then(async version => {
    const response = await fetch(deploymentPrefix() + "ui/offline/account", {
      cache: "no-store", credentials: "same-origin",
    });
    if (response.ok && !response.redirected && (await response.json()).account === account) {
      await setActiveAccount(partition, account, version);
    }
  })
    .catch(error => console.warn("offline account marker could not be updated", error));
}

let signingOut = false;
document.addEventListener("submit", async event => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.action.endsWith("/logout") || signingOut)
    return;
  event.preventDefault();
  if (!account) {
    form.submit();
    return;
  }
  try {
    const pending = await listOfflineOutbox({ partition, account, state: null });
    if (pending.length && !window.confirm(
      "Offline changes are waiting to sync. Signing out will remove them from this device. Continue?",
    )) return;
    signingOut = true;
    await clearOfflineAccount(partition, account);
    form.submit();
  } catch (error) {
    console.warn("offline account data could not be cleared", error);
    signingOut = false;
    window.alert("Offline data could not be cleared. Please try signing out again.");
  }
});
