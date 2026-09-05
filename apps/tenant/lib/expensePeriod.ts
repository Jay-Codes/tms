'use client';

/**
 * The expense window, remembered between visits.
 *
 * The ledger and a property's own expenses tab share one key on purpose: a
 * landlord who was looking at Q3 on `/expenses` and then opens a property is
 * still looking at Q3. It is the only piece of state the expense screens keep
 * in the browser — every figure comes from the server.
 */

import { resolvePeriod, today, type PeriodValue } from '@tms/ui';

export const EXPENSE_PERIOD_KEY = 'tms.expenses.period';

const ISO = /^\d{4}-\d{2}-\d{2}$/;

/** The stored window if it is still a sane one, otherwise the current month. */
export function loadExpensePeriod(): PeriodValue {
  const fallback = resolvePeriod('month', today());
  if (typeof window === 'undefined') return fallback;
  try {
    const raw = window.localStorage.getItem(EXPENSE_PERIOD_KEY);
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

export function saveExpensePeriod(value: PeriodValue): void {
  try {
    window.localStorage.setItem(EXPENSE_PERIOD_KEY, JSON.stringify(value));
  } catch {
    /* private mode — the picker still works, it just forgets */
  }
}
