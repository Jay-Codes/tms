'use client';

/**
 * Shared marks and helpers for the Phase 4 contract screens (SPEC §2.0):
 * what has happened is stamped, what is still waiting is only pencilled.
 */

import Link from 'next/link';
import { useEffect, useState } from 'react';
import {
  contractsApi,
  type Contract,
  type ContractSignature,
  type ContractStatus,
  type ScheduleRow,
} from '../lib/api';
import { Amount, fmtDate } from '../lib/format';
import { TableScroll, useT } from '@tms/ui';

export function ContractStatusStamp({ status }: { status: ContractStatus | string | null | undefined }) {
  const t = useT();
  if (status === 'active') return <span className="stamp stamp-paid">{t('contracts.status.active')}</span>;
  if (status === 'terminated') return <span className="stamp stamp-overdue">{t('contracts.status.terminated')}</span>;
  if (status === 'expiring') return <span className="pencil">{t('contracts.status.expiring')}</span>;
  if (status === 'pending_signature') return <span className="pencil">{t('contracts.status.pending_signature')}</span>;
  if (status === 'draft') return <span className="pencil">{t('contracts.status.draft')}</span>;
  if (status === 'ended') {
    return (
      <span className="stamp" style={{ color: 'var(--ink-soft)', borderColor: 'var(--ink-soft)' }}>
        {t('contracts.status.ended')}
      </span>
    );
  }
  return (
    <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)', textTransform: 'capitalize' }}>
      {status ?? '—'}
    </span>
  );
}

export function ScheduleStatusStamp({ status }: { status: ScheduleRow['status'] }) {
  const t = useT();
  if (status === 'paid') return <span className="stamp stamp-paid">{t('contracts.schedule.status.paid')}</span>;
  if (status === 'overdue') return <span className="stamp stamp-overdue">{t('contracts.schedule.status.overdue')}</span>;
  if (status === 'partial') return <span className="pencil">{t('contracts.schedule.status.partial')}</span>;
  if (status === 'waived') return <span className="pencil">{t('contracts.schedule.status.waived')}</span>;
  return <span className="pencil">{t('contracts.schedule.status.pending')}</span>;
}

/** True once the renter's signature row exists — the gate on Activate. */
export function renterSignature(
  contract: Pick<Contract, 'signatures'> | null | undefined,
): ContractSignature | null {
  return (contract?.signatures ?? []).find((s) => s.party === 'renter') ?? null;
}

export function landlordSignature(
  contract: Pick<Contract, 'signatures'> | null | undefined,
): ContractSignature | null {
  return (contract?.signatures ?? []).find((s) => s.party === 'landlord') ?? null;
}

/** FLOWS 3.5: pending_signature + a renter signature = the landlord's turn. */
export function isReadyToCountersign(c: Contract): boolean {
  return c.status === 'pending_signature' && renterSignature(c) !== null;
}

/**
 * Count of contracts waiting for the landlord to countersign, for the rail and
 * the dashboard card. The list endpoint has no count and "ready" is a derived
 * state, so one page of `pending_signature` is read and filtered. Errors are
 * silent — a badge must never break the chrome.
 */
export function useReadyToCountersign(): number | null {
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    contractsApi
      .list({ status: 'pending_signature', limit: 200 }, ac.signal)
      .then((r) => setCount((r.items ?? []).filter(isReadyToCountersign).length))
      .catch(() => setCount(null));
    return () => ac.abort();
  }, []);

  return count;
}

/**
 * The contracts ledger, shared by `/contracts` and a renter's record. Every
 * row links to the contract document — the list itself decides nothing.
 */
export function ContractsTable({
  items,
  loading,
  emptyText,
  showRenter = true,
}: {
  items: Contract[] | null;
  loading?: boolean;
  emptyText?: string;
  showRenter?: boolean;
}) {
  const t = useT();
  const cols = showRenter ? 6 : 5;
  return (
    <TableScroll label={t('contracts.table.label')}>
    <table className="ledger">
      <thead>
        <tr>
          <th>{t('common.unit')}</th>
          {showRenter ? <th>{t('common.renter')}</th> : null}
          <th>{t('common.status')}</th>
          <th>{t('contracts.col.term')}</th>
          <th className="num">{t('contracts.col.rent')}</th>
          <th className="num">{t('contracts.col.next_due')}</th>
        </tr>
      </thead>
      <tbody>
        {items === null || loading ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              {t('common.loading')}
            </td>
          </tr>
        ) : items.length === 0 ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              {emptyText}
            </td>
          </tr>
        ) : (
          items.map((c) => (
            <tr key={c.id}>
              <td style={{ fontWeight: 600 }}>
                <Link href={`/contracts/${c.id}`} style={{ color: 'inherit' }}>
                  {c.unit?.name ?? '—'}
                </Link>
                <div style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {c.unit?.property_name ?? ''}
                </div>
              </td>
              {showRenter ? <td>{c.renter?.full_name ?? '—'}</td> : null}
              <td>
                <ContractStatusStamp status={c.status} />
                {isReadyToCountersign(c) ? (
                  <div style={{ fontSize: 'var(--text-xs)', color: 'var(--primary)' }}>ready to countersign</div>
                ) : null}
              </td>
              <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                {fmtDate(c.start_date)} → {fmtDate(c.end_date)}
              </td>
              <td className="num">
                <Amount value={c.rent_amount} per={c.rent_period_days} />
              </td>
              <td className="num" style={{ fontSize: 'var(--text-sm)' }}>
                {c.schedules_summary?.next_due_date ? (
                  <>
                    {fmtDate(c.schedules_summary.next_due_date)}
                    <div style={{ color: 'var(--ink-soft)' }}>
                      <Amount value={c.schedules_summary.next_due_amount ?? null} />
                    </div>
                  </>
                ) : (
                  <span className="pencil">—</span>
                )}
              </td>
            </tr>
          ))
        )}
      </tbody>
    </table>
    </TableScroll>
  );
}

/** Two-column definition list (mirrors <Facts> on the renter screens). */
export function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl
      style={{
        display: 'grid',
        gridTemplateColumns: 'max-content 1fr',
        gap: 'var(--sp-2) var(--sp-4)',
        margin: 0,
      }}
    >
      {rows.map(([k, v], i) => (
        <div key={`${k}-${i}`} style={{ display: 'contents' }}>
          <dt style={{ color: 'var(--ink-soft)' }}>{k}</dt>
          <dd style={{ margin: 0 }}>{v ?? '—'}</dd>
        </div>
      ))}
    </dl>
  );
}
