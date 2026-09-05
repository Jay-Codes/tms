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

import { addDays } from './format';
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
  period: Pick<OfferedPeriod, 'days'>,
  termDays: number,
  startDate: string,
): Preview | null {
  if (!price || period.days <= 0 || termDays <= 0) return null;

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

/** Quick picks for tenancy length: the chosen period ×1, ×3, ×6, ×12. */
export const TERM_MULTIPLIERS = [1, 3, 6, 12] as const;

export function termQuickPicks(periodDays: number): number[] {
  return TERM_MULTIPLIERS.map((m) => periodDays * m);
}
