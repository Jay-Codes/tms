'use client';

/**
 * Audit trail (FLOWS flow 10). Append-only, org-scoped, newest first, filtered
 * by actor, entity, entity id and date range. A row opens to show what the
 * record looked like before the action and after it, side by side.
 *
 * `from`/`to` are date boxes but the endpoint takes RFC3339, so a day is sent
 * as the whole day: `from` at 00:00 and `to` at the end of that day, both in
 * the reader's own zone — pick "5 Sep" for both and you get 5 September.
 *
 * `entity_id` is not a server-side filter in this contract (API.md Phase 1
 * lists `entity_type`, `actor`, `from`, `to`, `cursor`, `limit`), so it narrows
 * the rows already loaded and the hint says so.
 */

import { Icon } from '@iconify/react';
import { Fragment, useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import { ApiError, orgApi, type AuditEntry, type Member } from '../../lib/api';

const PAGE_SIZE = 50;

/** The entity types the backend actually writes (`internal/audit`), alphabetical. */
const ENTITY_TYPES = [
  'bank_account',
  'contract',
  'contract_template',
  'link_request',
  'notification',
  'org',
  'org_branding',
  'org_member',
  'payment',
  'payment_period',
  'payment_schedule',
  'price_plan',
  'property',
  'renter_profile',
  'session',
  'unit',
  'user',
];

interface Filters {
  actor: string;
  entity_type: string;
  entity_id: string;
  from: string;
  to: string;
}

const EMPTY: Filters = { actor: '', entity_type: '', entity_id: '', from: '', to: '' };

function formatAt(at: string): string {
  const d = new Date(at);
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString();
}

/** `2026-09-05` → start of that day, RFC3339 in the reader's zone. */
function dayStart(date: string): string | undefined {
  if (!date) return undefined;
  const d = new Date(`${date}T00:00:00`);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

/** `2026-09-05` → the last instant of that day, so "to" includes the day picked. */
function dayEnd(date: string): string | undefined {
  if (!date) return undefined;
  const d = new Date(`${date}T23:59:59.999`);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

/** Pretty-print a before/after blob; `null` reads as "nothing was there". */
function json(value: unknown): string {
  if (value === null || value === undefined) return '—';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/** Keys whose value differs between before and after — the changed lines. */
function changedKeys(before: unknown, after: unknown): string[] {
  const b = (before ?? {}) as Record<string, unknown>;
  const a = (after ?? {}) as Record<string, unknown>;
  if (typeof b !== 'object' || typeof a !== 'object') return [];
  const keys = new Set([...Object.keys(b), ...Object.keys(a)]);
  return [...keys].filter((k) => JSON.stringify(b[k]) !== JSON.stringify(a[k])).sort();
}

function Pre({ label, value, tone }: { label: string; value: unknown; tone: 'before' | 'after' }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div
        style={{
          fontSize: 'var(--text-xs)',
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          color: tone === 'after' ? 'var(--stamp-paid)' : 'var(--ink-soft)',
          marginBottom: 'var(--sp-2)',
        }}
      >
        {label}
      </div>
      <pre
        style={{
          margin: 0,
          padding: 'var(--sp-3)',
          background: 'var(--sheet-tint)',
          border: '1px solid var(--rule)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-xs)',
          lineHeight: 1.5,
          overflowX: 'auto',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}
      >
        {json(value)}
      </pre>
    </div>
  );
}

function DiffRow({ row, cols }: { row: AuditEntry; cols: number }) {
  const changed = changedKeys(row.before, row.after);
  return (
    <tr>
      <td colSpan={cols} style={{ height: 'auto', padding: 'var(--sp-4) 0' }}>
        <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {row.entity_id ? (
              <>
                <strong style={{ color: 'var(--ink)' }}>{row.entity_type}</strong> {row.entity_id}
              </>
            ) : (
              row.entity_type
            )}
            {changed.length ? ` · changed: ${changed.join(', ')}` : ' · no field-level change recorded'}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: 'var(--sp-4)' }}>
            <Pre label="Before" value={row.before} tone="before" />
            <Pre label="After" value={row.after} tone="after" />
          </div>
        </div>
      </td>
    </tr>
  );
}

function AuditBody() {
  const [filters, setFilters] = useState<Filters>(EMPTY);
  const [applied, setApplied] = useState<Filters>(EMPTY);
  const [items, setItems] = useState<AuditEntry[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    orgApi
      .members(ac.signal)
      .then((r) => setMembers(r.items ?? []))
      .catch(() => setMembers([]));
    return () => ac.abort();
  }, []);

  const fetchPage = useCallback(async (f: Filters, nextCursor: string | null) => {
    setLoading(true);
    setError(null);
    try {
      const res = await orgApi.auditLog({
        entity_type: f.entity_type || undefined,
        actor: f.actor || undefined,
        from: dayStart(f.from),
        to: dayEnd(f.to),
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
  }, []);

  useEffect(() => {
    setOpen(null);
    void fetchPage(applied, null);
  }, [applied, fetchPage]);

  const needle = applied.entity_id.trim().toLowerCase();
  const rows = needle
    ? items.filter((r) => (r.entity_id ?? '').toLowerCase().includes(needle))
    : items;

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
        <Field id="actor" label="Actor">
          <select
            id="actor"
            className="input"
            value={filters.actor}
            onChange={(e) => setFilters((f) => ({ ...f, actor: e.target.value }))}
            style={{ minWidth: 200 }}
          >
            <option value="">Anyone</option>
            {members.map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.full_name || m.email}
              </option>
            ))}
          </select>
        </Field>
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
        <Field id="entity_id" label="Entity id" hint="Narrows the rows loaded below.">
          <input
            id="entity_id"
            className="input"
            value={filters.entity_id}
            placeholder="Paste an id"
            onChange={(e) => setFilters((f) => ({ ...f, entity_id: e.target.value }))}
            style={{ minWidth: 220 }}
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

      <ProblemNote error={error} />

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th style={{ width: 40 }} />
            <th>When</th>
            <th>Who</th>
            <th>Action</th>
            <th>Entity</th>
            <th>IP</th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && !loading ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                Nothing recorded for these filters.
              </td>
            </tr>
          ) : (
            rows.map((row) => {
              const expanded = open === row.id;
              return (
                <Fragment key={row.id}>
                  <tr>
                    <td>
                      <button
                        type="button"
                        className="btn btn-quiet"
                        aria-expanded={expanded}
                        aria-label={expanded ? 'Hide changes' : 'Show changes'}
                        onClick={() => setOpen(expanded ? null : row.id)}
                        style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
                      >
                        <Icon icon={expanded ? 'solar:alt-arrow-down-linear' : 'solar:alt-arrow-right-linear'} width={18} />
                      </button>
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>{formatAt(row.at)}</td>
                    <td style={{ fontWeight: 500 }}>{row.actor_name || 'System'}</td>
                    <td>{row.action}</td>
                    <td style={{ color: 'var(--ink-soft)' }}>
                      {row.entity_type}
                      {row.entity_id ? ` · ${row.entity_id.slice(0, 8)}` : ''}
                    </td>
                    <td style={{ color: 'var(--ink-soft)' }}>{row.ip ?? '—'}</td>
                  </tr>
                  {expanded ? <DiffRow row={row} cols={6} /> : null}
                </Fragment>
              );
            })
          )}
          {loading ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
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
