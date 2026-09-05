'use client';

/**
 * Shared ledgers and marks for the Phase 5 payment screens (SPEC §2.0): what
 * has happened is stamped (Paid, Overdue, Reversed), what is still waiting is
 * only pencilled (pending, part paid).
 *
 * Both tables here are pure renderers — the caller owns the fetching, so the
 * same ledger serves `/payments`, a contract and a renter's record.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useState } from 'react';
import { ScheduleStatusStamp } from './ContractBits';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  type ApiError,
  type Payment,
  paymentWho,
  type Schedule,
  isUnsettled,
  remainingOn,
} from '../lib/api';
import { fmtDate, fmtDateTime, fmtTZS } from '../lib/format';
import { TableScroll, useT, type Translator } from '@tms/ui';

/**
 * `methodLabel()` in lib/api is English-only, so the label is looked up here
 * instead — an unknown method still prints as it arrived.
 */
export function methodText(t: Translator, m: string | null | undefined): string {
  if (!m) return '—';
  const key = `payments.method.${m}`;
  const label = t(key);
  return label === key ? String(m).replace(/_/g, ' ') : label;
}

/** A reversed payment is struck through in the ledger and stamped. */
export function PaymentStatusStamp({ status }: { status: string }) {
  const t = useT();
  if (status === 'reversed') return <span className="stamp stamp-overdue">{t('payments.status.reversed')}</span>;
  return <span className="stamp stamp-paid">{t('payments.status.recorded')}</span>;
}

/** "12 days late" under an overdue row; nothing at all when it is not. */
export function DaysOverdue({ days }: { days: number | null | undefined }) {
  const t = useT();
  if (!days || days <= 0) return null;
  return (
    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--stamp-overdue)' }}>
      {t.n('payments.days_late', days)}
    </div>
  );
}

function Loading({ cols, text }: { cols: number; text: string }) {
  return (
    <tr>
      <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
        {text}
      </td>
    </tr>
  );
}

/* ------------------------------- schedules -------------------------------- */

/**
 * The schedule ledger across contracts. Each row names who owes what on which
 * unit, and carries the one action a landlord can take on it.
 */
export function SchedulesTable({
  items,
  loading,
  emptyText,
  onRecord,
}: {
  items: Schedule[] | null;
  loading?: boolean;
  emptyText?: string;
  onRecord?: (s: Schedule) => void;
}) {
  const t = useT();
  const cols = 7;
  const rows = items ?? [];
  const empty = emptyText ?? t('payments.schedules.empty');
  return (
    <TableScroll label={t('payments.schedules.table_label')}>
    <table className="ledger">
      <thead>
        <tr>
          <th>{t('common.renter')}</th>
          <th>{t('common.unit')}</th>
          <th>{t('payments.col.due')}</th>
          <th className="num">{t('common.amount')}</th>
          <th className="num">{t('payments.col.paid')}</th>
          <th>{t('common.status')}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {items === null || loading ? (
          <Loading cols={cols} text={t('common.loading')} />
        ) : rows.length === 0 ? (
          <Loading cols={cols} text={empty} />
        ) : (
          <>
            {rows.map((s) => (
              <tr key={s.id}>
                <td style={{ fontWeight: 600 }}>
                  {s.contract?.renter_user_id ? (
                    <Link href={`/renters/${s.contract.renter_user_id}`} style={{ color: 'inherit' }}>
                      {s.contract.renter_name}
                    </Link>
                  ) : (
                    (s.contract?.renter_name ?? '—')
                  )}
                </td>
                <td>
                  {s.contract?.id ? (
                    <Link href={`/contracts/${s.contract.id}`} style={{ color: 'inherit' }}>
                      {s.contract.unit_name}
                    </Link>
                  ) : (
                    (s.contract?.unit_name ?? '—')
                  )}
                  <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                    {s.contract?.property_name ?? ''}
                  </div>
                </td>
                <td>
                  {fmtDate(s.due_date)}
                  <DaysOverdue days={s.days_overdue} />
                </td>
                <td className="num">{fmtTZS(s.amount)}</td>
                <td className="num">
                  {s.paid_amount ? fmtTZS(s.paid_amount) : <span className="pencil">—</span>}
                </td>
                <td>
                  <ScheduleStatusStamp status={s.status} />
                  {s.status === 'partial' ? (
                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                      {t('payments.amount_still_owing', { amount: fmtTZS(remainingOn(s)) })}
                    </div>
                  ) : null}
                </td>
                <td>
                  {onRecord && isUnsettled(s) ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      style={{ minHeight: 36 }}
                      onClick={() => onRecord(s)}
                    >
                      {t('payments.record.action')}
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
            <tr className="total">
              <td colSpan={3}>{t('common.total')}</td>
              <td className="num">{fmtTZS(rows.reduce((sum, s) => sum + (s.amount ?? 0), 0))}</td>
              <td className="num">{fmtTZS(rows.reduce((sum, s) => sum + (s.paid_amount ?? 0), 0))}</td>
              <td colSpan={2} />
            </tr>
          </>
        )}
      </tbody>
    </table>
    </TableScroll>
  );
}

/* -------------------------------- payments -------------------------------- */

export function ReverseForm({
  payment,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  payment: Payment;
  busy: boolean;
  error: ApiError | null;
  onSubmit: (reason: string) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [reason, setReason] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(reason.trim());
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      <p
        style={{
          display: 'flex',
          gap: 'var(--sp-3)',
          padding: 'var(--sp-3) var(--sp-4)',
          border: '1px solid var(--stamp-overdue)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:danger-triangle-linear" width={20} />
        <span>{t('payments.reverse.warning', { amount: fmtTZS(payment.amount) })}</span>
      </p>
      <Field
        id="rev_reason"
        label={t('payments.reverse.reason_label')}
        hint={`${reason.length}/200`}
        error={error?.errors.reason}
      >
        <textarea
          id="rev_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('payments.reverse.reason_placeholder')}
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('payments.reverse.busy') : t('payments.reverse.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * Payment history. The two identity columns are switched off where they would
 * only repeat themselves: no renter column on a renter's own record, and
 * neither on a single contract.
 */
export function PaymentsTable({
  items,
  loading,
  emptyText,
  showRenter = true,
  showUnit = true,
  onReverse,
}: {
  items: Payment[] | null;
  loading?: boolean;
  emptyText?: string;
  showRenter?: boolean;
  showUnit?: boolean;
  onReverse?: (p: Payment) => void;
}) {
  const t = useT();
  const cols = 6 + (showRenter ? 1 : 0) + (showUnit ? 1 : 0);
  const rows = items ?? [];
  const empty = emptyText ?? t('payments.empty.history');
  return (
    <TableScroll label={t('payments.table_label')}>
    <table className="ledger">
      <thead>
        <tr>
          <th>{t('payments.col.paid_at')}</th>
          {showRenter ? <th>{t('common.renter')}</th> : null}
          {showUnit ? <th>{t('common.unit')}</th> : null}
          <th className="num">{t('common.amount')}</th>
          <th>{t('payments.col.method')}</th>
          <th>{t('payments.col.recorded_by')}</th>
          <th>{t('common.status')}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {items === null || loading ? (
          <Loading cols={cols} text={t('common.loading')} />
        ) : rows.length === 0 ? (
          <Loading cols={cols} text={empty} />
        ) : (
          <>
            {rows.map((p) => {
              const reversed = p.status === 'reversed';
              const who = paymentWho(p);
              return (
                <tr key={p.id} style={reversed ? { color: 'var(--ink-soft)' } : undefined}>
                  <td>{fmtDateTime(p.paid_at)}</td>
                  {showRenter ? (
                    <td>
                      {who.renterUserId ? (
                        <Link href={`/renters/${who.renterUserId}`} style={{ color: 'inherit' }}>
                          {who.renterName}
                        </Link>
                      ) : (
                        who.renterName
                      )}
                    </td>
                  ) : null}
                  {showUnit ? (
                    <td>
                      <Link href={`/contracts/${p.contract_id}`} style={{ color: 'inherit' }}>
                        {who.unitName}
                      </Link>
                      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                        {who.propertyName}
                      </div>
                    </td>
                  ) : null}
                  <td className="num" style={reversed ? { textDecoration: 'line-through' } : undefined}>
                    {fmtTZS(p.amount)}
                  </td>
                  <td>
                    {methodText(t, p.method)}
                    {p.reference ? (
                      <div className="num" style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                        {p.reference}
                      </div>
                    ) : null}
                  </td>
                  <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                    {p.recorded_by?.name ?? '—'}
                  </td>
                  <td>
                    <PaymentStatusStamp status={String(p.status)} />
                    {reversed && p.reversal_reason ? (
                      <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                        {p.reversal_reason}
                      </div>
                    ) : null}
                  </td>
                  <td>
                    {onReverse && !reversed ? (
                      <button
                        type="button"
                        className="btn btn-quiet"
                        style={{ minHeight: 36 }}
                        onClick={() => onReverse(p)}
                      >
                        {t('payments.reverse.action')}
                      </button>
                    ) : null}
                  </td>
                </tr>
              );
            })}
            <tr className="total">
              <td colSpan={cols - 5}>{t('payments.total_recorded')}</td>
              <td className="num">
                {fmtTZS(rows.filter((p) => p.status !== 'reversed').reduce((sum, p) => sum + (p.amount ?? 0), 0))}
              </td>
              <td colSpan={4} />
            </tr>
          </>
        )}
      </tbody>
    </table>
    </TableScroll>
  );
}

/** The reverse sheet, wired to whichever payment the caller picked. */
export function ReverseSheet({
  payment,
  busy,
  error,
  onClose,
  onSubmit,
}: {
  payment: Payment | null;
  busy: boolean;
  error: ApiError | null;
  onClose: () => void;
  onSubmit: (reason: string) => void;
}) {
  const t = useT();
  return (
    <Sheet open={payment !== null} title={t('payments.reverse.title')} onClose={onClose} width={520}>
      {payment ? (
        <ReverseForm payment={payment} busy={busy} error={error} onCancel={onClose} onSubmit={onSubmit} />
      ) : null}
    </Sheet>
  );
}
