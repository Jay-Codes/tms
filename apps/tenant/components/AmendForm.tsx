'use client';

/**
 * Phase 22.4 — amend or renew a running contract. The result is a new
 * contract the renter signs; the running one keeps governing until the
 * effective date, and on activation the backend moves money already paid for
 * later periods onto the new one. Nothing here computes any of that — the
 * form only offers the dates the backend will accept and sends what changed.
 */

import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { useTemplateList } from './TemplateBits';
import {
  ApiError,
  allowedEffectiveDates,
  contractsApi,
  periodsApi,
  toApiError,
  unwrapContract,
  type AmendInput,
  type Contract,
  type PaymentPeriod,
  type ScheduleRow,
} from '../lib/api';
import { fmtDate, fmtTZS, todayISO } from '../lib/format';

export type AmendMode = 'amend' | 'renew';

export function AmendForm({
  contract,
  schedules,
  mode,
  onDone,
  onCancel,
}: {
  contract: Contract;
  /** This contract's own periods; the future period starts are the choices. */
  schedules: ScheduleRow[];
  mode: AmendMode;
  onDone: (created: Contract) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const templates = useTemplateList();
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const today = todayISO();
  const starts = Array.from(
    new Set(schedules.map((s) => s.period_start).filter((d) => d >= today && d !== contract.end_date)),
  ).sort();

  const [effective, setEffective] = useState(mode === 'renew' ? contract.end_date : (starts[0] ?? contract.end_date));
  const [reason, setReason] = useState('');
  const [rent, setRent] = useState('');
  const [periodId, setPeriodId] = useState('');
  const [dueDay, setDueDay] = useState('');
  const [termDays, setTermDays] = useState(mode === 'renew' ? String(contract.term_days) : '');
  const [templateId, setTemplateId] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    periodsApi
      .list(false, ac.signal)
      .then((r) => setPeriods((r.items ?? []).filter((p) => p.active)))
      .catch(() => setPeriods([]));
    return () => ac.abort();
  }, []);

  const allowed = allowedEffectiveDates(error);
  const renewal = effective === contract.end_date;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    // Blank means "carry it from the running contract" — never sent.
    const body: AmendInput = { effective_date: effective, reason: reason.trim() };
    if (rent) body.rent_amount = Math.round(Number(rent));
    if (periodId) body.payment_period_id = periodId;
    if (dueDay) body.due_day = Math.round(Number(dueDay));
    if (termDays) body.term_days = Math.round(Number(termDays));
    if (templateId) body.template_id = templateId;
    try {
      onDone(unwrapContract(await contractsApi.amend(contract.id, body)));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      {allowed.length ? (
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {t('contracts.amend.allowed', { dates: allowed.map((d) => fmtDate(d)).join(', ') })}
        </p>
      ) : null}
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.amend.lead')}</p>

      <Field id="am_effective" label={t('contracts.amend.effective')} error={error?.errors.effective_date}>
        <select id="am_effective" className="input" value={effective} onChange={(e) => setEffective(e.target.value)}>
          {starts.map((d) => (
            <option key={d} value={d}>
              {fmtDate(d)}
            </option>
          ))}
          <option value={contract.end_date}>
            {t('contracts.amend.at_end', { date: fmtDate(contract.end_date) })}
          </option>
        </select>
      </Field>

      <Field
        id="am_reason"
        label={t('contracts.amend.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
        error={error?.errors.reason}
      >
        <textarea
          id="am_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={renewal ? t('contracts.amend.reason_ph_renew') : t('contracts.amend.reason_ph')}
        />
      </Field>

      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>{t('contracts.amend.optional_note')}</p>

      <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="am_rent"
          label={t('contracts.amend.rent', { days: contract.rent_period_days })}
          hint={rent ? fmtTZS(Number(rent)) : t('contracts.amend.now', { value: fmtTZS(contract.rent_amount) })}
          error={error?.errors.rent_amount}
        >
          <input
            id="am_rent"
            className="input num"
            type="number"
            min={1}
            step={1}
            inputMode="numeric"
            value={rent}
            onChange={(e) => setRent(e.target.value)}
          />
        </Field>
        <Field id="am_period" label={t('contracts.new.period')} error={error?.errors.payment_period_id}>
          <select id="am_period" className="input" value={periodId} onChange={(e) => setPeriodId(e.target.value)}>
            <option value="">
              {t('contracts.amend.keep', { value: contract.payment_period?.label ?? '—' })}
            </option>
            {periods.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </Field>
        <Field
          id="am_due_day"
          label={t('contracts.new.due_day')}
          hint={contract.due_day ? t('contracts.amend.now', { value: String(contract.due_day) }) : undefined}
          error={error?.errors.due_day}
        >
          <input
            id="am_due_day"
            className="input num"
            type="number"
            min={1}
            max={28}
            value={dueDay}
            onChange={(e) => setDueDay(e.target.value)}
          />
        </Field>
        <Field
          id="am_term"
          label={t('contracts.new.term')}
          hint={renewal ? t('contracts.amend.term_hint_renew') : t('contracts.amend.term_hint')}
          error={error?.errors.term_days}
        >
          <input
            id="am_term"
            className="input num"
            type="number"
            min={1}
            value={termDays}
            onChange={(e) => setTermDays(e.target.value)}
          />
        </Field>
      </div>

      <Field id="am_template" label={t('tpl.one')} error={error?.errors.template_id}>
        <select
          id="am_template"
          className="input"
          value={templateId}
          disabled={templates === null}
          onChange={(e) => setTemplateId(e.target.value)}
        >
          <option value="">{t('contracts.amend.template_same')}</option>
          {(templates ?? []).map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
        </select>
      </Field>

      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || reason.trim().length === 0}>
          {busy
            ? t('common.creating')
            : renewal
              ? t('contracts.amend.submit_renew')
              : t('contracts.amend.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}
