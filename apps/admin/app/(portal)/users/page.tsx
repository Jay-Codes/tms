'use client';

/**
 * Platform user directory — `GET /admin/users?q=&kind=&status=&org_id=&cursor=`
 * (PLAN2 19.2). The first thing support reaches for when a phone number calls:
 * "who is this on our platform".
 *
 * Search and both filters are server-side (the endpoint takes all of them);
 * nothing is filtered in the browser, so "Show older" always continues the same
 * query the backend answered. The list carries no NIDA field of any kind — the
 * masked value lives on the detail page and the full number only behind the
 * audited reveal.
 */

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useState } from 'react';
import { ProblemNote, StatusStamp } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import { KindChip } from '../../../components/UserBits';
import {
  ApiError,
  adminUsers,
  orgRoleLabel,
  toApiError,
  type AdminUserRow,
  type UserKind,
} from '../../../lib/api';
import { fmtDate, fmtNum } from '../../../lib/format';

const PAGE_SIZE = 50;

const KIND_TABS: { value: UserKind | ''; label: string }[] = [
  { value: '', label: 'Everyone' },
  { value: 'renter', label: 'Renters' },
  { value: 'org_user', label: 'Landlord staff' },
  { value: 'platform_admin', label: 'Platform admins' },
];

const STATUS_TABS = [
  { value: '', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'suspended', label: 'Suspended' },
];

const COLS = 7;

/** The orgs cell: name plus role (staff) or relationship (renter). */
function OrgsCell({ row }: { row: AdminUserRow }) {
  const orgs = row.orgs ?? [];
  if (orgs.length === 0) {
    return <span className="pencil">{row.kind === 'platform_admin' ? 'platform' : 'none'}</span>;
  }
  return (
    <div style={{ display: 'grid', gap: 2 }}>
      {orgs.slice(0, 3).map((o) => (
        <div key={o.id} style={{ minWidth: 0 }}>
          <Link href={`/orgs/${o.id}`} style={{ color: 'inherit' }}>
            {o.name}
          </Link>
          {o.role || o.relationship ? (
            <span className="pencil" style={{ fontSize: 'var(--text-sm)' }}>
              {' '}
              · {o.role ? orgRoleLabel(o.role) : o.relationship}
            </span>
          ) : null}
        </div>
      ))}
      {orgs.length > 3 ? <span className="pencil">+{orgs.length - 3} more</span> : null}
    </div>
  );
}

function UsersBody() {
  const params = useSearchParams();
  const initialOrg = params.get('org_id') ?? '';
  const initialKind = (params.get('kind') ?? '') as UserKind | '';

  const [kind, setKind] = useState<UserKind | ''>(initialKind);
  const [status, setStatus] = useState('');
  const [orgId] = useState(initialOrg);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [items, setItems] = useState<AdminUserRow[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const fetchPage = useCallback(
    async (f: { kind: UserKind | ''; status: string; q: string; org_id: string }, nextCursor: string | null) => {
      setLoading(true);
      setError(null);
      try {
        const res = await adminUsers.list({
          q: f.q || undefined,
          kind: f.kind || undefined,
          status: f.status || undefined,
          org_id: f.org_id || undefined,
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
    void fetchPage({ kind, status, q, org_id: orgId }, null);
  }, [kind, status, q, orgId, fetchPage]);

  return (
    <>
      <PageHead
        title="Users"
        lead="Everyone with an account — renters, landlord staff and platform admins, across every organization."
      />

      <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <div className="tabs" role="tablist" aria-label="Kind" style={{ gridAutoFlow: 'column', justifyContent: 'start' }}>
          {KIND_TABS.map((t) => (
            <button
              key={t.value || 'all'}
              type="button"
              role="tab"
              className="tab"
              aria-current={kind === t.value ? 'page' : undefined}
              aria-selected={kind === t.value}
              onClick={() => setKind(t.value)}
            >
              {t.label}
            </button>
          ))}
        </div>

        <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
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
                placeholder="Phone, e-mail or name"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                style={{ minWidth: 280 }}
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
      </div>

      {orgId ? (
        <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          Filtered to one organization. <Link href="/users">Show everyone →</Link>
        </p>
      ) : null}

      <div style={{ marginTop: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
      </div>

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th>Name</th>
            <th>Kind</th>
            <th>Phone</th>
            <th>E-mail</th>
            <th>Status</th>
            <th>Organizations</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && !loading ? (
            <tr>
              <td colSpan={COLS} style={{ color: 'var(--ink-soft)' }}>
                {q
                  ? `Nobody matches “${q}” with these filters. Phone numbers match in any format; e-mail and name need the start of the value.`
                  : 'No users match these filters.'}
              </td>
            </tr>
          ) : (
            items.map((u) => (
              <tr key={u.id}>
                <td>
                  <Link href={`/users/${u.id}`} style={{ fontWeight: 500, color: 'inherit' }}>
                    {u.full_name || <span className="pencil">no name</span>}
                  </Link>
                  {u.kind === 'renter' && (u.contracts_live ?? 0) > 0 ? (
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {fmtNum(u.contracts_live)} live contract{u.contracts_live === 1 ? '' : 's'}
                    </div>
                  ) : null}
                </td>
                <td>
                  <KindChip kind={u.kind} />
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{u.phone || <span className="pencil">—</span>}</td>
                <td>{u.email || <span className="pencil">—</span>}</td>
                <td>
                  <StatusStamp status={u.status} />
                </td>
                <td>
                  <OrgsCell row={u} />
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(u.created_at)}</td>
              </tr>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={COLS} style={{ color: 'var(--ink-soft)' }}>
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
            onClick={() => void fetchPage({ kind, status, q, org_id: orgId }, cursor)}
          >
            {loading ? 'Loading…' : 'Show older'}
          </button>
        </div>
      ) : null}
    </>
  );
}

export default function UsersPage() {
  return (
    <>
      {/* useSearchParams needs a boundary; the list is client-rendered anyway. */}
      <Suspense fallback={<p style={{ color: 'var(--ink-soft)' }}>Loading…</p>}>
        <UsersBody />
      </Suspense>
    </>
  );
}
