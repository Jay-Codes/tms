'use client';

/**
 * The one place a schedule row's state turns into ink (SPEC §2.0, API.md
 * Phase 5): a stamp only for what has actually happened.
 *
 *   paid    → stamp
 *   overdue → red stamp
 *   pending → pencil
 *   partial → pencil, "TZS x of y"
 *   waived  → pencil
 *
 * Home and the payments tab both render through here so the two screens can
 * never drift apart.
 */

import type { MySchedule, PaymentSchedule } from '../lib/api';
import { money } from '../lib/format';

type ScheduleLike = Pick<PaymentSchedule, 'status' | 'amount' | 'paid_amount'> & {
  days_overdue?: number;
};

export function scheduleStatusText(schedule: ScheduleLike): string {
  switch (schedule.status) {
    case 'paid':
      return 'Paid';
    case 'overdue':
      return 'Overdue';
    case 'partial':
      return `${money(schedule.paid_amount ?? 0)} of ${money(schedule.amount)}`;
    case 'waived':
      return 'Waived';
    default:
      return 'Due';
  }
}

export function ScheduleMark({ schedule }: { schedule: ScheduleLike }) {
  const text = scheduleStatusText(schedule);

  if (schedule.status === 'paid') {
    return <span className="stamp stamp-paid">Paid</span>;
  }

  if (schedule.status === 'overdue') {
    const late = schedule.days_overdue;
    return (
      <>
        <span className="stamp stamp-overdue">Overdue</span>
        {typeof late === 'number' && late > 0 && (
          <>
            <br />
            <span className="pencil">
              {late} day{late === 1 ? '' : 's'} late
            </span>
          </>
        )}
        {(schedule.paid_amount ?? 0) > 0 && (
          <>
            <br />
            <span className="pencil">{money(schedule.paid_amount)} paid</span>
          </>
        )}
      </>
    );
  }

  return <span className="pencil">{text}</span>;
}

/**
 * The home screen's one-line chip for the next payment. Same ink rules, but
 * a partial row also says how much is still owed rather than only how much
 * has landed — that is the number a renter is about to act on.
 */
export function NextDueChip({ schedule }: { schedule: MySchedule }) {
  if (schedule.status === 'partial') {
    return (
      <span className="pencil">
        {money(schedule.paid_amount ?? 0)} of {money(schedule.amount)} paid
      </span>
    );
  }
  return <ScheduleMark schedule={schedule} />;
}
