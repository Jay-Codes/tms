'use client';

/**
 * A property's own expenses (FLOWS flow 12 step 4).
 *
 * The same ledger as `/expenses`, filtered to one property and with its own
 * total, so a landlord standing in front of a building can answer "what has
 * this place cost me this quarter?" without leaving it. The window is the one
 * shared with the main ledger, so the two screens never disagree.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { ExpenseSheet } from './ExpenseSheet';
import { ChangeMark, ExpensesTable } from './ExpenseBits';
import { ProblemNote } from './FormBits';
import {
  ApiError,
  expensesApi,
  toApiError,
  type Expense,
  type ExpenseSummary,
} from '../lib/api';
import { fmtTZS } from '../lib/format';
import { loadExpensePeriod, saveExpensePeriod } from '../lib/expensePeriod';
import { PeriodPicker, periodLabel, type PeriodValue } from '@tms/ui';

export function PropertyExpenses({
  propertyId,
  propertyName,
}: {
  propertyId: string;
  propertyName?: string;
}) {
  const [period, setPeriod] = useState<PeriodValue>(loadExpensePeriod);
  const [items, setItems] = useState<Expense[] | null>(null);
  const [totals, setTotals] = useState<{ count: number; amount: number } | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [summary, setSummary] = useState<ExpenseSummary | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [version, setVersion] = useState(0);

  useEffect(() => saveExpensePeriod(period), [period]);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setItems(null);
      setTotals(null);
      setCursor(null);
      setError(null);
      try {
        const res = await expensesApi.list(
          { property_id: propertyId, from: period.from, to: period.to, status: 'recorded', limit: 50 },
          signal,
        );
        setItems(res.items ?? []);
        setTotals(res.totals ?? null);
        setCursor(res.next_cursor ?? null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [propertyId, period.from, period.to],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load, version]);

  useEffect(() => {
    const ac = new AbortController();
    setSummary(null);
    expensesApi
      .summary({ property_id: propertyId, from: period.from, to: period.to, group_by: 'category' }, ac.signal)
      .then(setSummary)
      .catch(() => setSummary(null));
    return () => ac.abort();
  }, [propertyId, period.from, period.to, version]);

  const loadMore = async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await expensesApi.list({
        property_id: propertyId,
        from: period.from,
        to: period.to,
        status: 'recorded',
        limit: 50,
        cursor,
      });
      setItems((prev) => [...(prev ?? []), ...(res.items ?? [])]);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <section aria-label="Expenses for this property" style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          justifyContent: 'space-between',
          gap: 'var(--sp-4)',
          flexWrap: 'wrap',
        }}
      >
        <div>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>Expenses</h2>
          <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            What this property cost in {periodLabel(period)}.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <Link href={`/expenses?property=${propertyId}`} className="btn btn-quiet">
            All expenses
          </Link>
          <button type="button" className="btn btn-primary" onClick={() => setSheetOpen(true)}>
            <Icon icon="solar:add-circle-linear" width={20} /> Record expense
          </button>
        </div>
      </div>

      <PeriodPicker value={period} onChange={setPeriod} label="Expense period" />

      <p
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 'var(--sp-3)',
          flexWrap: 'wrap',
          margin: 0,
        }}
      >
        <Icon icon="solar:bill-list-linear" width={20} style={{ alignSelf: 'center' }} />
        <strong style={{ fontSize: 'var(--text-xl)', fontVariantNumeric: 'tabular-nums lining-nums' }}>
          {totals ? fmtTZS(totals.amount) : summary ? fmtTZS(summary.total.amount) : '—'}
        </strong>
        <ChangeMark pct={summary?.change_pct} />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          vs {summary ? fmtTZS(summary.previous_total?.amount ?? 0) : '—'} in the period before
        </span>
      </p>

      <ProblemNote error={error} />

      <ExpensesTable
        items={items}
        totals={totals}
        showProperty={false}
        label="Property expenses"
        emptyText={error ? 'Nothing to show.' : 'Nothing recorded against this property in this period.'}
      />

      {cursor ? (
        <div>
          <button type="button" className="btn btn-secondary" onClick={() => void loadMore()} disabled={loadingMore}>
            {loadingMore ? 'Loading…' : 'Load more'}
          </button>
        </div>
      ) : null}

      <ExpenseSheet
        open={sheetOpen}
        lockedPropertyId={propertyId}
        lockedPropertyName={propertyName}
        onClose={() => setSheetOpen(false)}
        onSaved={() => setVersion((v) => v + 1)}
      />
    </section>
  );
}
