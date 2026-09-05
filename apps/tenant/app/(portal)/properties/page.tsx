'use client';

/**
 * Properties ledger (SPEC §5.3, FLOWS flow 4). One row per property with its
 * unit counts; the row opens the property. No data is invented here — the list
 * is exactly what `GET /properties` returns.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { ProblemNote } from '../../../components/FormBits';
import { PropertyForm } from '../../../components/PropertyForm';
import { PageHead } from '../../../components/PageHead';
import { Sheet } from '../../../components/Sheet';
import { ApiError, propertiesApi, toApiError, type Property } from '../../../lib/api';
import { TableScroll } from '@tms/ui';

function CountChip({ label, n, tone }: { label: string; n: number; tone?: 'ink' | 'faint' }) {
  if (!n) return null;
  return (
    <span
      style={{
        display: 'inline-block',
        padding: '0.1em 0.5em',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--text-xs)',
        whiteSpace: 'nowrap',
        color: tone === 'faint' ? 'var(--ink-faint)' : 'var(--ink-soft)',
        background: 'var(--sheet-tint)',
      }}
    >
      <span className="num">{n}</span> {label}
    </span>
  );
}

function PropertiesBody() {
  const router = useRouter();
  const [items, setItems] = useState<Property[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await propertiesApi.list({ limit: 100 }, signal);
      setItems(res.items ?? []);
      setError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <>
      <PageHead
        title="Properties"
        lead="Each property holds its units. Every unit carries a permanent QR code."
        actions={
          <>
            <Link href="/units" className="btn btn-secondary">
              <Icon icon="solar:widget-4-linear" width={20} /> Vacancy board
            </Link>
            <button type="button" className="btn btn-primary" onClick={() => setAdding(true)}>
              <Icon icon="solar:add-circle-linear" width={20} /> Add property
            </button>
          </>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <TableScroll label="Properties">
        <table className="ledger">
          <thead>
            <tr>
              <th>Property</th>
              <th>Location</th>
              <th>Units</th>
              <th className="num">Total</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {error ? 'Nothing to show.' : 'No properties yet. Add your first one to start.'}
                </td>
              </tr>
            ) : (
              items.map((p) => {
                const c = p.unit_counts ?? { total: 0, vacant: 0, occupied: 0, maintenance: 0, unlisted: 0 };
                return (
                  <tr
                    key={p.id}
                    onClick={() => router.push(`/properties/${p.id}`)}
                    style={{ cursor: 'pointer' }}
                  >
                    <td style={{ fontWeight: 600 }}>
                      <Link
                        href={`/properties/${p.id}`}
                        onClick={(e) => e.stopPropagation()}
                        style={{ color: 'inherit' }}
                      >
                        {p.name}
                      </Link>
                    </td>
                    <td style={{ color: 'var(--ink-soft)' }}>{p.location_text || '—'}</td>
                    <td>
                      <span style={{ display: 'inline-flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                        <CountChip label="occupied" n={c.occupied} />
                        <CountChip label="vacant" n={c.vacant} />
                        <CountChip label="maintenance" n={c.maintenance} tone="faint" />
                        <CountChip label="unlisted" n={c.unlisted} tone="faint" />
                        {c.total === 0 ? <span className="pencil">no units yet</span> : null}
                      </span>
                    </td>
                    <td className="num">{c.total}</td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
        </TableScroll>
      </div>

      <Sheet open={adding} title="Add property" onClose={() => setAdding(false)}>
        <PropertyForm
          onSaved={(p) => {
            setAdding(false);
            router.push(`/properties/${p.id}`);
          }}
          onCancel={() => setAdding(false)}
        />
      </Sheet>
    </>
  );
}

export default function PropertiesPage() {
  return (
    <>
      <PropertiesBody />
    </>
  );
}
