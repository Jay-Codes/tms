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

import { useT, type Translator } from '@tms/ui';
import type { MySchedule, PaymentSchedule } from '../lib/api';
import { money } from '../lib/format';

type ScheduleLike = Pick<PaymentSchedule, 'status' | 'amount' | 'paid_amount'> & {
  days_overdue?: number;
};

export function scheduleStatusText(t: Translator, schedule: ScheduleLike): string {
  switch (schedule.status) {
    case 'paid':
      return t('schedule.paid');
    case 'overdue':
      return t('schedule.overdue');
    case 'partial':
      return t('schedule.partial', {
        paid: money(schedule.paid_amount ?? 0),
        total: money(schedule.amount),
      });
    case 'waived':
      return t('schedule.waived');
    default:
      return t('schedule.due');
  }
}

export function ScheduleMark({ schedule }: { schedule: ScheduleLike }) {
  const t = useT();
  const text = scheduleStatusText(t, schedule);

  if (schedule.status === 'paid') {
    return <span className="stamp stamp-paid">{t('schedule.paid')}</span>;
  }

  if (schedule.status === 'overdue') {
    const late = schedule.days_overdue;
    return (
      <>
        <span className="stamp stamp-overdue">{t('schedule.overdue')}</span>
        {typeof late === 'number' && late > 0 && (
          <>
            <br />
            <span className="pencil">{t.n('schedule.daysLate', late)}</span>
          </>
        )}
        {(schedule.paid_amount ?? 0) > 0 && (
          <>
            <br />
            <span className="pencil">
              {t('schedule.paidAmount', { amount: money(schedule.paid_amount) })}
            </span>
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
  const t = useT();
  if (schedule.status === 'partial') {
    return (
      <span className="pencil">
        {t('schedule.partialPaid', {
          paid: money(schedule.paid_amount ?? 0),
          total: money(schedule.amount),
        })}
      </span>
    );
  }
  return <ScheduleMark schedule={schedule} />;
}
