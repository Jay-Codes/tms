'use client';

/**
 * Money and date formatting. Display only — the backend owns every number.
 *
 * Money is never translated: TZS is TZS and the digits are grouped the same
 * way in both languages (SPEC §5). Dates are, so the pure helpers below read
 * the active locale from a module slot that <LocaleProvider> keeps in step
 * during render — that way the ~100 existing `fmtDate(x)` call sites keep
 * working without threading a locale through every component.
 */

import { formatDateIntl, intlTag, makeTranslator, type Locale, type Translator } from '@tms/ui';
import en from '../i18n/en';
import sw from '../i18n/sw';
import type { Price } from './api';

let activeLocale: Locale = 'sw';
let activeT: Translator = makeTranslator('sw', sw, en);

/** Called by <LocaleProvider> on every render, before any child formats. */
export function setFormatLocale(locale: Locale): void {
  if (locale === activeLocale) return;
  activeLocale = locale;
  activeT = makeTranslator(locale, locale === 'sw' ? sw : en, locale === 'sw' ? en : sw);
}

export function formatLocale(): Locale {
  return activeLocale;
}

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
  const per = activeT(price.period_days === 1 ? 'common.per_day' : 'common.per_days', {
    count: price.period_days,
  });
  return `${fmtTZS(price.amount)} ${per}`;
}

/** Tabular money, currency set small and raised by the design system. */
export function Amount({ value, per }: { value: number | null | undefined; per?: number | null }) {
  if (value === null || value === undefined) return <span className="pencil">{activeT('common.no_price')}</span>;
  return (
    <span className="amount">
      <span className="currency">TZS</span>
      <span className="num">{fmtAmount(value)}</span>
      {per ? (
        <span style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {' '}
          {activeT(per === 1 ? 'common.per_day' : 'common.per_days', { count: per })}
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
  return formatDateIntl(activeLocale, value);
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
  const time = d.toLocaleTimeString(intlTag(activeLocale), {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  return `${formatDateIntl(activeLocale, value)}, ${time}`;
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

/** RFC3339 → `YYYY-MM-DDTHH:mm` in the browser's zone, for a prefilled box. */
export function rfc3339ToDatetimeLocal(value: string | null | undefined): string {
  if (!value) return nowDatetimeLocal();
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return nowDatetimeLocal();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/**
 * How long ago something happened, as a unit and a count — "2 h ago" is built
 * from it by the caller so the wording stays in the dictionary (SPEC §3.2).
 * Anything under a minute reads as `just_now`.
 */
export function ageParts(
  value: string | null | undefined,
): { unit: 'just_now' | 'minutes' | 'hours' | 'days'; count: number } | null {
  if (!value) return null;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return null;
  const secs = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (secs < 60) return { unit: 'just_now', count: 0 };
  if (secs < 3600) return { unit: 'minutes', count: Math.floor(secs / 60) };
  if (secs < 86_400) return { unit: 'hours', count: Math.floor(secs / 3600) };
  return { unit: 'days', count: Math.floor(secs / 86_400) };
}
