'use client';

/**
 * The "Customize" sheet behind the dashboard (FLOWS flow 9: "layout per org's
 * saved dashboard preferences"). Order is changed with up/down rather than
 * drag, because the same sheet has to work with a thumb on a phone; a card
 * switched off keeps its place in the list so it can be switched back on.
 *
 * The saved value is `dashboard_prefs` on the org branding record — the only
 * store; nothing is kept in the browser.
 */

import { Icon } from '@iconify/react';
import { useEffect, useState } from 'react';
import { ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  DASHBOARD_CARDS,
  brandingApi,
  hiddenDashboardCards,
  type DashboardCard,
  type DashboardPrefs,
} from '../lib/api';

export const CARD_LABELS: Record<DashboardCard, string> = {
  assets: 'Total assets',
  renters: 'Total renters',
  payment_status: 'Payment status',
  collections: 'Collections this month',
  link_requests: 'Link requests',
  overdue: 'Overdue',
};

export function DashboardCustomize({
  open,
  prefs,
  onClose,
  onSaved,
}: {
  open: boolean;
  prefs: DashboardPrefs;
  onClose: () => void;
  onSaved: (next: DashboardPrefs) => void;
}) {
  // Every card in one list: shown ones in their saved order, hidden ones after.
  const [order, setOrder] = useState<DashboardCard[]>([]);
  const [shown, setShown] = useState<Set<DashboardCard>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    if (!open) return;
    setOrder([...prefs.cards, ...hiddenDashboardCards(prefs)]);
    setShown(new Set(prefs.cards));
    setError(null);
  }, [open, prefs]);

  const move = (i: number, delta: number) => {
    setOrder((prev) => {
      const j = i + delta;
      if (j < 0 || j >= prev.length) return prev;
      const next = [...prev];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  };

  const toggle = (card: DashboardCard) => {
    setShown((prev) => {
      const next = new Set(prev);
      if (next.has(card)) next.delete(card);
      else next.add(card);
      return next;
    });
  };

  const save = async () => {
    setBusy(true);
    setError(null);
    const cards = order.filter((c) => shown.has(c));
    try {
      await brandingApi.save({ dashboard_prefs: { cards, layout: prefs.layout } });
      onSaved({ cards, layout: prefs.layout });
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet open={open} title="Customize dashboard" onClose={onClose} width={520}>
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Choose which cards appear and in what order. The arrangement is saved to your business, so
          everyone on your team sees the same dashboard.
        </p>

        <ProblemNote error={error} />

        <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'grid' }}>
          {order.map((card, i) => (
            <li
              key={card}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--sp-3)',
                minHeight: 'var(--row-h)',
                borderBottom: '1px solid var(--rule)',
              }}
            >
              <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flex: 1, cursor: 'pointer' }}>
                <input type="checkbox" checked={shown.has(card)} onChange={() => toggle(card)} />
                <span style={{ color: shown.has(card) ? 'var(--ink)' : 'var(--ink-faint)' }}>
                  {CARD_LABELS[card]}
                </span>
              </label>
              <button
                type="button"
                className="btn btn-quiet"
                aria-label={`Move ${CARD_LABELS[card]} up`}
                disabled={i === 0}
                onClick={() => move(i, -1)}
                style={{ minHeight: 36, padding: '0 var(--sp-2)' }}
              >
                <Icon icon="solar:alt-arrow-up-linear" width={18} />
              </button>
              <button
                type="button"
                className="btn btn-quiet"
                aria-label={`Move ${CARD_LABELS[card]} down`}
                disabled={i === order.length - 1}
                onClick={() => move(i, 1)}
                style={{ minHeight: 36, padding: '0 var(--sp-2)' }}
              >
                <Icon icon="solar:alt-arrow-down-linear" width={18} />
              </button>
            </li>
          ))}
        </ul>

        <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <button type="button" className="btn btn-primary" onClick={() => void save()} disabled={busy}>
            {busy ? 'Saving…' : 'Save layout'}
          </button>
          <button
            type="button"
            className="btn btn-quiet"
            disabled={busy}
            onClick={() => {
              setOrder([...DASHBOARD_CARDS]);
              setShown(new Set(DASHBOARD_CARDS));
            }}
          >
            Reset to default
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
            Cancel
          </button>
        </div>
      </div>
    </Sheet>
  );
}
