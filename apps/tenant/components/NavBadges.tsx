'use client';

/**
 * Nav badge counts. Split out of Shell.tsx so a page can read the pending
 * count without importing the portal chrome (which would remount the nav —
 * PLAN2 #7).
 *
 * The counts are fetched by the portal layout, kept for the life of the
 * session, and refreshed when the window regains focus or the interval fires
 * — never on route change.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { contractsApi, linkRequestsApi } from '../lib/api';
import { isReadyToCountersign } from './ContractBits';

/** How many pending requests the badge will count before it gives up and says "50+". */
export const PENDING_BADGE_CAP = 50;

/** How often the chrome re-reads its badge counts while the tab is open. */
export const BADGE_REFRESH_MS = 60_000;

export function pendingLabel(count: number): string {
  return count >= PENDING_BADGE_CAP ? `${PENDING_BADGE_CAP}+` : String(count);
}

export interface Badges {
  /** Pending link requests, or null while unknown. */
  pending: number | null;
  /** Contracts ready for the landlord's countersignature, or null. */
  countersign: number | null;
}

async function readPending(signal?: AbortSignal): Promise<number | null> {
  try {
    const r = await linkRequestsApi.list({ status: 'pending', limit: PENDING_BADGE_CAP }, signal);
    return typeof r.total === 'number' ? r.total : (r.items ?? []).length;
  } catch {
    return null;
  }
}

async function readCountersign(signal?: AbortSignal): Promise<number | null> {
  try {
    const r = await contractsApi.list({ status: 'pending_signature', limit: 200 }, signal);
    return (r.items ?? []).filter(isReadyToCountersign).length;
  } catch {
    return null;
  }
}

/**
 * Badge counts for the portal chrome. Errors are silent — a badge must never
 * break the chrome.
 */
export function useNavBadges(): Badges {
  const [badges, setBadges] = useState<Badges>({ pending: null, countersign: null });
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    inflight.current?.abort();
    const ac = new AbortController();
    inflight.current = ac;
    const [pending, countersign] = await Promise.all([readPending(ac.signal), readCountersign(ac.signal)]);
    if (ac.signal.aborted) return;
    setBadges({ pending, countersign });
  }, []);

  useEffect(() => {
    void refresh();
    const onFocus = () => void refresh();
    window.addEventListener('focus', onFocus);
    const timer = window.setInterval(() => void refresh(), BADGE_REFRESH_MS);
    return () => {
      window.removeEventListener('focus', onFocus);
      window.clearInterval(timer);
      inflight.current?.abort();
    };
  }, [refresh]);

  return badges;
}

/** Pending link requests on their own, for pages that show the count in a card. */
export function usePendingLinkRequests(): number | null {
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    void readPending(ac.signal).then((n) => {
      if (!ac.signal.aborted) setCount(n);
    });
    return () => ac.abort();
  }, []);

  return count;
}
