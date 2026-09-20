/*
 * Renter PWA service worker — app-shell caching only (SPEC §2, API.md Phase 7).
 *
 * Rules, in the order the fetch handler applies them:
 *   1. Anything that is not a same-origin GET is left to the network.
 *   2. Live data and bucket objects (/api, /branding, /qrcodes, /kyc,
 *      /signatures) are network-only — money, KYC images and presigned URLs
 *      must never be answered from a cache.
 *   3. Navigations go to the network first and fall back to the cached
 *      `/enduser/` shell, so a renter on a dead connection still gets the app
 *      frame (which then shows its own "cannot reach the server" copy).
 *   4. Build assets, the manifest and the icons are cache-first — they are
 *      immutable per build and CACHE is versioned.
 *
 * There are no offline writes: nothing is queued, nothing is replayed.
 */

const BASE = self.location.pathname.replace(/\/sw\.js$/, ''); // '' at the domain root, '/x' behind the dev proxy
const SHELL_URL = `${BASE}/`;
const CACHE = 'tms-enduser-shell-v1';

/** Precached on install. Kept tiny: the shell plus its install identity. */
const PRECACHE = [SHELL_URL, `${BASE}/manifest.webmanifest`, `${BASE}/icons/icon-192.png`, `${BASE}/icons/icon-512.png`];

/** Never served from cache — see rule 2. */
const NETWORK_ONLY = ['/api/', '/branding/', '/qrcodes/', '/kyc/', '/signatures/'];

/** Cache-first — see rule 4. */
function isShellAsset(pathname) {
  return (
    pathname.startsWith(`${BASE}/_next/static/`) ||
    pathname === `${BASE}/manifest.webmanifest` ||
    pathname.startsWith(`${BASE}/icons/`)
  );
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      // One bad URL must not fail the whole install, so each is added alone.
      .then((cache) => Promise.all(PRECACHE.map((url) => cache.add(url).catch(() => undefined))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (NETWORK_ONLY.some((prefix) => url.pathname.startsWith(prefix))) return;

  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request)
        .then((response) => {
          // Keep the shell fresh whenever the network answers with one.
          if (response.ok && url.pathname === SHELL_URL) {
            const copy = response.clone();
            void caches.open(CACHE).then((cache) => cache.put(SHELL_URL, copy));
          }
          return response;
        })
        .catch(() => caches.match(SHELL_URL).then((cached) => cached || Response.error())),
    );
    return;
  }

  if (!isShellAsset(url.pathname)) return;

  event.respondWith(
    caches.match(request).then(
      (cached) =>
        cached ||
        fetch(request).then((response) => {
          if (response.ok) {
            const copy = response.clone();
            void caches.open(CACHE).then((cache) => cache.put(request, copy));
          }
          return response;
        }),
    ),
  );
});
