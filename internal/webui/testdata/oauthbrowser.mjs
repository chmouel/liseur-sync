import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const profile = mkdtempSync(join(tmpdir(), 'oauth-browser-'));
const chrome = spawn(process.env.OAUTH_CHROME, [
  '--headless=new', '--disable-gpu', '--no-sandbox', '--ignore-certificate-errors',
  '--no-proxy-server', '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  '--host-resolver-rules=MAP issuer.test 127.0.0.1, MAP xn--bcher-kva.test 127.0.0.1',
  'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });

let ws;
let watchdog;
async function run() {
  const endpoint = await new Promise((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => reject(new Error('Chrome did not start')), 15000);
    chrome.stderr.on('data', chunk => {
      output += chunk;
      const match = output.match(/ws:\/\/[^\s]+/);
      if (match) { clearTimeout(timer); resolve(match[0]); }
    });
    chrome.once('error', reject);
  });
  ws = new WebSocket(endpoint);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  let nextID = 0;
  let dialogs = 0;
  const pending = new Map();
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Page.javascriptDialogOpening') {
      dialogs++;
      void send('Page.handleJavaScriptDialog', { accept: dialogs === 2 }, message.sessionId);
      return;
    }
    if (!pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    message.error ? reject(new Error(JSON.stringify(message.error))) : resolve(message.result);
  });
  function send(method, params = {}, sessionId) {
    const id = ++nextID;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params, sessionId }));
    });
  }
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const call = (method, params) => send(method, params, sessionId);
  await call('Page.enable', {});
  await call('Runtime.enable', {});
  async function evaluate(expression) {
    const result = await call('Runtime.evaluate', { expression, returnByValue: true, userGesture: true });
    if (result.exceptionDetails) throw new Error('Browser evaluation failed');
    return result.result.value;
  }
  async function waitFor(expression, label) {
    const deadline = Date.now() + 15000;
    while (Date.now() < deadline) {
      try { if (await evaluate(expression)) return; } catch {}
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    const page = await evaluate('({url: location.origin + location.pathname, text: document.body?.innerText.slice(0, 500)})');
    throw new Error(`Timed out waiting for ${label}: ${JSON.stringify(page)}`);
  }
  await call('Page.navigate', { url: process.env.OAUTH_ISSUER + '/ui/login' });
  await waitFor('!!document.querySelector("input[name=password]")', 'sign-in form');
  await evaluate(`document.querySelector('input[name=username]').value = 'alice';
    document.querySelector('input[name=password]').value = 'hunter2hunter';
    document.querySelector('form.auth-form').requestSubmit();`);
  await waitFor('location.pathname === "/ui/library"', 'sign-in');
  await call('Page.navigate', { url: process.env.OAUTH_CLIENT + '/' });
  await waitFor('!!document.querySelector("#connect")', 'assistant link');
  await evaluate('document.querySelector("#connect").click()');
  await waitFor('!!document.querySelector("a.btn.primary") && document.body.textContent.includes("Continue")', 'cross-site continuation');
  if (await evaluate('!!document.querySelector("input[name=password]")')) throw new Error('Unexpected sign-in on cross-site arrival');
  await evaluate('document.querySelector("a.btn.primary").click()');
  await waitFor('!!document.querySelector("button[name=decision][value=allow]")', 'consent after same-site continuation');
  await evaluate('document.querySelector("button[name=decision][value=allow]").click()');
  await waitFor('location.pathname === "/callback" && document.body.textContent.includes("Callback received")', 'OAuth callback');
  await call('Page.navigate', { url: process.env.OAUTH_ISSUER + '/ui/settings?section=admin&view=assistants' });
  const toggle = 'form[action$="admin/oauth/chatgpt"] button';
  await waitFor(`document.readyState === "complete" && document.querySelector('${toggle}')?.textContent.includes('Stop allowing')`, 'ChatGPT administration');
  await evaluate(`document.querySelector('${toggle}').click()`);
  if (dialogs !== 1) throw new Error('Cancel did not show a confirmation');
  await waitFor(`document.querySelector('${toggle}')?.textContent.includes('Stop allowing')`, 'cancelled removal');
  await evaluate(`document.querySelector('${toggle}').click()`);
  if (dialogs !== 2) throw new Error('Removal did not show a confirmation');
  await waitFor(`document.querySelector('${toggle}')?.textContent.includes('Allow ChatGPT')`, 'confirmed removal');
}

try {
  await Promise.race([
    run(),
    new Promise((_, reject) => { watchdog = setTimeout(() => reject(new Error('Browser flow timed out')), 35000); }),
  ]);
} finally {
  clearTimeout(watchdog);
  ws?.close();
  if (chrome.exitCode === null && chrome.signalCode === null) {
    const closed = new Promise(resolve => chrome.once('close', () => resolve(true)));
    chrome.kill();
    let timeout;
    const exited = await Promise.race([closed, new Promise(resolve => { timeout = setTimeout(() => resolve(false), 5000); })]);
    clearTimeout(timeout);
    if (!exited) {
      chrome.kill('SIGKILL');
      await closed;
    }
  }
  rmSync(profile, { recursive: true, force: true, maxRetries: 3 });
}
