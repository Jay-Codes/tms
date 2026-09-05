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
      .then(([u, r, p, t]) => {
        setUnits(u.items ?? []);
        setRenters(r.items ?? []);
        setPeriods(p.items ?? []);
        setTemplates(t.items ?? []);
        setTemplateId((t.items ?? []).find((x) => x.is_default)?.id ?? '');
        setLoadError(null);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    return () => ac.abort();
  }, []);

  const unit = units.find((u) => u.id === unitId) ?? null;
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
      <p style={{ color: 'var(--ink-soft)' }}>
        The contract is written from the template and sent to the renter to sign. Nothing changes for the
        unit until it is activated.
      </p>

      <Field
        id="c_unit"
        label="Unit"
        hint="Only vacant units can take a new contract."
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
          <option value="">Choose a unit…</option>
          {units.map((u) => (
            <option key={u.id} value={u.id}>
              {u.property_name} · {u.name}
              {u.current_price ? ` — ${fmtPrice(u.current_price)}` : ' — no price set'}
            </option>
          ))}
        </select>
      </Field>

      <Field
        id="c_renter"
        label="Renter"
        hint="Renters who have already applied to or rented from you."
        error={error?.errors.renter_user_id}
      >
        <select id="c_renter" className="input" value={renterId} onChange={(e) => setRenterId(e.target.value)}>
          <option value="">Choose a renter…</option>
          {renters.map((r) => (
            <option key={r.user_id} value={r.user_id}>
              {r.full_name} {r.phone ? `· ${r.phone}` : ''}
            </option>
          ))}
        </select>
      </Field>

      <Field id="c_template" label="Template" error={error?.errors.template_id}>
        <select
          id="c_template"
          className="input"
          value={templateId}
          onChange={(e) => setTemplateId(e.target.value)}
        >
          {templates.length === 0 ? <option value="">No templates yet</option> : null}
          {templates.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
              {t.is_default ? ' (default)' : ''}
            </option>
          ))}
        </select>
      </Field>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field id="c_period" label="Payment period" error={error?.errors.payment_period_id}>
          <select id="c_period" className="input" value={periodId} onChange={(e) => setPeriodId(e.target.value)}>
            <option value="">Choose…</option>
            {offered.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label} ({p.days} days)
              </option>
            ))}
          </select>
        </Field>

        <Field
          id="c_term"
          label="Tenancy length (days)"
          hint="How long the whole tenancy runs."
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

        <Field id="c_start" label="Start date" error={error?.errors.start_date}>
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
          label="Due day of month"
          hint="Blank uses your business default."
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
          {busy ? 'Creating…' : 'Create contract'}
        </button>
      </div>
    </form>
  );
}
