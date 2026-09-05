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

/** RFC3339 → `5 Sep 2026, 14:30` — payments are recorded to the minute. */
export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}, ${d.toLocaleTimeString(
    'en-GB',
    { hour: '2-digit', minute: '2-digit' },
  )}`;
}

/** `YYYY-MM-DDTHH:mm` in the browser's zone — the default for `paid_at`. */
export function nowDatetimeLocal(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** `todayISO()` shifted by whole days — used for the "due soon" window. */
export function isoPlusDays(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** First day of the current month as `YYYY-MM-DD`. */
export function monthStartISO(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`;
}

/**
 * A `datetime-local` value → RFC3339 UTC, which is what the API stores. An
 * empty box means "let the backend default it to now", so it maps to undefined.
 */
export function datetimeLocalToRfc3339(value: string): string | undefined {
  if (!value) return undefined;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}
