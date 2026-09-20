/*
 * Landlord portal service worker (API.md Phase 7 — PWA).
 *
 * App shell only. There are no offline writes and no cached business data:
 *   - cache-first  : the built assets under /tenant/_next/static/*, the
 *                    manifest and the icons — content-hashed or rarely
 *                    changed, so a hit is always correct.
 *   - network-only : /api/*, and the presigned MinIO paths /branding/*,
 *                    /qrcodes/*, /kyc/*, /signatures/* — money, documents and
 *                    signed URLs must never be served from a stale cache.
 *   - navigations  : network first, falling back to the cached shell so the
 *                    app opens on a bad connection and then fetches for itself.
 *
 * Registered only in production builds or with NEXT_PUBLIC_ENABLE_SW=1.
 */

const VERSION = 'tms-tenant-v1';
const BASE = self.location.pathname.replace(/\/sw\.js$/, ''); // '' at the domain root, '/x' behind the dev proxy
const SHELL_URL = `${BASE}/`;
const PRECACHE = [SHELL_URL, `${BASE}/manifest.webmanifest`, `${BASE}/icons/icon-192.png`, `${BASE}/icons/icon-512.png`];

/** Paths that must always go to the network, whatever the connection. */
const NETWORK_ONLY = ['/api/', '/branding/', '/qrcodes/', '/kyc/', '/signatures/'];

/** Paths safe to serve from cache first. */
function isShellAsset(pathname) {
  return (
    pathname.startsWith(`${BASE}/_next/static/`) ||
    pathname.startsWith('/_next/static/') ||
    pathname === `${BASE}/manifest.webmanifest` ||
    pathname.startsWith(`${BASE}/icons/`)
  );
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(VERSION)
      // A missing entry must not fail the whole install, so each is added on its own.
      .then((cache) => Promise.all(PRECACHE.map((u) => cache.add(u).catch(() => undefined))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (NETWORK_ONLY.some((p) => url.pathname.startsWith(p))) return;

  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res && res.ok && url.pathname === SHELL_URL) {
            const copy = res.clone();
            caches.open(VERSION).then((c) => c.put(SHELL_URL, copy));
          }
          return res;
        })
        .catch(() => caches.match(SHELL_URL).then((hit) => hit || Response.error())),
    );
    return;
  }

  if (!isShellAsset(url.pathname)) return;

  event.respondWith(
    caches.match(req).then(
      (hit) =>
        hit ||
        fetch(req).then((res) => {
          if (res && res.ok) {
            const copy = res.clone();
            caches.open(VERSION).then((c) => c.put(req, copy));
          }
          return res;
        }),
    ),
  );
});
