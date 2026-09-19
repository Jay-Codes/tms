'use client';

/**
 * Renter directory: everyone who has ever asked to link to one of this org's
 * units. Search by name or phone, filter by KYC state (SPEC §5.4).
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { NextDueCell } from '../../../components/DueBits';
import { Field, ProblemNote } from '../../../components/FormBits';
import { FilterTabs, KycStamp, linkStatusLabel, localeLabel } from '../../../components/RenterBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  rentersApi,
  toApiError,
  type KycStatus,
  type RenterSummary,
} from '../../../lib/api';
import { fmtDate } from '../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

/** Tab values with the key of their label — the words are picked at render. */
const KYC_TABS: { value: KycStatus | ''; key: string }[] = [
  { value: '', key: 'common.all' },
  { value: 'verified', key: 'renters.kyc.tab.verified' },
  { value: 'submitted', key: 'renters.kyc.tab.submitted' },
  { value: 'none', key: 'renters.kyc.tab.none' },
];

/**
 * `GET /renters` takes no `sort` parameter (API.md), so the Next-due ordering
 * is applied to the page that was loaded — the whole directory at `limit: 200`
 * for every org this product is built for. A renter with no next due sorts to
 * the end in both directions, because "nothing owing" is never the answer to
 * "who is next".
 */
function byNextDue(rows: RenterSummary[], dir: 'asc' | 'desc'): RenterSummary[] {
  return [...rows].sort((a, b) => {
    const x = a.next_due_date ?? '';
    const y = b.next_due_date ?? '';
    if (!x && !y) return 0;
    if (!x) return 1;
    if (!y) return -1;
    return dir === 'asc' ? x.localeCompare(y) : y.localeCompare(x);
  });
}

function DirectoryBody() {
  const t = useT();
  const [q, setQ] = useState('');
  const [debouncedQ, setDebouncedQ] = useState('');
  const [kyc, setKyc] = useState<KycStatus | ''>('');
  const [items, setItems] = useState<RenterSummary[] | null>(null);
  const [sort, setSort] = useState<'' | 'asc' | 'desc'>('');
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

  const rows = useMemo(
    () => (items === null || sort === '' ? items : byNextDue(items, sort)),
    [items, sort],
  );

  return (
    <>
      <PageHead
        title={t('renters.title')}
        lead={t('renters.lead')}
        actions={
          <Link href="/link-requests" className="btn btn-secondary">
            {t('nav.link_requests')}
          </Link>
        }
      />

      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 'var(--sp-4)' }}>
        <FilterTabs<KycStatus | ''>
          value={kyc}
          options={KYC_TABS.map((o) => ({ value: o.value, label: t(o.key) }))}
          onChange={setKyc}
          label={t('renters.kyc.filter_label')}
        />
        <Field id="r_q" label={t('common.search')}>
          <input
            id="r_q"
            className="input"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t('renters.search_placeholder')}
            style={{ minWidth: 240 }}
          />
        </Field>
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <TableScroll label={t('renters.title')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('common.name')}</th>
              <th>{t('common.phone')}</th>
              <th>{t('renters.col.kyc')}</th>
              <th>{t('renters.locale')}</th>
              <th>{t('renters.col.units')}</th>
              <th aria-sort={sort === 'asc' ? 'ascending' : sort === 'desc' ? 'descending' : 'none'}>
                <button
                  type="button"
                  className="btn btn-quiet"
                  onClick={() => setSort((s) => (s === 'asc' ? 'desc' : 'asc'))}
                  style={{
                    minHeight: 'var(--touch-min)',
                    padding: '0 var(--sp-2)',
                    font: 'inherit',
                    color: 'inherit',
                  }}
                >
                  {t('renters.col.next_due')}
                  <Icon
                    icon={
                      sort === 'desc'
                        ? 'solar:alt-arrow-down-linear'
                        : sort === 'asc'
                          ? 'solar:alt-arrow-up-linear'
                          : 'solar:sort-vertical-linear'
                    }
                    width={16}
                  />
                </button>
              </th>
              <th>{t('renters.col.known_since')}</th>
            </tr>
          </thead>
          <tbody>
            {rows === null ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  {t('common.loading')}
                </td>
              </tr>
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  {error ? t('common.no_results') : t('renters.empty')}
                </td>
              </tr>
            ) : (
              rows.map((r) => (
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
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {localeLabel(t, r.locale)}
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
                              · {u.property_name} · {linkStatusLabel(t, u.link_status)}
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </td>
                  <td style={{ fontSize: 'var(--text-sm)' }}>
                    <NextDueCell
                      date={r.next_due_date}
                      amount={r.next_due_amount}
                      overdue={r.overdue_amount}
                    />
                  </td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(r.created_at)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
        </TableScroll>
      </div>
    </>
  );
}

export default function RentersPage() {
  return (
    <>
      <DirectoryBody />
    </>
  );
}
