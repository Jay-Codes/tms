'use client';

/**
 * Record an offline payment (FLOWS flow 7, landlord step 1).
 *
 * The money has already changed hands somewhere else — cash, a bank transfer,
 * mobile money — and this sheet is only the record of it. It is deliberately
 * the *same* sheet everywhere it is opened from (a schedule row on `/payments`,
 * a row on the contract), so a landlord learns one form.
 *
 * The one piece of judgement it asks for is overpayment. The backend refuses an
 * over-large amount with `409 overpay_confirm_required` rather than guessing,
 * and this sheet turns that refusal into the confirm prompt FLOWS 7 step 2
 * calls for, then resubmits the identical body with `allow_overpay_rollover`.
 * Nothing is decided client-side: the excess, the next schedule and the
 * allocation all come from the server.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  PAYMENT_METHODS,
  contractsApi,
  isUnsettled,
  overpayPrompt,
  paymentsApi,
  remainingOn,
  toApiError,
  type OverpayPrompt,
  type PaymentInput,
  type PaymentMethod,
  type PaymentResult,
  type Schedule,
} from '../lib/api';
import { datetimeLocalToRfc3339, fmtDate, fmtTZS, nowDatetimeLocal } from '../lib/format';
import { TableScroll, useT, type Translator } from '@tms/ui';

export interface RecordPaymentTarget {
  contractId: string;
  /** "Room 2 · Mbezi Court — Asha Juma", printed at the head of the sheet. */
  label?: string;
  /** Prefilled when the sheet was opened from a schedule row. */
  scheduleId?: string;
}

/** Earliest unsettled row — the target when the landlord did not pick one. */
function earliestUnpaid(rows: Schedule[]): Schedule | null {
  const open = rows.filter(isUnsettled);
  if (open.length === 0) return null;
  return open.reduce((a, b) => (a.due_date <= b.due_date ? a : b));
}

function scheduleOptionLabel(t: Translator, s: Schedule): string {
  const owing = remainingOn(s);
  return `${fmtDate(s.due_date)} — ${fmtTZS(s.amount)}${
    s.paid_amount ? ` (${t('payments.amount_still_owing', { amount: fmtTZS(owing) })})` : ''
  }${s.status === 'overdue' ? ` · ${t('payments.schedule_status.overdue')}` : ''}`;
}

function AppliedRows({ result }: { result: PaymentResult }) {
  const t = useT();
  const applied = result.payment?.applied ?? [];
  const byId = new Map((result.schedules ?? []).map((s) => [s.id, s]));
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <p
        role="status"
        style={{
          display: 'flex',
          gap: 'var(--sp-3)',
          alignItems: 'center',
          color: 'var(--stamp-paid)',
          border: '1px solid var(--stamp-paid)',
          borderRadius: 'var(--radius-sm)',
          padding: 'var(--sp-3) var(--sp-4)',
          maxWidth: 'none',
        }}
      >
        <Icon icon="solar:check-circle-linear" width={20} />
        <span>{t('payments.record.done', { amount: fmtTZS(result.payment?.amount) })}</span>
      </p>

      {applied.length > 0 ? (
        <TableScroll label={t('payments.record.applied_to')}>
<table className="ledger">
          <thead>
            <tr>
              <th>{t('payments.record.applied_to')}</th>
              <th className="num">{t('common.amount')}</th>
              <th>{t('payments.record.now')}</th>
            </tr>
          </thead>
          <tbody>
            {applied.map((a) => {
              const s = byId.get(a.schedule_id);
              return (
                <tr key={a.schedule_id}>
                  <td>
                    {s
                      ? t('payments.record.due_on', { date: fmtDate(s.due_date) })
                      : t('payments.record.schedule')}
                  </td>
                  <td className="num">{fmtTZS(a.amount)}</td>
                  <td>
                    {s?.status === 'paid' ? (
                      <span className="stamp stamp-paid">{t('payments.schedule_status.paid')}</span>
                    ) : (
                      <span className="pencil">
                        {s?.status ? t(`payments.schedule_status.${s.status}`) : '—'}
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
</TableScroll>
      ) : null}
    </div>
  );
}

export function RecordPaymentSheet({
  open,
  target,
  onClose,
  onRecorded,
}: {
  open: boolean;
  target: RecordPaymentTarget | null;
  onClose: () => void;
  /** Fired once the payment is written, so the caller can refresh its ledger. */
  onRecorded: (result: PaymentResult) => void;
}) {
  const t = useT();
  const [schedules, setSchedules] = useState<Schedule[] | null>(null);
  const [scheduleId, setScheduleId] = useState('');
  const [amount, setAmount] = useState('');
  const [method, setMethod] = useState<PaymentMethod>('cash');
  const [reference, setReference] = useState('');
  const [paidAt, setPaidAt] = useState(nowDatetimeLocal());
  const [note, setNote] = useState('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [overpay, setOverpay] = useState<OverpayPrompt | null>(null);
  const [result, setResult] = useState<PaymentResult | null>(null);
  /** The landlord edited the box; stop overwriting it when the target moves. */
  const [amountTouched, setAmountTouched] = useState(false);

  const contractId = target?.contractId ?? '';

  // Reset on every open so a second payment never inherits the first one's box.
  useEffect(() => {
    if (!open) return;
    setSchedules(null);
    setScheduleId(target?.scheduleId ?? '');
    setAmount('');
    setAmountTouched(false);
    setMethod('cash');
    setReference('');
    setPaidAt(nowDatetimeLocal());
    setNote('');
    setError(null);
    setOverpay(null);
    setResult(null);
  }, [open, target?.contractId, target?.scheduleId]);

  // The ledger is read for two reasons: to offer a target when the sheet was
  // opened from the page head, and to prefill the amount with what is owed.
  useEffect(() => {
    if (!open || !contractId) return;
    const ac = new AbortController();
    contractsApi
      .schedules(contractId, ac.signal)
      .then((r) => setSchedules((r.items ?? []) as Schedule[]))
      .catch((e) => {
        if (!(e instanceof DOMException)) setSchedules([]);
      });
    return () => ac.abort();
  }, [open, contractId]);

  // Target and prefill, derived once the rows are in.
  useEffect(() => {
    if (!schedules) return;
    const chosen =
      schedules.find((s) => s.id === scheduleId) ?? earliestUnpaid(schedules) ?? schedules[0] ?? null;
    if (chosen && chosen.id !== scheduleId) setScheduleId(chosen.id);
    if (chosen && !amountTouched) setAmount(String(remainingOn(chosen) || chosen.amount || ''));
  }, [schedules, scheduleId, amountTouched]);

  const selected = (schedules ?? []).find((s) => s.id === scheduleId) ?? null;

  const send = useCallback(
    async (allowOverpay: boolean) => {
      setBusy(true);
      setError(null);
      try {
        const body: PaymentInput = {
          contract_id: contractId,
          schedule_id: scheduleId || undefined,
          amount: Math.round(Number(amount)),
          method,
          reference: reference.trim() || undefined,
          paid_at: datetimeLocalToRfc3339(paidAt),
          note: note.trim() || undefined,
        };
        if (allowOverpay) body.allow_overpay_rollover = true;
        const res = await paymentsApi.record(body);
        setOverpay(null);
        setResult(res);
        onRecorded(res);
      } catch (e) {
        const err = toApiError(e);
        // Not a failure — a question. FLOWS 7 step 2.
        const prompt = overpayPrompt(err);
        if (prompt) setOverpay(prompt);
        else setError(err);
      } finally {
        setBusy(false);
      }
    },
    [amount, contractId, method, note, onRecorded, paidAt, reference, scheduleId],
  );

  const amountNum = Math.round(Number(amount));
  const valid = contractId !== '' && Number.isFinite(amountNum) && amountNum > 0;

  return (
    <Sheet open={open} title={t('payments.record.title')} onClose={onClose} width={560}>
      {target?.label ? (
        <p style={{ marginBottom: 'var(--sp-4)', color: 'var(--ink-soft)' }}>{target.label}</p>
      ) : null}

      {result ? (
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <AppliedRows result={result} />
          <div>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              {t('common.done')}
            </button>
          </div>
        </div>
      ) : overpay ? (
        /* ------------------------- the confirm prompt ------------------------ */
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <p
            style={{
              display: 'flex',
              gap: 'var(--sp-3)',
              padding: 'var(--sp-3) var(--sp-4)',
              border: '1px solid var(--rule-strong)',
              borderRadius: 'var(--radius-sm)',
            }}
          >
            <Icon icon="solar:question-circle-linear" width={20} />
            <span>
              {overpay.next_schedule?.due_date
                ? t('payments.overpay.prompt_dated', {
                    amount: fmtTZS(overpay.excess),
                    date: fmtDate(overpay.next_schedule.due_date),
                  })
                : t('payments.overpay.prompt', { amount: fmtTZS(overpay.excess) })}
            </span>
          </p>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void send(true)}
            >
              {busy ? t('payments.record.busy') : t('payments.overpay.continue')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setOverpay(null)} disabled={busy}>
              {t('payments.overpay.change_amount')}
            </button>
          </div>
        </div>
      ) : (
        /* ------------------------------ the form ----------------------------- */
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void send(false);
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <ProblemNote error={error} />

          <Field
            id="rp_schedule"
            label={t('payments.record.schedule_label')}
            hint={
              schedules === null
                ? t('payments.record.schedule_loading')
                : selected
                  ? t('payments.record.schedule_owing', { amount: fmtTZS(remainingOn(selected)) })
                  : t('payments.record.schedule_none')
            }
            error={error?.errors.schedule_id}
          >
            <select
              id="rp_schedule"
              className="input"
              value={scheduleId}
              disabled={schedules === null || (schedules ?? []).length === 0}
              onChange={(e) => {
                setScheduleId(e.target.value);
                setAmountTouched(false);
              }}
            >
              {(schedules ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {scheduleOptionLabel(t, s)}
                </option>
              ))}
            </select>
          </Field>

          <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
            <Field id="rp_amount" label={t('payments.record.amount_label')} error={error?.errors.amount}>
              <input
                id="rp_amount"
                className="input num"
                type="number"
                min={1}
                step={1}
                value={amount}
                onChange={(e) => {
                  setAmount(e.target.value);
                  setAmountTouched(true);
                }}
              />
            </Field>
            <Field id="rp_method" label={t('payments.record.method_label')} error={error?.errors.method}>
              <select
                id="rp_method"
                className="input"
                value={method}
                onChange={(e) => setMethod(e.target.value as PaymentMethod)}
              >
                {PAYMENT_METHODS.map((m) => (
                  <option key={m.value} value={m.value}>
                    {t(`payments.method.${m.value}`)}
                  </option>
                ))}
              </select>
            </Field>
          </div>

          <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
            <Field
              id="rp_reference"
              label={t('payments.record.reference_label')}
              hint={t('payments.record.reference_hint')}
              error={error?.errors.reference}
            >
              <input
                id="rp_reference"
                className="input"
                maxLength={80}
                value={reference}
                onChange={(e) => setReference(e.target.value)}
                placeholder={t('payments.record.reference_placeholder')}
              />
            </Field>
            <Field id="rp_paid_at" label={t('payments.record.paid_at_label')} error={error?.errors.paid_at}>
              <input
                id="rp_paid_at"
                className="input"
                type="datetime-local"
                value={paidAt}
                onChange={(e) => setPaidAt(e.target.value)}
              />
            </Field>
          </div>

          <Field
            id="rp_note"
            label={t('common.note')}
            hint={t('payments.record.note_hint', { count: note.length })}
            error={error?.errors.note}
          >
            <textarea
              id="rp_note"
              className="input"
              rows={2}
              maxLength={500}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </Field>

          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="submit" className="btn btn-primary" disabled={busy || !valid}>
              {busy ? t('payments.record.busy') : t('payments.record.submit')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      )}
    </Sheet>
  );
}
