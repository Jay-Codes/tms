/*
 * TMS Admin — app-shell service worker (API.md Phase 7 PWA).
 *
 * Policy, identical to the other two apps:
 *   - cache-first for the app shell: /admin/_next/static/*, the manifest, icons
 *   - network-only for /api/* and the presigned bucket paths (a signed URL is
 *     single-use and money/audit data must never be served stale)
 *   - navigations: network first, falling back to the cached shell offline
 *   - no offline writes: a non-GET request is never touched
 */

const VERSION = 'tms-admin-v1';
const SHELL_CACHE = `${VERSION}-shell`;
const BASE = '/admin';

/** Precached so a cold offline start still paints the console chrome. */
const SHELL_ASSETS = [
  `${BASE}/`,
  `${BASE}/manifest.webmanifest`,
  `${BASE}/icon-192.png`,
  `${BASE}/icon-512.png`,
];

/** Never cached: live data and single-use signed URLs. */
const NETWORK_ONLY_PREFIXES = ['/api/', '/branding/', '/qrcodes/', '/kyc/', '/signatures/'];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.addAll(SHELL_ASSETS))
      .catch(() => undefined)
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SHELL_CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

function isShellAsset(url) {
  return (
    url.pathname.startsWith(`${BASE}/_next/static/`) ||
    url.pathname === `${BASE}/manifest.webmanifest` ||
    /^\/admin\/icon-[\w-]+\.png$/.test(url.pathname)
  );
}

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return; // no offline writes

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (NETWORK_ONLY_PREFIXES.some((p) => url.pathname.startsWith(p))) return;

  if (isShellAsset(url)) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone();
              caches.open(SHELL_CACHE).then((c) => c.put(req, copy)).catch(() => undefined);
            }
            return res;
          }),
      ),
    );
    return;
  }

  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).catch(() => caches.match(`${BASE}/`).then((hit) => hit || Response.error())),
    );
  }
});
