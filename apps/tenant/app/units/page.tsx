'use client';

/**
 * Vacancy board (FLOWS flow 4). Every unit in the org, filterable by status,
 * property and free text, with days-vacant read off `vacant_since`.
 * Selecting rows enables a bulk price change (FLOWS flow 5 step 3).
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Field, Note, ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import { Sheet } from '../../components/Sheet';
import { StatusMark } from '../../components/UnitStatus';
import {
  ApiError,
  propertiesApi,
  toApiError,
  unitsApi,
  type Property,
  type Unit,
  type UnitStatus,
} from '../../lib/api';
import { Amount, daysSince, fmtTZS, todayISO } from '../../lib/format';

const TABS: { value: '' | UnitStatus; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'vacant', label: 'Vacant' },
  { value: 'occupied', label: 'Occupied' },
  { value: 'maintenance', label: 'Maintenance' },
  { value: 'unlisted', label: 'Unlisted' },
];

/* ----------------------------- bulk price -------------------------------- */

function BulkPriceForm({
  unitIds,
  onDone,
  onCancel,
}: {
  unitIds: string[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const [mode, setMode] = useState<'percent' | 'set'>('percent');
  const [value, setValue] = useState('');
  const [periodDays, setPeriodDays] = useState('');
  const [from, setFrom] = useState(todayISO());
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unitsApi.bulkPrice({
        unit_ids: unitIds,
        mode,
        value: Number(value),
        period_days: periodDays ? Math.round(Number(periodDays)) : undefined,
        effective_from: from || undefined,
      });
      onDone();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>
        {unitIds.length} unit{unitIds.length === 1 ? '' : 's'} selected. Each gets a new price row —
        history is kept and active contracts are untouched.
      </p>

      <div className="field">
        <span style={{ fontSize: 'var(--text-sm)', fontWeight: 500, color: 'var(--ink-soft)' }}>Change</span>
        <div style={{ display: 'flex', gap: 'var(--sp-4)', margin: 'var(--sp-2) 0' }}>
          <label htmlFor="bp_percent" style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', cursor: 'pointer' }}>
            <input
              id="bp_percent"
              type="radio"
              name="bp_mode"
              checked={mode === 'percent'}
              onChange={() => setMode('percent')}
              style={{ width: 18, height: 18 }}
            />
            By percent
          </label>
          <label htmlFor="bp_set" style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', cursor: 'pointer' }}>
            <input
              id="bp_set"
              type="radio"
              name="bp_mode"
              checked={mode === 'set'}
              onChange={() => setMode('set')}
              style={{ width: 18, height: 18 }}
            />
            Set the same amount
          </label>
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="bp_value"
          label={mode === 'percent' ? 'Percent change (%)' : 'New amount (TZS)'}
          hint={
            mode === 'percent'
              ? 'e.g. 10 raises by 10%, −5 lowers by 5%. Rounded to whole shillings.'
              : value
                ? fmtTZS(Number(value))
                : 'Every selected unit gets exactly this amount.'
          }
          error={error?.errors.value}
        >
          <input
            id="bp_value"
            className="input num"
            type="number"
            step={mode === 'percent' ? 0.1 : 1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        <Field
          id="bp_days"
          label="Per (days)"
          hint="Leave blank to keep each unit's current basis."
          error={error?.errors.period_days}
        >
          <input
            id="bp_days"
            className="input num"
            type="number"
            min={1}
            value={periodDays}
            onChange={(e) => setPeriodDays(e.target.value)}
            placeholder="30"
          />
        </Field>
      </div>

      <Field id="bp_from" label="Effective from" error={error?.errors.effective_from}>
        <input id="bp_from" className="input" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
      </Field>

      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || value === ''}>
          {busy ? 'Applying…' : 'Apply to selected units'}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/* -------------------------------- board ---------------------------------- */

function UnitsBody() {
  const [status, setStatus] = useState<'' | UnitStatus>('');
  const [propertyId, setPropertyId] = useState('');
  const [q, setQ] = useState('');
  const [debouncedQ, setDebouncedQ] = useState('');
  const [items, setItems] = useState<Unit[] | null>(null);
  const [properties, setProperties] = useState<Property[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [bulkOpen, setBulkOpen] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => setProperties(r.items ?? []))
      .catch(() => setProperties([]));
    return () => ac.abort();
  }, []);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await unitsApi.list(
          { status: status || undefined, property_id: propertyId || undefined, q: debouncedQ || undefined, limit: 200 },
          signal,
        );
        setItems(res.items ?? []);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [status, propertyId, debouncedQ],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const visibleIds = useMemo(() => (items ?? []).map((u) => u.id), [items]);
  const allSelected = visibleIds.length > 0 && visibleIds.every((id) => selected.includes(id));

  const toggle = (id: string) =>
    setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]));

  return (
    <>
      <PageHead
        title="Units"
        lead="Every unit you manage. Vacant ones are pencilled in; occupied ones are stamped."
        actions={
          <button
            type="button"
            className="btn btn-primary"
            disabled={selected.length === 0}
            onClick={() => setBulkOpen(true)}
          >
            <Icon icon="solar:tag-price-linear" width={20} /> Bulk price
            {selected.length ? ` (${selected.length})` : ''}
          </button>
        }
      />

      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 'var(--sp-4)' }}>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }} role="tablist" aria-label="Status">
          {TABS.map((t) => {
            const active = status === t.value;
            return (
              <button
                key={t.value || 'all'}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setStatus(t.value)}
                style={{
                  minHeight: 'var(--touch-min)',
                  padding: '0 var(--sp-4)',
                  border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                  borderRadius: 'var(--radius-md)',
                  background: active ? 'var(--primary-soft)' : 'transparent',
                  color: 'var(--ink)',
                  fontWeight: active ? 600 : 400,
                  fontSize: 'var(--text-sm)',
                  cursor: 'pointer',
                }}
              >
                {t.label}
              </button>
            );
          })}
        </div>

        <Field id="f_property" label="Property">
          <select
            id="f_property"
            className="input"
            value={propertyId}
            onChange={(e) => setPropertyId(e.target.value)}
            style={{ minWidth: 220 }}
          >
            <option value="">All properties</option>
            {properties.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>

        <Field id="f_q" label="Search">
          <input
            id="f_q"
            className="input"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Unit name or code"
            style={{ minWidth: 220 }}
          />
        </Field>
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        {note ? <Note>{note}</Note> : null}

        <table className="ledger">
          <thead>
            <tr>
              <th style={{ width: 36 }}>
                <input
                  type="checkbox"
                  aria-label="Select every unit shown"
                  checked={allSelected}
                  onChange={() =>
                    setSelected((s) =>
                      allSelected ? s.filter((id) => !visibleIds.includes(id)) : Array.from(new Set([...s, ...visibleIds])),
                    )
                  }
                  style={{ width: 16, height: 16 }}
                />
              </th>
              <th>Unit</th>
              <th>Property</th>
              <th>Status</th>
              <th className="num">Days vacant</th>
              <th className="num">Current price</th>
              <th>Code</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  {error ? 'Nothing to show.' : 'No units match this filter.'}
                </td>
              </tr>
            ) : (
              items.map((u) => {
                const vacantDays = u.status === 'vacant' ? daysSince(u.vacant_since) : null;
                return (
                  <tr key={u.id}>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`Select ${u.name}`}
                        checked={selected.includes(u.id)}
                        onChange={() => toggle(u.id)}
                        style={{ width: 16, height: 16 }}
                      />
                    </td>
                    <td style={{ fontWeight: 600 }}>
                      <Link href={`/units/${u.id}`} style={{ color: 'inherit' }}>
                        {u.name}
                      </Link>
                    </td>
                    <td style={{ color: 'var(--ink-soft)' }}>
                      <Link href={`/properties/${u.property_id}`} style={{ color: 'inherit' }}>
                        {u.property_name}
                      </Link>
                    </td>
                    <td>
                      <StatusMark status={u.status} override={u.status_override} />
                    </td>
                    <td className="num">{vacantDays === null ? '—' : vacantDays}</td>
                    <td className="num">
                      <Amount value={u.current_price?.amount ?? null} per={u.current_price?.period_days ?? null} />
                    </td>
                    <td style={{ color: 'var(--ink-soft)', letterSpacing: '0.08em' }}>{u.unit_code}</td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      <Sheet open={bulkOpen} title="Bulk price change" onClose={() => setBulkOpen(false)}>
        <BulkPriceForm
          unitIds={selected}
          onDone={() => {
            setBulkOpen(false);
            setSelected([]);
            setNote('Prices updated.');
            void load();
          }}
          onCancel={() => setBulkOpen(false)}
        />
      </Sheet>
    </>
  );
}

export default function UnitsPage() {
  return (
    <Shell>
      <UnitsBody />
    </Shell>
  );
}
