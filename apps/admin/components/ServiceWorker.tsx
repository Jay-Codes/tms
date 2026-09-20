'use client';

/**
 * Registers the app-shell service worker.
 *
 * Only in production builds, or when NEXT_PUBLIC_ENABLE_SW=1 — a worker in
 * front of the dev server intercepts HMR and makes edits look lost (API.md
 * Phase 7 PWA). The scope is the app's basePath, so /admin/sw.js controls
 * /admin/* only and never the other two apps on the same origin.
 */

import { useEffect } from 'react';

import { BASE_PATH as BASE } from '../lib/basePath';

export function ServiceWorker() {
  useEffect(() => {
    const enabled =
      process.env.NODE_ENV === 'production' || process.env.NEXT_PUBLIC_ENABLE_SW === '1';
    if (!enabled || typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;
    navigator.serviceWorker.register(`${BASE}/sw.js`, { scope: `${BASE}/` }).catch(() => {
      /* a missing worker must never break the console */
    });
  }, []);

  return null;
}
