/**
 * Period arithmetic for the shared <PeriodPicker> (PLAN2 phase 9 / 11).
 *
 * Every window is a half-open range **[from, to)** on plain calendar dates,
 * exactly like the backend's `internal/period`: September 2026 is
 * `2026-09-01 → 2026-10-01`, and the last day a reader sees is `to` minus one
 * day. Dates are `YYYY-MM-DD` strings and the maths is done on the integers in
 * them — no `Date` parsing of local strings, so a browser in Dar es Salaam and
 * one in London resolve the same window.
 *
 * These functions are pure and have no React dependency; they are unit-tested
 * in `period.test.mts`.
 */

export type Cadence = 'month' | 'quarter' | 'half_year' | 'year' | 'custom';

export interface PeriodValue {
  cadence: Cadence;
  /** First day in the window, inclusive (YYYY-MM-DD). */
  from: string;
  /** First day *after* the window, exclusive (YYYY-MM-DD). */
  to: string;
}

export const CADENCES: readonly Cadence[] = ['month', 'quarter', 'half_year', 'year', 'custom'];

export const CADENCE_LABELS: Record<Cadence, string> = {
  month: 'Month',
  quarter: 'Quarter',
  half_year: '6 months',
  year: 'Year',
  custom: 'Custom',
};

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

interface Ymd {
  y: number;
  m: number; // 1-12
  d: number; // 1-31
}

const ISO = /^(\d{4})-(\d{2})-(\d{2})$/;

/** Parse `YYYY-MM-DD`. Throws on anything else — a bad window is a bug, not a value. */
export function parseDate(iso: string): Ymd {
  const m = ISO.exec(iso);
  if (!m) throw new Error(`period: expected YYYY-MM-DD, got ${JSON.stringify(iso)}`);
  return { y: Number(m[1]), m: Number(m[2]), d: Number(m[3]) };
}

export function formatDate({ y, m, d }: Ymd): string {
  return `${String(y).padStart(4, '0')}-${String(m).padStart(2, '0')}-${String(d).padStart(2, '0')}`;
}

/** Today in the browser's own calendar, as YYYY-MM-DD. */
export function today(now: Date = new Date()): string {
  return formatDate({ y: now.getFullYear(), m: now.getMonth() + 1, d: now.getDate() });
}

/** Day arithmetic via UTC, which has no DST and no local-midnight surprises. */
function toDayNumber(iso: string): number {
  const { y, m, d } = parseDate(iso);
  return Date.UTC(y, m - 1, d) / 86_400_000;
}

function fromDayNumber(n: number): string {
  const dt = new Date(n * 86_400_000);
  return formatDate({ y: dt.getUTCFullYear(), m: dt.getUTCMonth() + 1, d: dt.getUTCDate() });
}

export function addDays(iso: string, days: number): string {
  return fromDayNumber(toDayNumber(iso) + days);
}

/** Whole days in [from, to). */
export function daysBetween(from: string, to: string): number {
  return toDayNumber(to) - toDayNumber(from);
}

/** `a < b`, on dates. String comparison is enough for zero-padded ISO. */
export function isBefore(a: string, b: string): boolean {
  return a < b;
}

function addMonths(iso: string, months: number): string {
  const { y, m } = parseDate(iso);
  const total = y * 12 + (m - 1) + months;
  return formatDate({ y: Math.floor(total / 12), m: (total % 12) + 1, d: 1 });
}

/**
 * The window of `cadence` that contains `anchor`.
 *
 * `custom` has no natural window, so it keeps a month around the anchor and the
 * caller (the two date inputs) takes over from there.
 */
export function resolvePeriod(cadence: Cadence, anchor: string): PeriodValue {
  const { y, m } = parseDate(anchor);

  if (cadence === 'month') {
    const from = formatDate({ y, m, d: 1 });
    return { cadence, from, to: addMonths(from, 1) };
  }
  if (cadence === 'quarter') {
    const from = formatDate({ y, m: Math.floor((m - 1) / 3) * 3 + 1, d: 1 });
    return { cadence, from, to: addMonths(from, 3) };
  }
  if (cadence === 'half_year') {
    const from = formatDate({ y, m: m <= 6 ? 1 : 7, d: 1 });
    return { cadence, from, to: addMonths(from, 6) };
  }
  if (cadence === 'year') {
    const from = formatDate({ y, m: 1, d: 1 });
    return { cadence, from, to: addMonths(from, 12) };
  }
  const from = formatDate({ y, m, d: 1 });
  return { cadence: 'custom', from, to: addMonths(from, 1) };
}

/**
 * The window `delta` steps before (-1) or after (+1) `value`.
 *
 * A custom range has no cadence to step by, so it slides by its own length —
 * a 90-day range steps 90 days, which is what a reader comparing two custom
 * windows expects.
 */
export function shiftPeriod(value: PeriodValue, delta: number): PeriodValue {
  if (value.cadence === 'custom') {
    const span = Math.max(1, daysBetween(value.from, value.to));
    return { cadence: 'custom', from: addDays(value.from, span * delta), to: addDays(value.to, span * delta) };
  }
  const months = value.cadence === 'month' ? 1 : value.cadence === 'quarter' ? 3 : value.cadence === 'half_year' ? 6 : 12;
  return resolvePeriod(value.cadence, addMonths(value.from, months * delta));
}

function longDay(iso: string): string {
  const { m, d } = parseDate(iso);
  return `${d} ${MONTHS[m - 1]}`;
}

/**
 * What the picker prints: "Sep 2026", "Q3 2026", "Jul–Dec 2026", "2026",
 * "6 Sep – 5 Dec 2026". The end shown is the last day *inside* the window.
 */
export function periodLabel(value: PeriodValue): string {
  const start = parseDate(value.from);
  const lastIso = addDays(value.to, -1);
  const last = parseDate(lastIso);

  if (value.cadence === 'month') return `${MONTHS[start.m - 1]} ${start.y}`;
  if (value.cadence === 'quarter') return `Q${Math.floor((start.m - 1) / 3) + 1} ${start.y}`;
  if (value.cadence === 'half_year') return `${MONTHS[start.m - 1]}–${MONTHS[last.m - 1]} ${start.y}`;
  if (value.cadence === 'year') return String(start.y);

  if (start.y !== last.y) return `${longDay(value.from)} ${start.y} – ${longDay(lastIso)} ${last.y}`;
  if (start.m === last.m) return `${start.d} – ${last.d} ${MONTHS[last.m - 1]} ${last.y}`;
  return `${longDay(value.from)} – ${longDay(lastIso)} ${last.y}`;
}

/** The whole window sits before `min` (nothing left to step back to). */
export function endsBefore(value: PeriodValue, min: string | undefined): boolean {
  return !!min && isBefore(addDays(value.to, -1), min);
}

/** The whole window sits after `max`. */
export function startsAfter(value: PeriodValue, max: string | undefined): boolean {
  return !!max && isBefore(max, value.from);
}
