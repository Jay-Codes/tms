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
import { contractsApi, linkRequestsApi, proofsApi } from '../lib/api';
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
  /** Proofs of payment still waiting for an answer (Phase 16 §16.1), or null. */
  proofs: number | null;
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
 * Submitted proofs of payment. Its own endpoint (`GET /proofs/summary`) rather
 * than a listing, because the badge is drawn on every landlord screen and the
 * queue itself is only ever one of them.
 */
async function readProofs(signal?: AbortSignal): Promise<number | null> {
  try {
    const r = await proofsApi.summary(signal);
    return typeof r.submitted_count === 'number' ? r.submitted_count : null;
  } catch {
    return null;
  }
}

/**
 * Screens that change a count (accepting a proof, answering a link request)
 * call this so the chrome catches up at once instead of waiting for the next
 * focus or interval. There is still exactly one poller — the chrome's.
 */
const listeners = new Set<() => void>();

export function refreshNavBadges(): void {
  for (const fn of listeners) fn();
}

/**
 * Badge counts for the portal chrome. Errors are silent — a badge must never
 * break the chrome.
 */
export function useNavBadges(): Badges {
  const [badges, setBadges] = useState<Badges>({ pending: null, countersign: null, proofs: null });
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    inflight.current?.abort();
    const ac = new AbortController();
    inflight.current = ac;
    const [pending, countersign, proofs] = await Promise.all([
      readPending(ac.signal),
      readCountersign(ac.signal),
      readProofs(ac.signal),
    ]);
    if (ac.signal.aborted) return;
    setBadges({ pending, countersign, proofs });
  }, []);

  useEffect(() => {
    void refresh();
    const onFocus = () => void refresh();
    const onAsk = () => void refresh();
    window.addEventListener('focus', onFocus);
    listeners.add(onAsk);
    const timer = window.setInterval(() => void refresh(), BADGE_REFRESH_MS);
    return () => {
      window.removeEventListener('focus', onFocus);
      listeners.delete(onAsk);
      window.clearInterval(timer);
      inflight.current?.abort();
    };
  }, [refresh]);

  return badges;
}

/**
 * The submitted-proof count on its own, for the Payments page — which puts the
 * Proofs tab first while anything is waiting. One read on mount plus whatever
 * `refreshNavBadges()` triggers; the chrome owns the polling.
 */
export function useSubmittedProofs(): { count: number | null; refresh: () => void } {
  const [count, setCount] = useState<number | null>(null);

  const read = useCallback((signal?: AbortSignal) => {
    void readProofs(signal).then((n) => {
      if (!signal?.aborted) setCount(n);
    });
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    read(ac.signal);
    const onAsk = () => read();
    listeners.add(onAsk);
    window.addEventListener('focus', onAsk);
    return () => {
      listeners.delete(onAsk);
      window.removeEventListener('focus', onAsk);
      ac.abort();
    };
  }, [read]);

  return { count, refresh: refreshNavBadges };
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
