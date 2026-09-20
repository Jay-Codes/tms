'use client';

/**
 * Registers the app-shell service worker. Off in development unless
 * NEXT_PUBLIC_ENABLE_SW=1, because a worker caching HMR chunks makes the dev
 * loop lie about what is on screen (API.md Phase 7).
 *
 * Renders nothing and fails silently: a browser without service workers, or one
 * refusing to register over plain http, still gets the whole app.
 */

import { useEffect } from 'react';
import { BASE_PATH } from '../lib/basePath';

const ENABLED =
  process.env.NODE_ENV === 'production' || process.env.NEXT_PUBLIC_ENABLE_SW === '1';

export function RegisterSW() {
  useEffect(() => {
    if (!ENABLED) return;
    if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;
    navigator.serviceWorker.register(`${BASE_PATH}/sw.js`, { scope: `${BASE_PATH}/` }).catch(() => undefined);
  }, []);

  return null;
}
