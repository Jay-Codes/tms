'use client';

import { useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import { ApiError, orgApi, type AuditEntry } from '../../lib/api';

const PAGE_SIZE = 50;

/** Entity types seen in Phase 1; free text is allowed for anything later. */
const ENTITY_TYPES = ['org', 'user', 'org_member', 'session', 'property', 'unit', 'contract', 'payment'];

function formatAt(at: string): string {
  const d = new Date(at);
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString();
}

function AuditBody() {
  const [filters, setFilters] = useState({ entity_type: '', from: '', to: '' });
  const [applied, setApplied] = useState(filters);
  const [items, setItems] = useState<AuditEntry[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const fetchPage = useCallback(
    async (f: typeof filters, nextCursor: string | null) => {
      setLoading(true);
      setError(null);
      try {
        const res = await orgApi.auditLog({
          entity_type: f.entity_type || undefined,
          from: f.from || undefined,
          to: f.to || undefined,
          cursor: nextCursor || undefined,
          limit: PAGE_SIZE,
        });
        setItems((prev) => (nextCursor ? [...prev, ...(res.items ?? [])] : res.items ?? []));
        setCursor(res.next_cursor ?? null);
      } catch (e) {
        setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
        if (!nextCursor) setItems([]);
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    void fetchPage(applied, null);
  }, [applied, fetchPage]);

  return (
    <>
      <PageHead title="Audit log" lead="Every action taken in your business, newest first. Append-only." />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(filters);
        }}
        style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 'var(--sp-5)' }}
      >
        <Field id="entity_type" label="Entity">
          <select
            id="entity_type"
            className="input"
            value={filters.entity_type}
            onChange={(e) => setFilters((f) => ({ ...f, entity_type: e.target.value }))}
            style={{ minWidth: 180 }}
          >
            <option value="">All</option>
            {ENTITY_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
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
            const cleared = { entity_type: '', from: '', to: '' };
            setFilters(cleared);
            setApplied(cleared);
          }}
        >
          Clear
        </button>
      </form>

      <ProblemNote error={error} />

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th>When</th>
            <th>Who</th>
            <th>Action</th>
            <th>Entity</th>
            <th>IP</th>
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && !loading ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                Nothing recorded for these filters.
              </td>
            </tr>
          ) : (
            items.map((row) => (
              <tr key={row.id}>
                <td style={{ whiteSpace: 'nowrap' }}>{formatAt(row.at)}</td>
                <td style={{ fontWeight: 500 }}>{row.actor_name || 'System'}</td>
                <td>{row.action}</td>
                <td style={{ color: 'var(--ink-soft)' }}>
                  {row.entity_type}
                  {row.entity_id ? ` · ${row.entity_id.slice(0, 8)}` : ''}
                </td>
                <td style={{ color: 'var(--ink-soft)' }}>{row.ip ?? '—'}</td>
              </tr>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>

      {cursor ? (
        <div style={{ marginTop: 'var(--sp-4)' }}>
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void fetchPage(applied, cursor)}>
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
      <AuditBody />
    </Shell>
  );
}
