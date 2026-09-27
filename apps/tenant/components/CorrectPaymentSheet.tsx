'use client';

/**
 * Phase 24 — correct a payment recorded wrong (amount, date, method,
 * reference, or the wrong tenancy). The backend reverses the original and
 * records the corrected one in one transaction and texts the renter; the
 * sheet only collects what changed. Anything left as it was is not sent, so
 * the backend copies it from the original.
 */

import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  PAYMENT_METHODS,
  contractsApi,
  paymentsApi,
  toApiError,
  type CashMethod,
  type Contract,
  type Payment,
  type PaymentCorrection,
} from '../lib/api';
import { datetimeLocalToRfc3339, fmtTZS, rfc3339ToDatetimeLocal } from '../lib/format';

/**
 * Running tenancies a payment can be moved to — the first page of active and
 * of expiring contracts, which is every one for the orgs this is built for.
 */
function useRunningContracts(enabled: boolean): Contract[] {
  const [items, setItems] = useState<Contract[]>([]);
  useEffect(() => {
    if (!enabled) return;
    const ac = new AbortController();
    Promise.all([
      contractsApi.list({ status: 'active', limit: 200 }, ac.signal),
      contractsApi.list({ status: 'expiring', limit: 200 }, ac.signal),
    ])
      .then(([a, e]) => setItems([...(a.items ?? []), ...(e.items ?? [])]))
      .catch(() => setItems([]));
    return () => ac.abort();
  }, [enabled]);
  return items;
}

function CorrectForm({
  payment,
  onDone,
  onCancel,
}: {
  payment: Payment;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const contracts = useRunningContracts(true);
  const originalMethod = (PAYMENT_METHODS.some((m) => m.value === payment.method) ? payment.method : 'cash') as CashMethod;
  const [amount, setAmount] = useState(String(payment.amount));
  const [paidAt, setPaidAt] = useState(rfc3339ToDatetimeLocal(payment.paid_at));
  const [method, setMethod] = useState<CashMethod>(originalMethod);
  const [reference, setReference] = useState(payment.reference ?? '');
  const [contractId, setContractId] = useState(payment.contract_id);
  const [reason, setReason] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body: PaymentCorrection = { reason: reason.trim() };
    const n = Math.round(Number(amount));
    if (n !== payment.amount) body.amount = n;
    if (paidAt !== rfc3339ToDatetimeLocal(payment.paid_at)) body.paid_at = datetimeLocalToRfc3339(paidAt);
    if (method !== payment.method) body.method = method;
    if (reference.trim() !== (payment.reference ?? '')) body.reference = reference.trim();
    if (contractId !== payment.contract_id) body.contract_id = contractId;
    try {
      await paymentsApi.correct(payment.id, body);
      onDone();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  // The payment's own contract stays first even if it is no longer running.
  const others = contracts.filter((c) => c.id !== payment.contract_id);

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('payments.correct.lead')}</p>

      <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="pc_amount"
          label={t('payments.correct.amount')}
          hint={amount ? fmtTZS(Number(amount)) : undefined}
          error={error?.errors.amount}
        >
          <input
            id="pc_amount"
            className="input num"
            type="number"
            min={1}
            step={1}
            inputMode="numeric"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
        </Field>
        <Field id="pc_paid_at" label={t('payments.correct.paid_at')} error={error?.errors.paid_at}>
          <input
            id="pc_paid_at"
            className="input"
            type="datetime-local"
            value={paidAt}
            onChange={(e) => setPaidAt(e.target.value)}
          />
        </Field>
        <Field id="pc_method" label={t('payments.correct.method')} error={error?.errors.method}>
          <select id="pc_method" className="input" value={method} onChange={(e) => setMethod(e.target.value as CashMethod)}>
            {PAYMENT_METHODS.map((m) => (
              <option key={m.value} value={m.value}>
                {t(`payments.method.${m.value}`)}
              </option>
            ))}
          </select>
        </Field>
        <Field id="pc_reference" label={t('payments.correct.reference')} error={error?.errors.reference}>
          <input
            id="pc_reference"
            className="input"
            maxLength={80}
            value={reference}
            onChange={(e) => setReference(e.target.value)}
          />
        </Field>
      </div>

      <Field
        id="pc_contract"
        label={t('payments.correct.contract')}
        hint={t('payments.correct.contract_hint')}
        error={error?.errors.contract_id}
      >
        <select id="pc_contract" className="input" value={contractId} onChange={(e) => setContractId(e.target.value)}>
          <option value={payment.contract_id}>{t('payments.correct.same_contract')}</option>
          {others.map((c) => (
            <option key={c.id} value={c.id}>
              {c.unit?.name} · {c.unit?.property_name} — {c.renter?.full_name}
            </option>
          ))}
        </select>
      </Field>

      <Field
        id="pc_reason"
        label={t('payments.correct.reason')}
        hint={`${reason.length}/200`}
        error={error?.errors.reason}
      >
        <textarea
          id="pc_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('payments.correct.reason_ph')}
        />
      </Field>

      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || !reason.trim() || !(Number(amount) > 0)}
        >
          {busy ? t('common.saving') : t('payments.correct.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

export function CorrectPaymentSheet({
  payment,
  onClose,
  onDone,
}: {
  payment: Payment | null;
  onClose: () => void;
  /** Fired after the correction is written, so the caller can re-read its ledger. */
  onDone: () => void;
}) {
  const t = useT();
  return (
    <Sheet open={payment !== null} title={t('payments.correct.title')} onClose={onClose} width={560}>
      {payment ? <CorrectForm key={payment.id} payment={payment} onDone={onDone} onCancel={onClose} /> : null}
    </Sheet>
  );
}
