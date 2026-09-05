/**
 * UX-only helpers: phone shaping and error copy.
 *
 * None of this is business logic — the Go API normalizes and validates
 * everything again. This exists so a renter finds out about a typo before
 * spending an SMS.
 */

import { formatDateIntl, type Locale, type Translator } from '@tms/ui';
import { ApiError } from './api';

/**
 * Normalize a Tanzanian mobile number to E.164 (`+2557XXXXXXXX`).
 * Accepts `07…`, `7…`, `255…`, `+255…`, with spaces or dashes.
 * Returns null when the input can't be read as a TZ mobile number.
 */
export function normalizePhone(input: string): string | null {
  const digits = input.replace(/[^\d+]/g, '');
  let rest: string;

  if (digits.startsWith('+255')) rest = digits.slice(4);
  else if (digits.startsWith('255')) rest = digits.slice(3);
  else if (digits.startsWith('0')) rest = digits.slice(1);
  else if (/^[67]/.test(digits)) rest = digits;
  else return null;

  if (!/^[67]\d{8}$/.test(rest)) return null;
  return `+255${rest}`;
}

export function isValidPhone(input: string): boolean {
  return normalizePhone(input) !== null;
}

/** `+255712345678` → `0712 345 678` for display back to the renter. */
export function displayPhone(e164: string): string {
  const m = /^\+255(\d{3})(\d{3})(\d{3})$/.exec(e164);
  if (!m) return e164;
  return `0${m[1]} ${m[2]} ${m[3]}`;
}

export const PIN_PATTERN = /^\d{4,6}$/;

export function isValidPin(pin: string): boolean {
  return PIN_PATTERN.test(pin);
}

/**
 * One line of copy for any thrown error, in the reader's language.
 *
 * The backend's own `detail` (and `title`, when it sent one) is prose written
 * for this renter by the API and is shown verbatim — never re-translated.
 * Everything the client composes itself comes from the dictionaries: the
 * "try again in N minutes" wording the flows call for, the offline line, and
 * the generic per-status fallbacks.
 */
export function errorMessage(t: Translator, err: unknown, fallbackRetryMinutes = 10): string {
  if (err instanceof ApiError) {
    if (err.status === 429) {
      if (err.detail) return err.detail;
      const minutes = err.retryAfterSeconds
        ? Math.max(1, Math.ceil(err.retryAfterSeconds / 60))
        : fallbackRetryMinutes;
      return t.n('error.retryMinutes', minutes);
    }
    if (err.detail) return err.detail;
    if (err.is('upload_failed')) return t('error.upload');
    if (err.is('signature_too_large')) return t('error.signatureTooLarge');
    if (err.serverTitle) return err.serverTitle;
    return statusText(t, err.status);
  }
  if (err instanceof Error && err.message) return err.message;
  return t('error.generic');
}

/** Translated stand-in for a problem the backend did not describe itself. */
export function statusText(t: Translator, status: number): string {
  if (status === 0) return t('error.status.offline');
  if (status === 401 || status === 403 || status === 404 || status === 409 || status === 429) {
    return t(`error.status.${status}`);
  }
  if (status >= 500) return t('error.status.500');
  return t('error.status.other');
}

/** Seconds → `1:05`, for the OTP resend cooldown. */
export function countdown(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}:${String(s).padStart(2, '0')}` : `${s}s`;
}

/* ------------------------------------------------------------------ */
/* Money                                                               */
/* ------------------------------------------------------------------ */

/** `250000` → `250,000`. Amounts are whole TZS; the API never sends cents. */
export function formatAmount(amount: number): string {
  return new Intl.NumberFormat('en-GB').format(Math.round(amount));
}

/** `250000` → `TZS 250,000`. */
export function money(amount: number, currency = 'TZS'): string {
  return `${currency} ${formatAmount(amount)}`;
}

/** `30` → `30 days` / `siku 30`. */
export function days(t: Translator, n: number): string {
  return t.n('common.days', n, { count: formatAmount(n) });
}

/** The unit's headline price: `TZS 250,000 / 30 days`. */
export function priceLine(
  t: Translator,
  amount: number,
  periodDays: number,
  currency = 'TZS',
): string {
  return `${money(amount, currency)} / ${days(t, periodDays)}`;
}

/* ------------------------------------------------------------------ */
/* Dates                                                               */
/* ------------------------------------------------------------------ */
/* Dates on the wire are plain `YYYY-MM-DD`. All arithmetic runs in UTC so
 * a renter in UTC+3 never sees a day slip. */

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})/;

function parseDate(iso: string): Date | null {
  const m = DATE_RE.exec(iso);
  if (!m) return null;
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return Number.isNaN(d.getTime()) ? null : d;
}

function toIso(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/** Today in the renter's own timezone, as `YYYY-MM-DD`. */
export function todayIso(): string {
  const now = new Date();
  return toIso(new Date(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())));
}

/** `YYYY-MM-DD` + n days, same format. Returns the input if unparseable. */
export function addDays(iso: string, n: number): string {
  const d = parseDate(iso);
  if (!d) return iso;
  d.setUTCDate(d.getUTCDate() + n);
  return toIso(d);
}

/**
 * `2026-09-08` → `8 Sep 2026` (en) / `8 Sep 2026` in Swahili month names (sw).
 * The shared runtime owns the Intl call so every app formats dates the same
 * way; this wrapper only adds the ledger's em dash for a missing date.
 */
export function formatDate(locale: Locale, iso: string | null | undefined): string {
  if (!iso) return '—';
  if (!parseDate(iso)) return iso;
  return formatDateIntl(locale, iso);
}

/* ------------------------------------------------------------------ */
/* Signatures                                                          */
/* ------------------------------------------------------------------ */

/**
 * Last four digits of a server-masked phone, for the signature block's
 * "via phone •••1234". Returns null when the mask hides everything.
 */
export function phoneLast4(masked: string | null | undefined): string | null {
  if (!masked) return null;
  const digits = masked.replace(/\D/g, '');
  return digits.length >= 4 ? digits.slice(-4) : null;
}
