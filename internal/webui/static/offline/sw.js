const ROOT = new URL('./', self.location).pathname;
const CACHE_PREFIX = 'liseur-sync-offline-shell-' + encodeURIComponent(ROOT) + '-';
const CACHE = CACHE_PREFIX + '__OFFLINE_SHELL_REVISION__';
const SHELL = new URL('./shell.html', self.location).href;
const ASSETS = [
  SHELL,
  new URL('./manifest.json', self.location).href,
  new URL('./offline.css', self.location).href,
  new URL('./offline.js', self.location).href,
  new URL('./offline-shelf.js', self.location).href,
  new URL('./icon.svg', self.location).href,
  new URL('./icon-512.svg', self.location).href,
  new URL('./icon-192.png', self.location).href,
  new URL('./icon-512.png', self.location).href,
  new URL('./icon-maskable-512.png', self.location).href,
  new URL('./apple-touch-icon.png', self.location).href,
  new URL('./read/', self.location).href,
  new URL('./assets/offline-account.js', self.location).href,
  new URL('./assets/offline-storage.js', self.location).href,
  new URL('./assets/offline-sync.js', self.location).href,
  new URL('./assets/reader-annotations.js', self.location).href,
  new URL('./assets/reader-app.js', self.location).href,
  new URL('./assets/reader-auth.js', self.location).href,
  new URL('./assets/reader-engine.js', self.location).href,
  new URL('./assets/reader-live.js', self.location).href,
  new URL('./assets/reader-positions.js', self.location).href,
  new URL('./assets/reader-publication.js', self.location).href,
  new URL('./assets/reader-session-upload.js', self.location).href,
  new URL('./assets/reader-session.js', self.location).href,
  new URL('./assets/reader-sync.js', self.location).href,
  new URL('./assets/style.css', self.location).href,
  new URL('./assets/vendor/foliate/epubcfi.js', self.location).href,
  new URL('./assets/vendor/readium/readium.js', self.location).href,
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE)
      .then((cache) => cache.addAll(ASSETS.map(url => new Request(url, { cache: 'reload' })))),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys.filter((key) => key.startsWith(CACHE_PREFIX) && key !== CACHE)
          .map((key) => caches.delete(key)),
      ))
      .then(() => self.clients.claim()),
  );
});

async function cachedNavigation(shell, reader) {
  const cache = await caches.open(CACHE);
  const response = await cache.match(shell);
  if (!response) return Response.error();
  if (!reader) return response;
  const headers = new Headers(response.headers);
  const policy = headers.get('Content-Security-Policy') || '';
  const oldNonce = policy.match(/'nonce-([^']+)'/)?.[1];
  if (!oldNonce) return Response.error();
  const nonce = btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(16))));
  const html = (await response.text()).replaceAll(`nonce="${oldNonce}"`, `nonce="${nonce}"`);
  headers.set('Content-Security-Policy', policy.replaceAll(`'nonce-${oldNonce}'`, `'nonce-${nonce}'`));
  headers.delete('Content-Length');
  headers.set('Cache-Control', 'no-store');
  return new Response(html, { headers });
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin ||
      !url.pathname.startsWith(ROOT)) {
    return;
  }

  if (request.mode === 'navigate' && (url.pathname === ROOT || url.pathname === `${ROOT}read/`)) {
    const shell = url.pathname === `${ROOT}read/`
      ? new URL('./read/', self.location).href
      : SHELL;
    event.respondWith(cachedNavigation(shell, url.pathname === `${ROOT}read/`));
    return;
  }

  if (!ASSETS.includes(request.url)) {
    return;
  }
  event.respondWith(caches.open(CACHE).then(cache => cache.match(request))
    .then(cached => cached || Response.error()));
});
