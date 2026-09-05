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
import { Field, ProblemNote } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import { ApiError, orgApi, type AuditEntry, type Member } from '../../../lib/api';
import { fmtDateTime } from '../../../lib/format';
import { TableScroll, useT, type Translator } from '@tms/ui';

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

/**
 * Every action string `internal/audit` writes. A row whose action is not in
 * this set (an older or newer build) prints the raw value rather than a key.
 */
const KNOWN_ACTIONS = new Set([
  'auth.login',
  'auth.login_failed',
  'auth.logout',
  'auth.otp_send',
  'auth.otp_verify',
  'auth.register_renter',
  'auth.verify_email',
  'auth.invite_accept',
  'user.locale_update',
  'org.create',
  'org.update',
  'org.member_invite',
  'org.member_remove',
  'org.suspend',
  'org.activate',
  'org.branding_update',
  'org.branding_asset',
  'org.bank_account_update',
  'org.notification_settings_update',
  'branding.theme_update',
  'property.create',
  'property.update',
  'property.delete',
  'unit.create',
  'unit.update',
  'unit.delete',
  'unit.qr_generate',
  'price.create',
  'price.bulk_update',
  'payment_period.create',
  'payment_period.update',
  'payment_period.delete',
  'payment_period.restore_recommended',
  'payment_period.recommend',
  'renter_profile.update',
  'kyc.upload',
  'kyc.view',
  'link_request.create',
  'link_request.cancel',
  'link_request.approve',
  'link_request.reject',
  'contract_template.create',
  'contract_template.update',
  'contract_template.delete',
  'contract.create',
  'contract.sign',
  'contract.activate',
  'contract.activate_landlord_recorded',
  'contract.terminate',
  'contract.lifecycle_run',
  'payment.record',
  'payment.reverse',
  'payment.overdue_run',
  'notification.custom',
  'notification.retry',
  'notification.scheduler_run',
  'expense_category.create',
  'expense_category.update',
  'expense_category.delete',
  'expense.create',
  'expense.update',
  'expense.void',
  'expense.receipt_attach',
  'expense.receipt_remove',
]);

/** Human name for an action; unknown actions keep their stable machine string. */
function actionLabel(t: Translator, action: string): string {
  return KNOWN_ACTIONS.has(action) ? t(`audit.action.${action}`) : action;
}

/** Human name for an entity type; unknown types keep their machine string. */
function entityLabel(t: Translator, entity: string): string {
  return ENTITY_TYPES.includes(entity) ? t(`audit.entity.${entity}`) : entity;
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
  const t = useT();
  const changed = changedKeys(row.before, row.after);
  return (
    <tr>
      <td colSpan={cols} style={{ height: 'auto', padding: 'var(--sp-4) 0' }}>
        <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {row.entity_id ? (
              <>
                <strong style={{ color: 'var(--ink)' }}>{entityLabel(t, row.entity_type)}</strong> {row.entity_id}
              </>
            ) : (
              entityLabel(t, row.entity_type)
            )}
            {changed.length
              ? ` · ${t('audit.changed', { fields: changed.join(', ') })}`
              : ` · ${t('audit.no_change')}`}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: 'var(--sp-4)' }}>
            <Pre label={t('audit.before')} value={row.before} tone="before" />
            <Pre label={t('audit.after')} value={row.after} tone="after" />
          </div>
        </div>
      </td>
    </tr>
  );
}

function AuditBody() {
  const t = useT();
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
      <PageHead title={t('audit.title')} lead={t('audit.lead')} />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(filters);
        }}
        style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 'var(--sp-5)' }}
      >
        <Field id="actor" label={t('audit.actor')}>
          <select
            id="actor"
            className="input"
            value={filters.actor}
            onChange={(e) => setFilters((f) => ({ ...f, actor: e.target.value }))}
            style={{ minWidth: 200 }}
          >
            <option value="">{t('audit.actor.anyone')}</option>
            {members.map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.full_name || m.email}
              </option>
            ))}
          </select>
        </Field>
        <Field id="entity_type" label={t('audit.entity')}>
          <select
            id="entity_type"
            className="input"
            value={filters.entity_type}
            onChange={(e) => setFilters((f) => ({ ...f, entity_type: e.target.value }))}
            style={{ minWidth: 180 }}
          >
            <option value="">{t('common.all')}</option>
            {ENTITY_TYPES.map((e) => (
              <option key={e} value={e}>
                {entityLabel(t, e)}
              </option>
            ))}
          </select>
        </Field>
        <Field id="entity_id" label={t('audit.entity_id')} hint={t('audit.entity_id.hint')}>
          <input
            id="entity_id"
            className="input"
            value={filters.entity_id}
            placeholder={t('audit.entity_id.placeholder')}
            onChange={(e) => setFilters((f) => ({ ...f, entity_id: e.target.value }))}
            style={{ minWidth: 220 }}
          />
        </Field>
        <Field id="from" label={t('common.from')}>
          <input
            id="from"
            className="input"
            type="date"
            value={filters.from}
            onChange={(e) => setFilters((f) => ({ ...f, from: e.target.value }))}
          />
        </Field>
        <Field id="to" label={t('common.to')}>
          <input
            id="to"
            className="input"
            type="date"
            value={filters.to}
            onChange={(e) => setFilters((f) => ({ ...f, to: e.target.value }))}
          />
        </Field>
        <button type="submit" className="btn btn-secondary">
          {t('common.apply')}
        </button>
        <button
          type="button"
          className="btn btn-quiet"
          onClick={() => {
            setFilters(EMPTY);
            setApplied(EMPTY);
          }}
        >
          {t('common.clear')}
        </button>
      </form>

      <ProblemNote error={error} />

      <TableScroll label={t('audit.table_label')}>
      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th style={{ width: 40 }} />
            <th>{t('audit.col.when')}</th>
            <th>{t('audit.col.who')}</th>
            <th>{t('audit.col.action')}</th>
            <th>{t('audit.entity')}</th>
            <th>{t('audit.col.ip')}</th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && !loading ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                {t('audit.empty')}
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
                        aria-label={expanded ? t('audit.toggle.hide') : t('audit.toggle.show')}
                        onClick={() => setOpen(expanded ? null : row.id)}
                        style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
                      >
                        <Icon icon={expanded ? 'solar:alt-arrow-down-linear' : 'solar:alt-arrow-right-linear'} width={18} />
                      </button>
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(row.at)}</td>
                    <td style={{ fontWeight: 500 }}>{row.actor_name || t('audit.system')}</td>
                    <td>{actionLabel(t, row.action)}</td>
                    <td style={{ color: 'var(--ink-soft)' }}>
                      {entityLabel(t, row.entity_type)}
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
                {t('common.loading')}
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
      </TableScroll>

      {cursor ? (
        <div style={{ marginTop: 'var(--sp-4)' }}>
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void fetchPage(applied, cursor)}>
            {loading ? t('common.loading_more') : t('common.load_more')}
          </button>
        </div>
      ) : null}
    </>
  );
}

export default function AuditPage() {
  return (
    <>
      <AuditBody />
    </>
  );
}
