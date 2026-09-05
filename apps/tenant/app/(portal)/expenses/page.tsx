'use client';

/**
 * The expense ledger (FLOWS flow 12 step 1).
 *
 * The period comes first, because "how much did this cost me?" is always a
 * question about a window — and the window a landlord last looked at is the one
 * they want again, so it is remembered in the browser (the only thing here that
 * is; every figure comes from the server).
 *
 * Nothing on this screen is summed in the browser. The totals row prints
 * `totals` from `GET /expenses` — the whole filter, not just the page loaded —
 * and the strip above it prints `GET /expenses/summary`, which is also the only
 * source of the period-over-period change.
 */

import { Icon } from '@iconify/react';
import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import { ExpenseSheet } from '../../../components/ExpenseSheet';
import { ExpenseSummaryStrip, ExpensesTable } from '../../../components/ExpenseBits';
import { ProblemNote } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  downloadBlob,
  expenseCategoriesApi,
  expensesApi,
  propertiesApi,
  toApiError,
  type Expense,
  type ExpenseCategory,
  type ExpenseGroupBy,
  type ExpenseListQuery,
  type ExpenseSummary,
  type Property,
} from '../../../lib/api';
import { PeriodPicker, type PeriodValue } from '@tms/ui';
import { loadExpensePeriod, saveExpensePeriod } from '../../../lib/expensePeriod';

type StatusFilter = 'recorded' | 'voided' | 'all';

const STATUSES: { value: StatusFilter; label: string }[] = [
  { value: 'recorded', label: 'Recorded' },
  { value: 'voided', label: 'Voided' },
  { value: 'all', label: 'All' },
];

function ExpensesBody() {
  // `/expenses?property=<id>` is how a property page hands the reader over
  // with its own filter already applied.
  const initialProperty = useSearchParams().get('property') ?? '';

  const [period, setPeriod] = useState<PeriodValue>(loadExpensePeriod);
  const [propertyId, setPropertyId] = useState(initialProperty);
  const [categoryId, setCategoryId] = useState('');
  const [status, setStatus] = useState<StatusFilter>('recorded');
  const [q, setQ] = useState('');
  /** `q` debounced — the ledger is not re-read on every keystroke. */
  const [search, setSearch] = useState('');

  const [properties, setProperties] = useState<Property[]>([]);
  const [categories, setCategories] = useState<ExpenseCategory[]>([]);

  const [items, setItems] = useState<Expense[] | null>(null);
  const [totals, setTotals] = useState<{ count: number; amount: number } | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const [summary, setSummary] = useState<ExpenseSummary | null>(null);
  const [groupBy, setGroupBy] = useState<ExpenseGroupBy>('category');

  const [sheetOpen, setSheetOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
  /** Bumped after a write so both reads re-run without duplicating the effects. */
  const [version, setVersion] = useState(0);

  useEffect(() => saveExpensePeriod(period), [period]);

  useEffect(() => {
    const t = setTimeout(() => setSearch(q.trim()), 300);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => setProperties(r.items ?? []))
      .catch(() => setProperties([]));
    expenseCategoriesApi
      .list(ac.signal)
      .then((r) => setCategories(r.items ?? []))
      .catch(() => setCategories([]));
    return () => ac.abort();
  }, []);

  const query = useMemo<ExpenseListQuery>(
    () => ({
      from: period.from,
      to: period.to,
      property_id: propertyId || undefined,
      category_id: categoryId || undefined,
      status,
      q: search || undefined,
      limit: 50,
    }),
    [period.from, period.to, propertyId, categoryId, status, search],
  );

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setItems(null);
      setTotals(null);
      setCursor(null);
      setError(null);
      try {
        const res = await expensesApi.list(query, signal);
        setItems(res.items ?? []);
        setTotals(res.totals ?? null);
        setCursor(res.next_cursor ?? null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [query],
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
      .summary(
        { from: period.from, to: period.to, group_by: groupBy, property_id: propertyId || undefined },
        ac.signal,
      )
      .then(setSummary)
      .catch(() => setSummary(null));
    return () => ac.abort();
  }, [period.from, period.to, groupBy, propertyId, version]);

  const loadMore = async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await expensesApi.list({ ...query, cursor });
      setItems((prev) => [...(prev ?? []), ...(res.items ?? [])]);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setLoadingMore(false);
    }
  };

  const exportCsv = async () => {
    setExporting(true);
    setError(null);
    try {
      const { blob, filename } = await expensesApi.csv(query);
      downloadBlob(blob, filename || `expenses-${period.from}-${period.to}.csv`);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setExporting(false);
    }
  };

  return (
    <>
      <PageHead
        title="Expenses"
        lead="What each property costs you: repairs, utilities, levies. Recorded here, netted off in Reports."
        actions={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => void exportCsv()} disabled={exporting}>
              <Icon icon="solar:download-minimalistic-linear" width={20} />
              {exporting ? 'Preparing…' : 'Export CSV'}
            </button>
            <button type="button" className="btn btn-primary" onClick={() => setSheetOpen(true)}>
              <Icon icon="solar:add-circle-linear" width={20} /> Record expense
            </button>
          </>
        }
      />

      <div style={{ marginBottom: 'var(--sp-4)' }}>
        <PeriodPicker value={period} onChange={setPeriod} label="Expense period" />
      </div>

      <div
        style={{
          display: 'flex',
          gap: 'var(--sp-3)',
          flexWrap: 'wrap',
          alignItems: 'flex-end',
          marginBottom: 'var(--sp-4)',
        }}
      >
        <div className="field" style={{ minWidth: 180 }}>
          <label htmlFor="f_property">Property</label>
          <select
            id="f_property"
            className="input"
            value={propertyId}
            onChange={(e) => setPropertyId(e.target.value)}
          >
            <option value="">All properties</option>
            {properties.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>

        <div className="field" style={{ minWidth: 180 }}>
          <label htmlFor="f_category">Category</label>
          <select
            id="f_category"
            className="input"
            value={categoryId}
            onChange={(e) => setCategoryId(e.target.value)}
          >
            <option value="">All categories</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
                {c.active ? '' : ' (inactive)'}
              </option>
            ))}
          </select>
        </div>

        <div className="field" style={{ minWidth: 200, flex: 1 }}>
          <label htmlFor="f_q">Search</label>
          <input
            id="f_q"
            className="input"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Vendor, reference or note"
          />
        </div>

        <div className="field">
          <span id="f_status_label" style={{ fontSize: 'var(--text-sm)', fontWeight: 500, marginBottom: 'var(--sp-2)' }}>
            Status
          </span>
          <div className="segmented" role="group" aria-labelledby="f_status_label">
            {STATUSES.map((s) => (
              <button
                key={s.value}
                type="button"
                aria-pressed={status === s.value}
                onClick={() => setStatus(s.value)}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <ExpenseSummaryStrip summary={summary} groupBy={groupBy} onGroupBy={setGroupBy} loading={items === null} />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <ExpensesTable
          items={items}
          totals={totals}
          emptyText={
            error
              ? 'Nothing to show.'
              : status === 'voided'
                ? 'No voided expenses in this period.'
                : 'No expenses in this period. Record the first one.'
          }
        />

        {cursor ? (
          <div>
            <button type="button" className="btn btn-secondary" onClick={() => void loadMore()} disabled={loadingMore}>
              {loadingMore ? 'Loading…' : 'Load more'}
            </button>
          </div>
        ) : null}

        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Corrections are recorded, never erased: an expense entered by mistake is voided with a
          reason and stays in the ledger.
        </p>
      </div>

      <ExpenseSheet
        open={sheetOpen}
        onClose={() => setSheetOpen(false)}
        // The sheet closes itself unless the receipt upload failed, in which
        // case it stays open to say so — so this only refreshes the ledger.
        onSaved={() => setVersion((v) => v + 1)}
      />
    </>
  );
}

export default function ExpensesPage() {
  return (
    <Suspense fallback={null}>
      <ExpensesBody />
    </Suspense>
  );
}
