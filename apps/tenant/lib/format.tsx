'use client';

/** Money and date formatting. Display only — the backend owns every number. */

import type { Price } from './api';

/** `250000` → `TZS 250,000`. Integer TZS, no decimals (SPEC §5 money rule). */
export function fmtTZS(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—';
  return `TZS ${Math.round(Number(n)).toLocaleString('en-US')}`;
}

/** Digits only, for use inside an element that already prints the currency. */
export function fmtAmount(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—';
  return Math.round(Number(n)).toLocaleString('en-US');
}

/** `{amount: 250000, period_days: 30}` → `TZS 250,000 / 30 days`. */
export function fmtPrice(price: Price | null | undefined): string {
  if (!price) return '—';
  return `${fmtTZS(price.amount)} / ${price.period_days} ${price.period_days === 1 ? 'day' : 'days'}`;
}

/** Tabular money, currency set small and raised by the design system. */
export function Amount({ value, per }: { value: number | null | undefined; per?: number | null }) {
  if (value === null || value === undefined) return <span className="pencil">no price</span>;
  return (
    <span className="amount">
      <span className="currency">TZS</span>
      <span className="num">{fmtAmount(value)}</span>
      {per ? (
        <span style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {' '}
          / {per} {per === 1 ? 'day' : 'days'}
        </span>
      ) : null}
    </span>
  );
}

/** RFC3339 or `YYYY-MM-DD` → `5 Sep 2026`. */
export function fmtDate(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value.length === 10 ? `${value}T00:00:00Z` : value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

/** Today as `YYYY-MM-DD` in the browser's zone — a default for `effective_from`. */
export function todayISO(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** Whole days between `since` and now; null when the unit was never vacant. */
export function daysSince(since: string | null | undefined): number | null {
  if (!since) return null;
  const d = new Date(since.length === 10 ? `${since}T00:00:00Z` : since);
  if (Number.isNaN(d.getTime())) return null;
  const ms = Date.now() - d.getTime();
  return Math.max(0, Math.floor(ms / 86_400_000));
}
