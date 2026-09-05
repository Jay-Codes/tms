'use client';

/**
 * Wizard step 4 (FLOWS flow 1 step 3.4): units inside the first property,
 * each with a price and optionally a restricted set of payment periods.
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { ProblemNote } from '../../../components/FormBits';
import { StatusMark } from '../../../components/UnitStatus';
import { AddManyUnitsForm, AddUnitForm } from '../../../components/UnitForms';
import {
  ApiError,
  periodsApi,
  propertiesApi,
  toApiError,
  type PaymentPeriod,
  type Property,
  type Unit,
} from '../../../lib/api';
import { Amount } from '../../../lib/format';

export function UnitsStep() {
  const [property, setProperty] = useState<Property | null>(null);
  const [units, setUnits] = useState<Unit[] | null>(null);
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [many, setMany] = useState(false);

  const loadUnits = useCallback(async (propertyId: string, signal?: AbortSignal) => {
    const res = await propertiesApi.units(propertyId, signal);
    setUnits(res.items ?? []);
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    (async () => {
      try {
        const res = await propertiesApi.list({ limit: 1 }, ac.signal);
        const first = (res.items ?? [])[0] ?? null;
        setProperty(first);
        if (first) await loadUnits(first.id, ac.signal);
        else setUnits([]);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setUnits([]);
      }
      try {
        const per = await periodsApi.list(false, ac.signal);
        setPeriods((per.items ?? []).filter((p) => p.active));
      } catch {
        /* picker degrades to empty */
      }
    })();
    return () => ac.abort();
  }, [loadUnits]);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      <p style={{ maxWidth: 'var(--measure)' }}>
        The rooms, houses or shops inside that property. Each one gets its own permanent QR code.
      </p>
      <ProblemNote error={error} />

      {units === null ? (
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      ) : !property ? (
        <p style={{ color: 'var(--ink-soft)' }}>
          Add a property in the previous step first — units live inside a property.
        </p>
      ) : (
        <>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            Adding to <strong>{property.name}</strong>.{' '}
            <Link href={`/properties/${property.id}`}>Open the property</Link> to manage another one.
          </p>

          {units.length > 0 ? (
            <table className="ledger">
              <thead>
                <tr>
                  <th>Unit</th>
                  <th>Status</th>
                  <th className="num">Price</th>
                  <th>Code</th>
                </tr>
              </thead>
              <tbody>
                {units.map((u) => (
                  <tr key={u.id}>
                    <td style={{ fontWeight: 600 }}>
                      <Link href={`/units/${u.id}`} style={{ color: 'inherit' }}>
                        {u.name}
                      </Link>
                    </td>
                    <td>
                      <StatusMark status={u.status} override={u.status_override} />
                    </td>
                    <td className="num">
                      <Amount value={u.current_price?.amount ?? null} per={u.current_price?.period_days ?? null} />
                    </td>
                    <td style={{ color: 'var(--ink-soft)', letterSpacing: '0.08em' }}>{u.unit_code}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}

          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className={many ? 'btn btn-quiet' : 'btn btn-secondary'}
              onClick={() => setMany(false)}
              aria-pressed={!many}
            >
              One at a time
            </button>
            <button
              type="button"
              className={many ? 'btn btn-secondary' : 'btn btn-quiet'}
              onClick={() => setMany(true)}
              aria-pressed={many}
            >
              Add many
            </button>
          </div>

          {many ? (
            <AddManyUnitsForm propertyId={property.id} onAdded={() => void loadUnits(property.id)} />
          ) : (
            <AddUnitForm
              propertyId={property.id}
              periods={periods}
              onAdded={() => void loadUnits(property.id)}
            />
          )}

          {units.length > 0 ? (
            <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              <Link href={`/properties/${property.id}/qr`}>Print the QR sheet</Link> once you have added
              every unit.
            </p>
          ) : null}
        </>
      )}
    </div>
  );
}
