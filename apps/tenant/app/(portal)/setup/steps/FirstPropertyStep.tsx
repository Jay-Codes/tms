'use client';

/** Wizard step 2 (FLOWS flow 1 step 3.2): the first property. */

import Link from 'next/link';
import { TableScroll, useT } from '@tms/ui';
import { useEffect, useState } from 'react';
import { ProblemNote } from '../../../../components/FormBits';
import { PropertyForm } from '../../../../components/PropertyForm';
import { ApiError, propertiesApi, toApiError, type Property } from '../../../../lib/api';

export function FirstPropertyStep() {
  const t = useT();
  const [items, setItems] = useState<Property[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 20 }, ac.signal)
      .then((r) => setItems(r.items ?? []))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      });
    return () => ac.abort();
  }, []);

  const has = (items?.length ?? 0) > 0;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 'var(--measure)' }}>
      <p>{t('setup.property.lead')}</p>
      <ProblemNote error={error} />

      {items === null ? (
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      ) : has && !adding ? (
        <>
          <TableScroll label={t('nav.properties')}>
<table className="ledger">
            <thead>
              <tr>
                <th>{t('common.property')}</th>
                <th>{t('setup.property.col.location')}</th>
                <th className="num">{t('nav.units')}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((p) => (
                <tr key={p.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/properties/${p.id}`} style={{ color: 'inherit' }}>
                      {p.name}
                    </Link>
                  </td>
                  <td style={{ color: 'var(--ink-soft)' }}>{p.location_text || '—'}</td>
                  <td className="num">{p.unit_counts?.total ?? 0}</td>
                </tr>
              ))}
            </tbody>
          </table>
</TableScroll>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('setup.property.done')}</p>
          <div>
            <button type="button" className="btn btn-secondary" onClick={() => setAdding(true)}>
              {t('setup.property.add_another')}
            </button>
          </div>
        </>
      ) : (
        <PropertyForm
          onSaved={(p) => {
            setItems((prev) => [...(prev ?? []), p]);
            setAdding(false);
          }}
          onCancel={has ? () => setAdding(false) : undefined}
        />
      )}
    </div>
  );
}
