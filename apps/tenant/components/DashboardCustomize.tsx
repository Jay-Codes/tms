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
import { useT } from '@tms/ui';
import { ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  DASHBOARD_CARDS,
  DEFAULT_DASHBOARD_CARDS,
  brandingApi,
  hiddenDashboardCards,
  type DashboardCard,
  type DashboardPrefs,
} from '../lib/api';

/** Dictionary keys for the card labels — translated where they are printed. */
export const CARD_LABEL_KEYS: Record<DashboardCard, string> = {
  assets: 'dash.card.assets',
  renters: 'dash.card.renters',
  payment_status: 'dash.card.payment_status',
  collections: 'dash.card.collections',
  link_requests: 'dash.card.link_requests',
  overdue: 'dash.card.overdue',
  expenses: 'dash.card.expenses',
  revenue: 'dash.card.revenue',
  net_income: 'dash.card.net_income',
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
  const t = useT();
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
    <Sheet open={open} title={t('dash.customize.title')} onClose={onClose} width={520}>
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('dash.customize.lead')}
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
                  {t(CARD_LABEL_KEYS[card])}
                </span>
              </label>
              <button
                type="button"
                className="btn btn-quiet"
                aria-label={t('dash.customize.move_up', { card: t(CARD_LABEL_KEYS[card]) })}
                disabled={i === 0}
                onClick={() => move(i, -1)}
                style={{ minHeight: 36, padding: '0 var(--sp-2)' }}
              >
                <Icon icon="solar:alt-arrow-up-linear" width={18} />
              </button>
              <button
                type="button"
                className="btn btn-quiet"
                aria-label={t('dash.customize.move_down', { card: t(CARD_LABEL_KEYS[card]) })}
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
            {busy ? t('common.saving') : t('dash.customize.save')}
          </button>
          <button
            type="button"
            className="btn btn-quiet"
            disabled={busy}
            onClick={() => {
              setOrder([...DASHBOARD_CARDS]);
              setShown(new Set(DEFAULT_DASHBOARD_CARDS));
            }}
          >
            {t('dash.customize.reset')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </button>
        </div>
      </div>
    </Sheet>
  );
}
