export const REOPEN_MESSAGE =
  "this reading session has expired; open the book from your library again";

export function readerAuth({
  apiBase, tokenURL, csrf, handed, detached, fetcher = (...args) => fetch(...args),
  now = Date.now, onChange = () => {}, onExhausted = () => {},
}) {
  let credential = null, previous = null, flight = null, generation = 0, stopped = false;
  const responses = new WeakMap();
  const exhausted = () => {
    stopped = true;
    credential = null;
    generation++;
    const err = new Error(REOPEN_MESSAGE);
    err.terminal = true;
    onExhausted(err);
    return err;
  };
  const current = (identity) => !!identity && !stopped && identity === credential;
  const acquire = async () => {
    if (stopped) {
      const err = new Error(REOPEN_MESSAGE);
      err.terminal = true;
      throw err;
    }
    if (credential && now() < credential.expires - 60000) return credential;
    if (flight) return flight;
    const started = generation;
    flight = (async () => {
      let got;
      if (detached) {
        if (!handed) throw exhausted();
        got = { token: handed };
      } else {
        const resp = await fetcher(tokenURL, {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ csrf }),
          signal: AbortSignal.timeout(30000),
        });
        if ([401, 403].includes(resp.status) || resp.redirected) throw exhausted();
        if (!resp.ok) throw new Error("could not obtain a reading credential");
        got = await resp.json();
      }
      if (!got.token) throw new Error("missing reading credential");
      if (stopped || started !== generation) throw new Error("stale reading credential");
      const resp = await fetcher(apiBase + "v1/token", {
        headers: { Authorization: "Bearer " + got.token },
        credentials: "omit", signal: AbortSignal.timeout(30000),
      });
      if (resp.status === 401) throw exhausted();
      // Older servers may lack introspection. A credential-local identity is
      // conservative: replacement invalidates all state rather than mixing users.
      if (!resp.ok && ![404, 501].includes(resp.status))
        throw new Error("could not identify reading credential");
      const info = resp.ok ? await resp.json() : {};
      if (stopped || started !== generation) throw new Error("stale reading credential");
      const old = previous;
      credential = {
        secret: got.token,
        expires: Date.parse(got.expires_at) || (detached ? Infinity : now() + 3600000),
        account: info.account_id || "credential:" + (generation + 1),
        device: info.device_id || got.device_id || null,
        generation: ++generation,
      };
      handed = null;
      previous = credential;
      onChange(credential, old);
      if (stopped) {
        const err = new Error(REOPEN_MESSAGE);
        err.terminal = true;
        throw err;
      }
      return credential;
    })().finally(() => { flight = null; });
    return flight;
  };
  return {
    acquire,
    stop: () => { if (!stopped) exhausted(); },
    identity: () => credential,
    current,
    responseIdentity: (resp) => responses.get(resp),
    responseCurrent: (resp) => current(responses.get(resp)),
    async request(path, options = {}) {
      let refreshed = false;
      for (let attempt = 0; attempt < 2; attempt++) {
        const identity = await acquire();
        options.signal?.throwIfAborted();
        const resp = await fetcher(apiBase + path, {
          ...options, credentials: "omit",
          signal: options.signal || AbortSignal.timeout(30000),
          headers: { ...options.headers, Authorization: "Bearer " + identity.secret },
        });
        if (resp.status !== 401) {
          responses.set(resp, identity);
          return resp;
        }
        await resp.body?.cancel();
        // A late refusal for a replaced token must not evict its successor.
        if (current(identity)) {
          if (detached || refreshed) throw exhausted();
          credential = null;
          refreshed = true;
        }
      }
      // A late refusal of an obsolete token is not a refusal of its successor.
      throw new Error("reading credential changed during the request");
    },
  };
}
