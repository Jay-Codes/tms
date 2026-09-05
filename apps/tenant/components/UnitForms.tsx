'use client';

/**
 * Add one unit, or many at once, to a property.
 * `POST /properties/{id}/units` and `POST /properties/{id}/units/bulk`.
 */

import { useState } from 'react';
import {
  ApiError,
  propertiesApi,
  toApiError,
  unwrapUnit,
  type PaymentPeriod,
  type PriceInput,
  type Unit,
} from '../lib/api';
import { fmtTZS } from '../lib/format';
import { Field, ProblemNote } from './FormBits';
import { PeriodPicker } from './PeriodPicker';

/** Shared price inputs; returns undefined when the landlord left the amount blank. */
function priceBody(amount: string, periodDays: string): PriceInput | undefined {
  const a = Number(amount);
  if (!amount.trim() || !Number.isFinite(a) || a <= 0) return undefined;
  const d = Number(periodDays);
  return { amount: Math.round(a), period_days: Number.isFinite(d) && d > 0 ? Math.round(d) : 30 };
}

function PriceFields({
  amount,
  setAmount,
  periodDays,
  setPeriodDays,
  error,
  idPrefix,
}: {
  amount: string;
  setAmount: (v: string) => void;
  periodDays: string;
  setPeriodDays: (v: string) => void;
  error: ApiError | null;
  idPrefix: string;
}) {
  const preview = priceBody(amount, periodDays);
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 140px', gap: 'var(--sp-4)' }}>
      <Field
        id={`${idPrefix}_amount`}
        label="Price (TZS)"
        hint={preview ? `${fmtTZS(preview.amount)} / ${preview.period_days} days` : 'Whole shillings. Leave blank to set it later.'}
        error={error?.errors['price.amount'] ?? error?.errors.amount}
      >
        <input
          id={`${idPrefix}_amount`}
          className="input num"
          type="number"
          min={1}
          step={1}
          inputMode="numeric"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          placeholder="250000"
        />
      </Field>
      <Field
        id={`${idPrefix}_days`}
        label="Per (days)"
        error={error?.errors['price.period_days'] ?? error?.errors.period_days}
      >
        <input
          id={`${idPrefix}_days`}
          className="input num"
          type="number"
          min={1}
          value={periodDays}
          onChange={(e) => setPeriodDays(e.target.value)}
        />
      </Field>
    </div>
  );
}

export function AddUnitForm({
  propertyId,
  periods,
  onAdded,
  onCancel,
}: {
  propertyId: string;
  periods: PaymentPeriod[];
  onAdded: (u: Unit) => void;
  onCancel?: () => void;
}) {
  const [name, setName] = useState('');
  const [amount, setAmount] = useState('');
  const [periodDays, setPeriodDays] = useState('30');
  const [allowed, setAllowed] = useState<string[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await propertiesApi.addUnit(propertyId, {
        name: name.trim(),
        price: priceBody(amount, periodDays),
        allowed_period_ids: allowed,
      });
      onAdded(unwrapUnit(res));
      setName('');
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <Field id="unit_name" label="Unit name" hint='Your own name, e.g. "Room 1" or "House B".' error={error?.errors.name}>
        <input
          id="unit_name"
          className="input"
          value={name}
          maxLength={60}
          onChange={(e) => setName(e.target.value)}
          placeholder="Room 1"
        />
      </Field>
      <PriceFields
        amount={amount}
        setAmount={setAmount}
        periodDays={periodDays}
        setPeriodDays={setPeriodDays}
        error={error}
        idPrefix="unit"
      />
      <PeriodPicker periods={periods} value={allowed} onChange={setAllowed} idPrefix="add" />
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
          {busy ? 'Adding…' : 'Add unit'}
        </button>
        {onCancel ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel}>
            Cancel
          </button>
        ) : null}
      </div>
    </form>
  );
}

export function AddManyUnitsForm({
  propertyId,
  onAdded,
  onCancel,
}: {
  propertyId: string;
  onAdded: (units: Unit[]) => void;
  onCancel?: () => void;
}) {
  const [text, setText] = useState('');
  const [amount, setAmount] = useState('');
  const [periodDays, setPeriodDays] = useState('30');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const names = text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await propertiesApi.addUnitsBulk(propertyId, {
        names,
        price: priceBody(amount, periodDays),
      });
      onAdded(res.items ?? []);
      setText('');
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <Field
        id="bulk_names"
        label="Unit names"
        hint="One per line (commas work too). Up to 200 at a time."
        error={error?.errors.names}
      >
        <textarea
          id="bulk_names"
          className="input"
          rows={8}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={'Room 1\nRoom 2\nRoom 3'}
          style={{ height: 'auto', paddingTop: 'var(--sp-2)', paddingBottom: 'var(--sp-2)' }}
        />
      </Field>
      <PriceFields
        amount={amount}
        setAmount={setAmount}
        periodDays={periodDays}
        setPeriodDays={setPeriodDays}
        error={error}
        idPrefix="bulk"
      />
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {names.length} unit{names.length === 1 ? '' : 's'} will be created, each with its own QR code.
      </p>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || names.length === 0}>
          {busy ? 'Adding…' : `Add ${names.length || ''} units`.trim()}
        </button>
        {onCancel ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel}>
            Cancel
          </button>
        ) : null}
      </div>
    </form>
  );
}
