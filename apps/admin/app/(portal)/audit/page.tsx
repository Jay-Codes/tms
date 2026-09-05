'use client';

/**
 * Cross-org audit search — `GET /admin/audit-log` (FLOWS 11.3: support cases).
 *
 * Every filter is a query parameter the backend applies; the org picker is
 * filled from `GET /admin/orgs` so a support case can be narrowed by business
 * without pasting a UUID. Rows expand to the before/after the audit row stored.
 */

import { useSearchParams } from 'next/navigation';
import { Fragment, Suspense, useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import {
  ApiError,
  adminApi,
  toApiError,
  type AdminAuditEntry,
  type AdminOrgSummary,
} from '../../lib/api';
import { fmtDateTime, fmtJson } from '../../lib/format';

const PAGE_SIZE = 50;

/** Entity types the platform writes audit rows for (free text also allowed). */
const ENTITY_TYPES = [
  'org',
  'user',
  'org_member',
  'session',
  'property',
  'unit',
  'price_plan',
  'payment_period',
  'contract',
  'contract_template',
  'unit_link_request',
  'payment',
  'payment_schedule',
  'notification',
  'job',
];

interface Filters {
  org_id: string;
  actor: string;
  entity_type: string;
  entity_id: string;
  q: string;
  from: string;
  to: string;
}

const EMPTY: Filters = { org_id: '', actor: '', entity_type: '', entity_id: '', q: '', from: '', to: '' };

/**
 * The org picker. `GET /admin/orgs` caps `limit` at 100, so the cursor is
 * followed a few pages to name every org; errors are silent, because a picker
 * that cannot load must not take the search down with it.
 */
const ORG_PAGE = 100;
const ORG_MAX_PAGES = 5;

function useOrgOptions(): AdminOrgSummary[] {
  const [orgs, setOrgs] = useState<AdminOrgSummary[]>([]);

  useEffect(() => {
    const ac = new AbortController();
    (async () => {
      const all: AdminOrgSummary[] = [];
      let cursor: string | null = null;
      for (let page = 0; page < ORG_MAX_PAGES; page++) {
        const res: { items: AdminOrgSummary[]; next_cursor?: string | null } = await adminApi.orgs(
          { limit: ORG_PAGE, cursor: cursor || undefined },
          ac.signal,
        );
        all.push(...(res.items ?? []));
        cursor = res.next_cursor ?? null;
        if (!cursor) break;
      }
      setOrgs(all);
    })().catch(() => setOrgs([]));
    return () => ac.abort();
  }, []);

  return orgs;
}

function ExpandedRow({ row, colSpan }: { row: AdminAuditEntry; colSpan: number }) {
  const box: React.CSSProperties = {
    margin: 0,
    padding: 'var(--sp-3)',
    background: 'var(--sheet-tint)',
    borderRadius: 'var(--radius-sm)',
    fontSize: 'var(--text-sm)',
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    maxHeight: 320,
    overflow: 'auto',
  };
  return (
    <tr>
      <td colSpan={colSpan}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
          <div>
            <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginBottom: 'var(--sp-2)' }}>
              Before
            </div>
            <pre style={box}>{fmtJson(row.before)}</pre>
          </div>
          <div>
            <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginBottom: 'var(--sp-2)' }}>
              After
            </div>
            <pre style={box}>{fmtJson(row.after)}</pre>
          </div>
        </div>
        <div style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          Entry {row.id}
          {row.entity_id ? ` · entity ${row.entity_id}` : ''}
          {row.actor_user_id ? ` · actor ${row.actor_user_id}` : ''}
          {row.org_id ? ` · org ${row.org_id}` : ''}
        </div>
      </td>
    </tr>
  );
}

function AuditBody() {
  const params = useSearchParams();
  const orgs = useOrgOptions();

  const [filters, setFilters] = useState<Filters>({ ...EMPTY, org_id: params.get('org_id') ?? '' });
  const [applied, setApplied] = useState<Filters>({ ...EMPTY, org_id: params.get('org_id') ?? '' });
  const [items, setItems] = useState<AdminAuditEntry[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  const fetchPage = useCallback(async (f: Filters, nextCursor: string | null) => {
    setLoading(true);
    setError(null);
    try {
      const res = await adminApi.auditLog({
        org_id: f.org_id || undefined,
        actor: f.actor.trim() || undefined,
        entity_type: f.entity_type || undefined,
        entity_id: f.entity_id.trim() || undefined,
        q: f.q.trim() || undefined,
        from: f.from || undefined,
        to: f.to || undefined,
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
  }, []);

  useEffect(() => {
    void fetchPage(applied, null);
  }, [applied, fetchPage]);

  const orgLabel = (row: AdminAuditEntry) => {
    if (row.org_name) return row.org_name;
    const match = orgs.find((o) => o.id === row.org_id);
    if (match) return match.name;
    return row.org_id ? row.org_id.slice(0, 8) : 'Platform';
  };

  return (
    <>
      <PageHead
        title="Audit search"
        lead="Every recorded action, across every organization. Append-only, newest first."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(filters);
        }}
        style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}
      >
        <Field id="org_id" label="Organization">
          <select
            id="org_id"
            className="input"
            value={filters.org_id}
            onChange={(e) => setFilters((f) => ({ ...f, org_id: e.target.value }))}
            style={{ minWidth: 220 }}
          >
            <option value="">All organizations</option>
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
        </Field>
        <Field id="actor" label="Actor" hint="User id">
          <input
            id="actor"
            className="input"
            value={filters.actor}
            onChange={(e) => setFilters((f) => ({ ...f, actor: e.target.value }))}
            style={{ minWidth: 180 }}
          />
        </Field>
        <Field id="entity_type" label="Entity">
          <select
            id="entity_type"
            className="input"
            value={filters.entity_type}
            onChange={(e) => setFilters((f) => ({ ...f, entity_type: e.target.value }))}
            style={{ minWidth: 170 }}
          >
            <option value="">All</option>
            {ENTITY_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </Field>
        <Field id="entity_id" label="Entity id">
          <input
            id="entity_id"
            className="input"
            value={filters.entity_id}
            onChange={(e) => setFilters((f) => ({ ...f, entity_id: e.target.value }))}
            style={{ minWidth: 180 }}
          />
        </Field>
        <Field id="q" label="Text" hint="Matches action or entity">
          <input
            id="q"
            className="input"
            type="search"
            value={filters.q}
            onChange={(e) => setFilters((f) => ({ ...f, q: e.target.value }))}
            style={{ minWidth: 180 }}
          />
        </Field>
        <Field id="from" label="From">
          <input
            id="from"
            className="input"
            type="date"
            value={filters.from}
            onChange={(e) => setFilters((f) => ({ ...f, from: e.target.value }))}
          />
        </Field>
        <Field id="to" label="To">
          <input
            id="to"
            className="input"
            type="date"
            value={filters.to}
            onChange={(e) => setFilters((f) => ({ ...f, to: e.target.value }))}
          />
        </Field>
        <button type="submit" className="btn btn-secondary">
          Apply
        </button>
        <button
          type="button"
          className="btn btn-quiet"
          onClick={() => {
            setFilters(EMPTY);
            setApplied(EMPTY);
          }}
        >
          Clear
        </button>
      </form>

      <div style={{ marginTop: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
      </div>

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th>When</th>
            <th>Organization</th>
            <th>Who</th>
            <th>Action</th>
            <th>Entity</th>
            <th>IP</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && !loading ? (
            <tr>
              <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                Nothing recorded for these filters.
              </td>
            </tr>
          ) : (
            items.map((row) => (
              <Fragment key={row.id}>
                <tr>
                  <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(row.at)}</td>
                  <td>{orgLabel(row)}</td>
                  <td style={{ fontWeight: 500 }}>{row.actor_name || 'System'}</td>
                  <td>{row.action}</td>
                  <td style={{ color: 'var(--ink-soft)' }}>
                    {row.entity_type}
                    {row.entity_id ? ` · ${row.entity_id.slice(0, 8)}` : ''}
                  </td>
                  <td style={{ color: 'var(--ink-soft)' }}>{row.ip ?? '—'}</td>
                  <td>
                    <button
                      type="button"
                      className="btn btn-quiet"
                      style={{ minHeight: 32 }}
                      aria-expanded={open === row.id}
                      onClick={() => setOpen((cur) => (cur === row.id ? null : row.id))}
                    >
                      {open === row.id ? 'Hide' : 'Details'}
                    </button>
                  </td>
                </tr>
                {open === row.id ? <ExpandedRow row={row} colSpan={7} /> : null}
              </Fragment>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
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
            onClick={() => void fetchPage(applied, cursor)}
          >
            {loading ? 'Loading…' : 'Load more'}
          </button>
        </div>
      ) : null}
    </>
  );
}

export default function AuditPage() {
  return (
    <Shell>
      <Suspense fallback={<p style={{ color: 'var(--ink-soft)' }}>Loading…</p>}>
        <AuditBody />
      </Suspense>
    </Shell>
  );
}
