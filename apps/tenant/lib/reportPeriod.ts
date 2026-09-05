'use client';

/**
 * The reports window, remembered between visits — the same pattern the expense
 * ledger uses (`expensePeriod.ts`), under its own key so a landlord's expense
 * window and their reporting window are independent.
 *
 * It is the only piece of report state kept in the browser. Every figure comes
 * from the server, resolved against the window sent with the request.
 */

import { resolvePeriod, today, type PeriodValue } from '@tms/ui';

export const REPORT_PERIOD_KEY = 'tms.reports.period';

const ISO = /^\d{4}-\d{2}-\d{2}$/;

/** The stored window if it is still a sane one, otherwise the current month. */
export function loadReportPeriod(): PeriodValue {
  const fallback = resolvePeriod('month', today());
  if (typeof window === 'undefined') return fallback;
  try {
    const raw = window.localStorage.getItem(REPORT_PERIOD_KEY);
    if (!raw) return fallback;
    const p = JSON.parse(raw) as Partial<PeriodValue>;
    if (typeof p?.from === 'string' && typeof p?.to === 'string' && ISO.test(p.from) && ISO.test(p.to) && p.from < p.to) {
      return { cadence: (p.cadence ?? 'month') as PeriodValue['cadence'], from: p.from, to: p.to };
    }
  } catch {
    /* a corrupt entry is not worth a broken screen */
  }
  return fallback;
}

export function saveReportPeriod(value: PeriodValue): void {
  try {
    window.localStorage.setItem(REPORT_PERIOD_KEY, JSON.stringify(value));
  } catch {
    /* private mode — the picker still works, it just forgets */
  }
}

/**
 * The window as the API takes it. `cadence` rides along so the response can
 * echo a named window ("Q3 2026") rather than a pair of dates, but `from`/`to`
 * are always sent: they are what was actually asked for.
 */
export function periodQuery(p: PeriodValue): { cadence: string; from: string; to: string } {
  return { cadence: p.cadence, from: p.from, to: p.to };
}
