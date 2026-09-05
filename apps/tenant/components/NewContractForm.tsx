'use client';

/**
 * "New contract" (FLOWS flow 3, the manual path beside link approval).
 *
 * Everything offered here is read from the API: only vacant units can carry a
 * new contract, only renters already known to this org can be named, and the
 * payment periods are the org's own. The form computes nothing — the backend
 * resolves the terms, prorates the schedule and hashes the snapshot.
 */

import { useEffect, useState } from 'react';
import { LOCALE_LABELS, LOCALES, useT, type Locale } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import {
  ApiError,
  contractsApi,
  periodsApi,
  rentersApi,
  templatesApi,
  toApiError,
  unitsApi,
  unwrapContract,
  type Contract,
  type ContractTemplateSummary,
  type PaymentPeriod,
  type RenterSummary,
  type Unit,
} from '../lib/api';
import { fmtPrice, todayISO } from '../lib/format';

export function NewContractForm({ onCreated }: { onCreated: (c: Contract) => void }) {
  const t = useT();
  const [units, setUnits] = useState<Unit[]>([]);
  const [renters, setRenters] = useState<RenterSummary[]>([]);
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const [templates, setTemplates] = useState<ContractTemplateSummary[]>([]);

  const [unitId, setUnitId] = useState('');
  const [renterId, setRenterId] = useState('');
  const [templateId, setTemplateId] = useState('');
  const [periodId, setPeriodId] = useState('');
  const [termDays, setTermDays] = useState('365');
  const [startDate, setStartDate] = useState(todayISO());
  const [dueDay, setDueDay] = useState('');
  /**
   * Which language the document is written in. It follows the renter — the
   * person who has to read and sign it — and only stops following once the
   * landlord has overridden it by hand. Renters from before Phase 13 have no
   * locale yet, so the platform default (Swahili) stands in.
   */
  const [language, setLanguage] = useState<Locale>('sw');
  const [languageTouched, setLanguageTouched] = useState(false);

  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    Promise.all([
      unitsApi.list({ status: 'vacant', limit: 200 }, ac.signal),
      rentersApi.list({ limit: 200 }, ac.signal),
      periodsApi.list(false, ac.signal),
      templatesApi.list(ac.signal),
    ])
      .then(([u, r, p, tpl]) => {
        setUnits(u.items ?? []);
        setRenters(r.items ?? []);
        setPeriods(p.items ?? []);
        setTemplates(tpl.items ?? []);
        setTemplateId((tpl.items ?? []).find((x) => x.is_default)?.id ?? '');
        setLoadError(null);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    return () => ac.abort();
  }, []);

  const unit = units.find((u) => u.id === unitId) ?? null;
  const renter = renters.find((r) => r.user_id === renterId) ?? null;
  const renterLocale = renter?.locale ?? null;

  useEffect(() => {
    if (languageTouched) return;
    setLanguage(renterLocale ?? 'sw');
  }, [renterLocale, languageTouched]);

  // A unit may restrict which periods it offers; `null`/[] means all of them.
  const offered = unit?.allowed_period_ids?.length
    ? periods.filter((p) => unit.allowed_period_ids!.includes(p.id))
    : periods;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await contractsApi.create({
        unit_id: unitId,
        renter_user_id: renterId,
        template_id: templateId || undefined,
        payment_period_id: periodId,
        term_days: Math.round(Number(termDays)),
        start_date: startDate,
        due_day: dueDay ? Math.round(Number(dueDay)) : undefined,
        language,
      });
      onCreated(unwrapContract(res));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  if (loadError) return <ProblemNote error={loadError} />;

  const ready = unitId && renterId && periodId && Number(termDays) > 0 && startDate;

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.new.lead')}</p>

      <Field
        id="c_unit"
        label={t('common.unit')}
        hint={t('contracts.new.unit_hint')}
        error={error?.errors.unit_id}
      >
        <select
          id="c_unit"
          className="input"
          value={unitId}
          onChange={(e) => {
            setUnitId(e.target.value);
            setPeriodId('');
          }}
        >
          <option value="">{t('contracts.new.choose_unit')}</option>
          {units.map((u) => (
            <option key={u.id} value={u.id}>
              {u.property_name} · {u.name}
              {u.current_price ? ` — ${fmtPrice(u.current_price)}` : ` — ${t('contracts.new.no_price')}`}
            </option>
          ))}
        </select>
      </Field>

      <Field
        id="c_renter"
        label={t('common.renter')}
        hint={t('contracts.new.renter_hint')}
        error={error?.errors.renter_user_id}
      >
        <select id="c_renter" className="input" value={renterId} onChange={(e) => setRenterId(e.target.value)}>
          <option value="">{t('contracts.new.choose_renter')}</option>
          {renters.map((r) => (
            <option key={r.user_id} value={r.user_id}>
              {r.full_name} {r.phone ? `· ${r.phone}` : ''}
            </option>
          ))}
        </select>
      </Field>

      <Field id="c_template" label={t('tpl.one')} error={error?.errors.template_id}>
        <select
          id="c_template"
          className="input"
          value={templateId}
          onChange={(e) => setTemplateId(e.target.value)}
        >
          {templates.length === 0 ? <option value="">{t('tpl.list.empty')}</option> : null}
          {templates.map((tpl) => (
            <option key={tpl.id} value={tpl.id}>
              {tpl.name}
              {tpl.is_default ? ` (${t('tpl.default').toLowerCase()})` : ''}
            </option>
          ))}
        </select>
      </Field>

      <Field
        id="c_language"
        label={t('contracts.new.language')}
        hint={
          renterLocale
            ? t('contracts.new.language_hint_renter', { language: LOCALE_LABELS[renterLocale] })
            : t('contracts.new.language_hint_default')
        }
        error={error?.errors.language}
      >
        <select
          id="c_language"
          className="input"
          value={language}
          onChange={(e) => {
            setLanguage(e.target.value as Locale);
            setLanguageTouched(true);
          }}
        >
          {LOCALES.map((l) => (
            <option key={l} value={l}>
              {LOCALE_LABELS[l]}
            </option>
          ))}
        </select>
      </Field>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field id="c_period" label={t('contracts.new.period')} error={error?.errors.payment_period_id}>
          <select id="c_period" className="input" value={periodId} onChange={(e) => setPeriodId(e.target.value)}>
            <option value="">{t('tpl.choose')}</option>
            {offered.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label} ({t.n('common.day', p.days)})
              </option>
            ))}
          </select>
        </Field>

        <Field
          id="c_term"
          label={t('contracts.new.term')}
          hint={t('contracts.new.term_hint')}
          error={error?.errors.term_days}
        >
          <input
            id="c_term"
            className="input num"
            type="number"
            min={1}
            max={3650}
            value={termDays}
            onChange={(e) => setTermDays(e.target.value)}
          />
        </Field>

        <Field id="c_start" label={t('contracts.new.start')} error={error?.errors.start_date}>
          <input
            id="c_start"
            className="input"
            type="date"
            value={startDate}
            onChange={(e) => setStartDate(e.target.value)}
          />
        </Field>

        <Field
          id="c_due_day"
          label={t('contracts.new.due_day')}
          hint={t('contracts.new.due_day_hint')}
          error={error?.errors.due_day}
        >
          <input
            id="c_due_day"
            className="input num"
            type="number"
            min={1}
            max={28}
            value={dueDay}
            onChange={(e) => setDueDay(e.target.value)}
          />
        </Field>
      </div>

      <div>
        <button type="submit" className="btn btn-primary" disabled={busy || !ready}>
          {busy ? t('common.creating') : t('contracts.new.submit')}
        </button>
      </div>
    </form>
  );
}
