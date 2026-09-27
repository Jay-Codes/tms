'use client';

/**
 * Phase 22.5 — settle-up when a tenancy ends, and the deposit ledger. The
 * backend computes every figure (the preview and the stored settlement are
 * the same computation); these pieces only put them in plain words and send
 * the landlord's choices back.
 */

import { useCallback, useEffect, useState } from 'react';
import { TableScroll, useT, type Translator } from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import { Facts } from './ContractBits';
import { methodText } from './PaymentBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  PAYMENT_METHODS,
  depositApi,
  exceedsDepositHeld,
  toApiError,
  type CashMethod,
  type DepositKind,
  type DepositLedger,
  type Settlement,
} from '../lib/api';
import { fmtDate, fmtTZS, todayISO } from '../lib/format';

/* ------------------------------ settlement ------------------------------- */

/** The settle-up as sentences, in the order the landlord reads a bill. */
export function settlementLines(t: Translator, s: Settlement): string[] {
  const lines: string[] = [];
  if (s.straddle) {
    lines.push(
      s.proration === 'pro_rata'
        ? t('settle.pro_rata', {
            lived: s.straddle.days_lived,
            days: s.straddle.period_days,
            amount: fmtTZS(s.straddle.charged),
          })
        : t('settle.full', { amount: fmtTZS(s.straddle.charged) }),
    );
  }
  lines.push(s.arrears > 0 ? t('settle.arrears', { amount: fmtTZS(s.arrears) }) : t('settle.arrears_none'));
  if (s.prepaid > 0) {
    const amount = fmtTZS(s.prepaid);
    lines.push(
      s.prepaid_action === 'refund'
        ? t('settle.prepaid_refund', { amount })
        : s.prepaid_action === 'forfeit'
          ? t('settle.prepaid_keep', { amount })
          : t('settle.prepaid_choose', { amount }),
    );
  }
  if (s.deposit_held > 0 || s.deposit_required > 0) {
    lines.push(
      t('settle.deposit', { held: fmtTZS(s.deposit_held), required: fmtTZS(s.deposit_required) }),
    );
  }
  return lines;
}

/** "The renter still owes …" / "You owe the renter …" / "Nothing is owed". */
export function settlementNet(t: Translator, s: Settlement): string {
  if (s.net > 0) return t('settle.net_renter', { amount: fmtTZS(s.net) });
  if (s.net < 0) {
    return s.deposit_held > 0
      ? t('settle.net_landlord_deposit', { amount: fmtTZS(-s.net) })
      : t('settle.net_landlord', { amount: fmtTZS(-s.net) });
  }
  return t('settle.net_zero');
}

export function SettlementSummary({ settlement }: { settlement: Settlement }) {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
      <ul style={{ margin: 0, paddingLeft: '1.2em', display: 'grid', gap: 'var(--sp-1)' }}>
        {settlementLines(t, settlement).map((line) => (
          <li key={line}>{line}</li>
        ))}
      </ul>
      <p style={{ fontWeight: 600 }}>{settlementNet(t, settlement)}</p>
    </div>
  );
}

/* -------------------------------- deposit -------------------------------- */

const KINDS: DepositKind[] = ['received', 'deduction', 'refund', 'applied_to_rent'];

function DepositForm({
  contractId,
  kind,
  held,
  onDone,
  onCancel,
}: {
  contractId: string;
  kind: DepositKind;
  held: number;
  onDone: (d: DepositLedger) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [amount, setAmount] = useState(kind === 'refund' || kind === 'applied_to_rent' ? String(held || '') : '');
  const [method, setMethod] = useState<CashMethod>('cash');
  const [reference, setReference] = useState('');
  const [reason, setReason] = useState('');
  const [date, setDate] = useState(todayISO());
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const needsMethod = kind === 'received' || kind === 'refund';
  const cap = exceedsDepositHeld(error);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await depositApi.record(contractId, {
        kind,
        amount: Math.round(Number(amount)),
        method: needsMethod ? method : undefined,
        reference: reference.trim() || undefined,
        reason: reason.trim() || undefined,
        occurred_at: date || undefined,
      });
      onDone(res.deposit);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t(`deposit.lead.${kind}`, { held: fmtTZS(held) })}</p>
      <Field
        id="dp_amount"
        label={t('deposit.amount')}
        hint={amount ? fmtTZS(Number(amount)) : undefined}
        error={cap !== null ? t('deposit.exceeds', { held: fmtTZS(cap) }) : error?.errors.amount}
      >
        <input
          id="dp_amount"
          className="input num"
          type="number"
          min={1}
          step={1}
          inputMode="numeric"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
        />
      </Field>
      {needsMethod ? (
        <Field id="dp_method" label={t('deposit.method')} error={error?.errors.method}>
          <select id="dp_method" className="input" value={method} onChange={(e) => setMethod(e.target.value as CashMethod)}>
            {PAYMENT_METHODS.map((m) => (
              <option key={m.value} value={m.value}>
                {t(`payments.method.${m.value}`)}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      {kind !== 'deduction' ? (
        <Field id="dp_reference" label={t('deposit.reference')} error={error?.errors.reference}>
          <input
            id="dp_reference"
            className="input"
            maxLength={80}
            value={reference}
            onChange={(e) => setReference(e.target.value)}
          />
        </Field>
      ) : null}
      <Field
        id="dp_reason"
        label={kind === 'deduction' ? t('deposit.reason_required') : t('deposit.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
        error={error?.errors.reason}
      >
        <textarea
          id="dp_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={kind === 'deduction' ? t('deposit.reason_ph') : undefined}
        />
      </Field>
      <Field id="dp_date" label={t('deposit.date')} error={error?.errors.occurred_at}>
        <input id="dp_date" className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || !(Number(amount) > 0) || (kind === 'deduction' && !reason.trim())}
        >
          {busy ? t('common.saving') : t(`deposit.submit.${kind}`)}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * The contract page's "Deposit" section: what the policy asks for, what came
 * in, what is held, and every movement. `onChanged` fires after a record —
 * applying the deposit to rent changes the rent book too.
 */
export function DepositSection({ contractId, onChanged }: { contractId: string; onChanged: () => void }) {
  const t = useT();
  const [ledger, setLedger] = useState<DepositLedger | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [kind, setKind] = useState<DepositKind | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setLedger((await depositApi.get(contractId, signal)).deposit);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [contractId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const held = ledger?.held ?? 0;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      <ProblemNote error={error} />
      {note ? <Note>{note}</Note> : null}
      {ledger ? (
        <>
          <Facts
            rows={[
              [t('deposit.required'), ledger.required === null ? t('deposit.required_none') : fmtTZS(ledger.required)],
              [t('deposit.received'), fmtTZS(ledger.received)],
              [t('deposit.held'), <strong key="held">{fmtTZS(ledger.held)}</strong>],
              ...(ledger.owed_beyond_deposit > 0
                ? ([[t('deposit.owed_beyond'), fmtTZS(ledger.owed_beyond_deposit)]] as [string, React.ReactNode][])
                : []),
            ]}
          />

          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
            {KINDS.map((k) => (
              <button
                key={k}
                type="button"
                className={k === 'received' ? 'btn btn-secondary' : 'btn btn-quiet'}
                onClick={() => {
                  setNote(null);
                  setKind(k);
                }}
                disabled={k !== 'received' && k !== 'deduction' && held <= 0}
              >
                {t(`deposit.action.${k}`)}
              </button>
            ))}
          </div>

          <TableScroll label={t('deposit.entries')}>
            <table className="ledger">
              <thead>
                <tr>
                  <th>{t('deposit.col.kind')}</th>
                  <th className="num">{t('common.amount')}</th>
                  <th>{t('deposit.col.date')}</th>
                  <th>{t('deposit.col.detail')}</th>
                </tr>
              </thead>
              <tbody>
                {ledger.entries.length === 0 ? (
                  <tr>
                    <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                      {t('deposit.empty')}
                    </td>
                  </tr>
                ) : (
                  ledger.entries.map((e) => (
                    <tr key={e.id}>
                      <td>{t(`deposit.kind.${e.kind}`)}</td>
                      <td className="num">{fmtTZS(e.amount)}</td>
                      <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{fmtDate(e.occurred_at)}</td>
                      <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                        {[e.method ? methodText(t, e.method) : null, e.reference, e.reason].filter(Boolean).join(' · ') ||
                          '—'}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </TableScroll>

          {ledger.rent_refunds.length ? (
            <>
              <h3 style={{ fontSize: 'var(--text-md)' }}>{t('deposit.rent_refunds')}</h3>
              <TableScroll label={t('deposit.rent_refunds')}>
                <table className="ledger">
                  <thead>
                    <tr>
                      <th>{t('deposit.col.date')}</th>
                      <th className="num">{t('common.amount')}</th>
                      <th>{t('deposit.col.detail')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ledger.rent_refunds.map((r) => (
                      <tr key={r.id}>
                        <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{fmtDate(r.refunded_at)}</td>
                        <td className="num">{fmtTZS(r.amount)}</td>
                        <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                          {[methodText(t, r.method), r.reference, r.reason].filter(Boolean).join(' · ')}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableScroll>
            </>
          ) : null}
        </>
      ) : error ? null : (
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      )}

      <Sheet open={kind !== null} title={kind ? t(`deposit.action.${kind}`) : ''} onClose={() => setKind(null)} width={480}>
        {kind ? (
          <DepositForm
            contractId={contractId}
            kind={kind}
            held={held}
            onCancel={() => setKind(null)}
            onDone={(d) => {
              setLedger(d);
              setKind(null);
              setNote(t('deposit.saved'));
              onChanged();
            }}
          />
        ) : null}
      </Sheet>
    </div>
  );
}
