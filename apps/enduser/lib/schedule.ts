/**
 * Schedule preview — DISPLAY ONLY.
 *
 * Flow 2 step 5 asks the renter to see "N payments of X" before they commit,
 * which means the numbers have to appear as they move the sliders, before any
 * request exists to ask the server about. So this mirrors the server's
 * proration arithmetic (SPEC §4, API.md `schedule_preview`) purely to paint
 * the preview table.
 *
 * It is never the source of truth: the moment `POST /units/{code}/link`
 * answers, the screen switches to the `schedule_preview` in the response and
 * this module's output is discarded. Nothing is submitted from here — the
 * request body carries only the renter's three choices (period, term, start).
 */

import { addDays, addMonths, daysBetween } from './format';
import type { OfferedPeriod, UnitPrice } from './api';

export interface PreviewRow {
  /** 1-based payment number. */
  n: number;
  /** `YYYY-MM-DD`. */
  due: string;
  amount: number;
  /** Covers fewer days than a full period — the truncated last payment. */
  partial: boolean;
  daysCovered: number;
}

export interface Preview {
  rows: PreviewRow[];
  count: number;
  total: number;
  /** Exclusive end of the tenancy: `start_date + term_days` (SPEC §4). */
  endDate: string;
}

/** The server's rule: `round(price.amount × days / price.period_days)`. */
export function prorate(price: UnitPrice, coveredDays: number): number {
  if (price.period_days <= 0) return 0;
  return Math.round((price.amount * coveredDays) / price.period_days);
}

/**
 * End date derived from a start date and a term length. SPEC §4 makes it
 * exclusive — `end_date = start_date + term_days` — so a 30-day term starting
 * 8 Sep ends 8 Oct, the day the next payment period would begin. This matches
 * the server's `end_date`, which wins once the request is created.
 */
export function deriveEndDate(startDate: string, termDays: number): string {
  return addDays(startDate, Math.max(0, termDays));
}

/**
 * Build the payment rows for a term. Payments fall every `period.days` from
 * the start date; the last one is truncated to whatever days remain.
 */
export function buildPreview(
  price: UnitPrice | null,
  period: Pick<OfferedPeriod, 'days' | 'months'>,
  termDays: number,
  startDate: string,
): Preview | null {
  if (!price || period.days <= 0 || termDays <= 0) return null;
  if (period.months) return buildCalendarPreview(price, period.months, termDays, startDate);

  const count = Math.ceil(termDays / period.days);
  const rows: PreviewRow[] = [];
  let total = 0;

  for (let i = 0; i < count; i += 1) {
    const covered = Math.min(period.days, termDays - i * period.days);
    const amount = prorate(price, covered);
    total += amount;
    rows.push({
      n: i + 1,
      due: addDays(startDate, i * period.days),
      amount,
      partial: covered !== period.days,
      daysCovered: covered,
    });
  }

  return { rows, count, total, endDate: deriveEndDate(startDate, termDays) };
}

/**
 * A calendar period (the server's GenerateCadence): rent falls due on the 1st
 * — the preview assumes the default billing day — each row running to the
 * day before the next billing day and charged a full period's rent whatever
 * the month's length. A start or an end between billing days is prorated
 * over the days of the cycle it falls in.
 */
function buildCalendarPreview(
  price: UnitPrice,
  months: number,
  termDays: number,
  startDate: string,
): Preview | null {
  const endDate = deriveEndDate(startDate, termDays);
  const perPeriod = prorate(price, 30 * months);
  const base = `${startDate.slice(0, 8)}01`;
  const rows: PreviewRow[] = [];
  let total = 0;
  let cursor = startDate;
  for (let k = 0; cursor < endDate; k += 1) {
    const cycleStart = addMonths(base, k * months);
    const cycleEnd = addMonths(base, (k + 1) * months);
    if (cycleEnd <= cursor) continue;
    const rowEnd = cycleEnd < endDate ? cycleEnd : endDate;
    const covered = daysBetween(cursor, rowEnd);
    const cycleDays = daysBetween(cycleStart, cycleEnd);
    const amount = covered === cycleDays ? perPeriod : Math.round((perPeriod * covered) / cycleDays);
    total += amount;
    rows.push({ n: rows.length + 1, due: cursor, amount, partial: covered !== cycleDays, daysCovered: covered });
    cursor = rowEnd;
  }
  return { rows, count: rows.length, total, endDate };
}

/** Quick picks for tenancy length: the chosen period ×1, ×3, ×6, ×12. */
export const TERM_MULTIPLIERS = [1, 3, 6, 12] as const;

export interface TermPick {
  days: number;
  /** Set when the pick is a whole number of calendar months. */
  months?: number;
}

/**
 * The quick picks for a period. A calendar period's are whole months from the
 * start date, so the tenancy ends on a billing day.
 */
export function termQuickPicks(
  period: Pick<OfferedPeriod, 'days' | 'months'>,
  startDate: string,
): TermPick[] {
  if (period.months) {
    return TERM_MULTIPLIERS.map((m) => {
      const months = m * period.months!;
      return { days: daysBetween(startDate, addMonths(startDate, months)), months };
    });
  }
  return TERM_MULTIPLIERS.map((m) => ({ days: period.days * m }));
}

/** The shortest term a period accepts: one period (28 days for a calendar month). */
export function minTermDays(period: Pick<OfferedPeriod, 'days' | 'months'>): number {
  return period.months ? 28 * period.months : period.days;
}
