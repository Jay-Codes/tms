'use client';

/**
 * Organization directory — `GET /admin/orgs?q=&status=&cursor=`.
 *
 * Search and the status tabs are server-side filters (the endpoint takes both);
 * nothing is filtered in the browser, so "Load more" always continues the same
 * query the backend answered.
 */

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useState } from 'react';
import { ProblemNote, StatusStamp } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import { ApiError, adminApi, toApiError, type AdminOrgSummary } from '../../../lib/api';
import { fmtDate, fmtNum } from '../../../lib/format';

const PAGE_SIZE = 50;

const STATUS_TABS = [
  { value: '', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'suspended', label: 'Suspended' },
];

function OrgsBody() {
  const params = useSearchParams();
  const initialStatus = params.get('status') ?? '';

  const [status, setStatus] = useState(initialStatus);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [items, setItems] = useState<AdminOrgSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const fetchPage = useCallback(
    async (f: { status: string; q: string }, nextCursor: string | null) => {
      setLoading(true);
      setError(null);
      try {
        const res = await adminApi.orgs({
          q: f.q || undefined,
          status: f.status || undefined,
          cursor: nextCursor || undefined,
          limit: PAGE_SIZE,
        });
        setItems((prev) => (nextCursor ? [...prev, ...(res.items ?? [])] : (res.items ?? [])));
        setCursor(res.next_cursor ?? null);
      } catch (e) {
        setError(toApiError(e));
        if (!nextCursor) setItems([]);
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    void fetchPage({ status, q }, null);
  }, [status, q, fetchPage]);

  return (
    <>
      <PageHead title="Organizations" lead="Every landlord business on the platform." />

      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        {/* `.tabs` is a grid (a vertical rail); laid across for a filter row. */}
        <div
          className="tabs"
          role="tablist"
          aria-label="Status"
          style={{ gridAutoFlow: 'column', justifyContent: 'start' }}
        >
          {STATUS_TABS.map((t) => (
            <button
              key={t.value || 'all'}
              type="button"
              role="tab"
              className="tab"
              aria-current={status === t.value ? 'page' : undefined}
              aria-selected={status === t.value}
              onClick={() => setStatus(t.value)}
            >
              {t.label}
            </button>
          ))}
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            setQ(search.trim());
          }}
          style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'flex-end' }}
        >
          <div className="field">
            <label htmlFor="q">Search</label>
            <input
              id="q"
              className="input"
              type="search"
              placeholder="Name or slug"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{ minWidth: 260 }}
            />
          </div>
          <button type="submit" className="btn btn-secondary">
            Search
          </button>
          {q ? (
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                setSearch('');
                setQ('');
              }}
            >
              Clear
            </button>
          ) : null}
        </form>
      </div>

      <div style={{ marginTop: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
      </div>

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th>Organization</th>
            <th>Owner</th>
            <th className="num">Properties</th>
            <th className="num">Units</th>
            <th className="num">Renters</th>
            <th className="num">Contracts</th>
            <th className="num">SMS 30d</th>
            <th>Status</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && !loading ? (
            <tr>
              <td colSpan={9} style={{ color: 'var(--ink-soft)' }}>
                No organizations match these filters.
              </td>
            </tr>
          ) : (
            items.map((org) => (
              <tr key={org.id}>
                <td>
                  <Link href={`/orgs/${org.id}`} style={{ fontWeight: 500, color: 'inherit' }}>
                    {org.name}
                  </Link>
                  <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{org.slug}</div>
                </td>
                <td>
                  {org.owner ? (
                    <>
                      <div>{org.owner.name}</div>
                      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                        {org.owner.email}
                      </div>
                    </>
                  ) : (
                    <span className="pencil">no owner</span>
                  )}
                </td>
                <td className="num">{fmtNum(org.counts?.properties)}</td>
                <td className="num">{fmtNum(org.counts?.units)}</td>
                <td className="num">{fmtNum(org.counts?.renters)}</td>
                <td className="num">{fmtNum(org.counts?.active_contracts)}</td>
                <td className="num">
                  {fmtNum(org.sms?.sent_30d)}
                  {org.sms?.failed_30d ? (
                    <span style={{ color: 'var(--stamp-overdue)' }}> · {fmtNum(org.sms.failed_30d)} failed</span>
                  ) : null}
                </td>
                <td>
                  <StatusStamp status={org.status} />
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(org.created_at)}</td>
              </tr>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={9} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>

      {cursor ? (
        <div style={{ marginTop: 'var(--sp-4)' }}>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={loading}
            onClick={() => void fetchPage({ status, q }, cursor)}
          >
            {loading ? 'Loading…' : 'Load more'}
          </button>
        </div>
      ) : null}
    </>
  );
}

export default function OrgsPage() {
  return (
    <>
      {/* useSearchParams needs a boundary; the list is client-rendered anyway. */}
      <Suspense fallback={<p style={{ color: 'var(--ink-soft)' }}>Loading…</p>}>
        <OrgsBody />
      </Suspense>
    </>
  );
}
