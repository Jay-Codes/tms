/**
 * Number and date formatting for the charts (Phase 11).
 *
 * Charts print money twice: compact on an axis (`1.2M`, `450k`) and in full in
 * a tooltip (`TZS 1,234,567`). Both live here so an axis tick and the tooltip
 * under it can never disagree about what a figure is.
 *
 * Dates are `YYYY-MM-DD` bucket starts and are parsed as UTC — the same rule
 * `period.ts` follows, so a browser in Dar es Salaam and one in London label a
 * bucket identically.
 */

export type Bucket = 'day' | 'week' | 'month';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function toUtc(iso: string): Date | null {
  const d = new Date(iso.length === 10 ? `${iso}T00:00:00Z` : iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** `1_234_567` → `1.2M`, `450_000` → `450k`, `-820` → `-820`. */
export function compactNumber(n: number): string {
  if (!Number.isFinite(n)) return '—';
  const sign = n < 0 ? '-' : '';
  const a = Math.abs(n);
  const unit = (v: number, suffix: string) => {
    const s = v >= 100 ? v.toFixed(0) : v.toFixed(1);
    return `${sign}${s.replace(/\.0$/, '')}${suffix}`;
  };
  if (a >= 1e9) return unit(a / 1e9, 'B');
  if (a >= 1e6) return unit(a / 1e6, 'M');
  if (a >= 1e3) return unit(a / 1e3, 'k');
  return `${sign}${Math.round(a)}`;
}

/** Axis-sized money: `TZS 1.2M`. The currency is stated once, on the axis. */
export function compactTZS(n: number): string {
  return `TZS ${compactNumber(n)}`;
}

/** Full money, the way the ledger writes it: `TZS 1,234,567`. */
export function fullTZS(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(Number(n))) return '—';
  return `TZS ${Math.round(Number(n)).toLocaleString('en-US')}`;
}

/** `0.842` → `84%`. Null (no denominator) prints an em dash, never `0%`. */
export function pctLabel(v: number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return '—';
  return `${(v * 100).toFixed(digits)}%`;
}

/** A signed percentage change: `+12%`, `-4%`, `—` when there is no comparison. */
export function changeLabel(pct: number | null | undefined, digits = 0): string {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return '—';
  const v = Number(pct.toFixed(digits));
  return `${v > 0 ? '+' : ''}${v}%`;
}

/** The short label under a bucket: `5 Sep` for days/weeks, `Sep` for months. */
export function bucketTick(start: string, bucket: Bucket): string {
  const d = toUtc(start);
  if (!d) return start;
  if (bucket === 'month') {
    return d.getUTCMonth() === 0 ? `${MONTHS[0]} ${d.getUTCFullYear()}` : MONTHS[d.getUTCMonth()];
  }
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
}

/** The tooltip heading: `5 Sep 2026`, `Week of 5 Sep 2026`, `Sep 2026`. */
export function bucketHeading(start: string, bucket: Bucket): string {
  const d = toUtc(start);
  if (!d) return start;
  const day = `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
  if (bucket === 'month') return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
  if (bucket === 'week') return `Week of ${day}`;
  return day;
}

/**
 * How many ticks to skip so labels never collide. `slot` is the pixels each
 * bucket owns; a label needs roughly `min` of them.
 */
export function tickStride(count: number, slot: number, min = 56): number {
  if (count <= 1 || slot >= min) return 1;
  return Math.max(1, Math.ceil(min / Math.max(1, slot)));
}
