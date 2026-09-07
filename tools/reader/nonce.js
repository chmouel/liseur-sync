// Only trusted Readium injectables receive the shell's CSP nonce.
export let scriptNonce = "";
export function setScriptNonce(value) { scriptNonce = value; }
