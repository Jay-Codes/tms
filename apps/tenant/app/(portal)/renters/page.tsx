'use client';

/**
 * Renter directory: everyone who has ever asked to link to one of this org's
 * units. Search by name or phone, filter by KYC state (SPEC §5.4).
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../components/FormBits';
import { FilterTabs, KycStamp } from '../../components/RenterBits';
import { PageHead, Shell } from '../../components/Shell';
import {
  ApiError,
  rentersApi,
  toApiError,
  type KycStatus,
  type RenterSummary,
} from '../../lib/api';
import { fmtDate } from '../../lib/format';

const KYC_TABS: { value: KycStatus | ''; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'verified', label: 'Verified' },
  { value: 'submitted', label: 'Submitted' },
  { value: 'none', label: 'No KYC' },
];

function DirectoryBody() {
  const [q, setQ] = useState('');
  const [debouncedQ, setDebouncedQ] = useState('');
  const [kyc, setKyc] = useState<KycStatus | ''>('');
  const [items, setItems] = useState<RenterSummary[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await rentersApi.list(
          { q: debouncedQ || undefined, kyc_status: kyc || undefined, limit: 200 },
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
    [debouncedQ, kyc],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <>
      <PageHead
        title="Renters"
        lead="Everyone linked to — or asking to link to — one of your units."
        actions={
          <Link href="/link-requests" className="btn btn-secondary">
            Link requests
          </Link>
        }
      />

      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 'var(--sp-4)' }}>
        <FilterTabs<KycStatus | ''> value={kyc} options={KYC_TABS} onChange={setKyc} label="KYC status" />
        <Field id="r_q" label="Search">
          <input
            id="r_q"
            className="input"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Name or phone"
            style={{ minWidth: 240 }}
          />
        </Field>
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <table className="ledger">
          <thead>
            <tr>
              <th>Name</th>
              <th>Phone</th>
              <th>KYC</th>
              <th>Units</th>
              <th>Known since</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                  {error ? 'Nothing to show.' : 'No renters match this filter.'}
                </td>
              </tr>
            ) : (
              items.map((r) => (
                <tr key={r.user_id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/renters/${r.user_id}`} style={{ color: 'inherit' }}>
                      {r.full_name || '—'}
                    </Link>
                    {r.email ? (
                      <div style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                        {r.email}
                      </div>
                    ) : null}
                  </td>
                  <td style={{ color: 'var(--ink-soft)' }}>{r.phone ?? '—'}</td>
                  <td>
                    <KycStamp status={r.kyc_status} />
                  </td>
                  <td style={{ fontSize: 'var(--text-sm)' }}>
                    {(r.units ?? []).length === 0 ? (
                      <span style={{ color: 'var(--ink-faint)' }}>—</span>
                    ) : (
                      <ul style={{ margin: 0, padding: 0, listStyle: 'none', display: 'grid', gap: 2 }}>
                        {(r.units ?? []).map((u) => (
                          <li key={`${u.unit_id}-${u.link_status}`}>
                            <Link href={`/units/${u.unit_id}`} style={{ color: 'inherit' }}>
                              {u.unit_name}
                            </Link>
                            <span style={{ color: 'var(--ink-soft)' }}>
                              {' '}
                              · {u.property_name} · {u.link_status}
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(r.created_at)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}

export default function RentersPage() {
  return (
    <Shell>
      <DirectoryBody />
    </Shell>
  );
}
