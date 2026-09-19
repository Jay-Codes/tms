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
import type { MySchedule, PaymentSchedule, ProofStatus } from '../lib/api';
import { daysUntil, money } from '../lib/format';

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

/**
 * Phase 16 (§16.3): the countdown itself — how many days there are before the
 * money is expected, in the stamp colours. `days_until_due` comes from the
 * API (counted on the Dar es Salaam wall clock); a row without it is counted
 * from `due_date` here purely so the chip still says something.
 *
 * A proof already sent outranks the countdown: once a renter has handed over
 * a receipt, what they are waiting on is their landlord, not the calendar.
 */
export function CountdownChip({
  schedule,
  awaiting = false,
}: {
  schedule?: MySchedule | null;
  /** A still-`submitted` proof covers this row. */
  awaiting?: boolean;
}) {
  const t = useT();

  if (awaiting || schedule?.proof?.status === 'submitted') {
    return <span className="pencil">{t('due.awaiting')}</span>;
  }
  if (!schedule) return <span className="pencil">{t('due.nothing')}</span>;
  if (schedule.status === 'paid') {
    return <span className="stamp stamp-paid">{t('schedule.paid')}</span>;
  }
  if (schedule.status === 'waived') {
    return <span className="pencil">{t('schedule.waived')}</span>;
  }

  const left =
    typeof schedule.days_until_due === 'number'
      ? schedule.days_until_due
      : daysUntil(schedule.due_date);
  if (left === null) return <ScheduleMark schedule={schedule} />;

  if (left < 0) {
    return <span className="stamp stamp-overdue">{t.n('due.overdue', -left)}</span>;
  }
  if (left === 0) return <span className="stamp">{t('due.today')}</span>;
  return <span className="pencil">{t.n('due.in', left)}</span>;
}

/** Where a claim stands: pencilled while it waits, stamped once answered. */
export function ProofChip({ status }: { status: ProofStatus }) {
  const t = useT();
  if (status === 'accepted') {
    return <span className="stamp stamp-paid">{t('proof.status.accepted')}</span>;
  }
  if (status === 'rejected') {
    return <span className="stamp stamp-overdue">{t('proof.status.rejected')}</span>;
  }
  return <span className="pencil">{t('proof.status.submitted')}</span>;
}
