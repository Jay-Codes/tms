'use client';

/**
 * Service worker registration (API.md Phase 7).
 *
 * Registered in production builds only, unless `NEXT_PUBLIC_ENABLE_SW=1` asks
 * for it explicitly — a worker in front of the dev server would sit between
 * HMR and the browser. When it is off we also unregister anything already
 * installed, so flipping the flag back off actually clears the worker instead
 * of leaving a stale one serving the app.
 */

import { useEffect } from 'react';
import { BASE_PATH } from '../lib/auth';

const ENABLED =
  process.env.NODE_ENV === 'production' || process.env.NEXT_PUBLIC_ENABLE_SW === '1';

export function ServiceWorker() {
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return;

    if (!ENABLED) {
      void navigator.serviceWorker
        .getRegistrations()
        .then((regs) => Promise.all(regs.map((r) => r.unregister())))
        .catch(() => undefined);
      return;
    }

    // The worker lives at the root of its own scope, so no
    // `Service-Worker-Allowed` header is needed.
    void navigator.serviceWorker
      .register(`${BASE_PATH}/sw.js`, { scope: `${BASE_PATH}/` })
      .catch(() => undefined);
  }, []);

  return null;
}
