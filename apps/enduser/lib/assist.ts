/**
 * Landlord-assisted onboarding on the renter's device (FLOWS 2b, PLAN2
 * Phase 25).
 *
 * The landlord's QR lands on `/u/{unit_code}?assist={session_id}`. From then
 * on, until the link request is sent, register and login skip "Send code":
 * the code is already on the landlord's screen, written into the very slot
 * the SMS would have filled, so `POST /auth/otp/verify` takes it unchanged.
 *
 * The session id is kept in sessionStorage — the length of the tab, like the
 * scanned unit (lib/scan.ts) — because register and login are reached by
 * links that do not carry it. It is always re-checked against
 * `GET /public/assist/{id}`: a closed or expired session drops the tab back
 * to the ordinary SMS path.
 */

import { useEffect, useState } from 'react';
import { ApiError, assistPublicApi, type PublicAssist } from './api';

const KEY = 'tms.assist.session';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function rememberAssist(id: string): void {
  try {
    sessionStorage.setItem(KEY, id);
  } catch {
    /* no session storage — the unit page still carries ?assist= */
  }
}

export function readAssist(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function forgetAssist(): void {
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* nothing to forget */
  }
}

export type AssistState =
  | { state: 'none' }
  | { state: 'loading' }
  /** The landlord closed the session, or it ran out — only reported for `?assist=`. */
  | { state: 'ended' }
  | ({ state: 'open'; id: string } & PublicAssist);

/**
 * The assist session this tab is in. `?assist=` on the current URL wins over
 * the remembered one (a second QR replaces the first). Read after mount, like
 * `?next=`: pages are prerendered and must not bake a query string in.
 */
export function useAssist(): AssistState {
  const [out, setOut] = useState<AssistState>({ state: 'loading' });

  useEffect(() => {
    const fromUrl = new URLSearchParams(window.location.search).get('assist');
    const id = fromUrl && UUID.test(fromUrl) ? fromUrl : readAssist();
    if (!id || !UUID.test(id)) {
      setOut({ state: 'none' });
      return;
    }
    const ac = new AbortController();
    assistPublicApi
      .get(id, ac.signal)
      .then((res) => {
        if (res.status === 'open') {
          rememberAssist(id);
          setOut({ state: 'open', id, ...res });
        } else {
          forgetAssist();
          setOut(fromUrl ? { state: 'ended' } : { state: 'none' });
        }
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') return;
        // Unknown id: forget it. No network: keep it for the next screen.
        // Either way this screen falls back to the SMS path, which still
        // works if the text does arrive.
        const unknown = err instanceof ApiError && err.status === 404;
        if (unknown) forgetAssist();
        setOut(unknown && fromUrl ? { state: 'ended' } : { state: 'none' });
      });
    return () => ac.abort();
  }, []);

  return out;
}
