'use client';

/**
 * Landlord inbox (FLOWS flow 3 steps 1–2): every renter who scanned a QR and
 * asked to be linked to a unit, split by decision status. Nothing is decided
 * here — a row opens the request so the KYC can be read first.
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { ProblemNote } from '../../../components/FormBits';
import { FilterTabs, KycStamp, LinkStatusStamp } from '../../../components/RenterBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  linkRequestsApi,
  toApiError,
  type LinkRequest,
  type LinkRequestStatus,
} from '../../../lib/api';
import { Amount, fmtDate } from '../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

/** Tab values with the key of their label — the words are picked at render. */
const TABS: LinkRequestStatus[] = ['pending', 'approved', 'rejected', 'cancelled'];

function InboxBody() {
  const t = useT();
  const [status, setStatus] = useState<LinkRequestStatus>('pending');
  const [items, setItems] = useState<LinkRequest[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setItems(null);
      try {
        const res = await linkRequestsApi.list({ status, limit: 200 }, signal);
        setItems(res.items ?? []);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [status],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <>
      <PageHead title={t('nav.link_requests')} lead={t('linkreq.lead')} />

      <div style={{ marginBottom: 'var(--sp-4)' }}>
        <FilterTabs<LinkRequestStatus>
          value={status}
          options={TABS.map((v) => ({ value: v, label: t(`linkreq.status.${v}`) }))}
          onChange={setStatus}
          label={t('linkreq.filter_label')}
        />
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <TableScroll label={t('nav.link_requests')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('common.renter')}</th>
              <th>{t('common.unit')}</th>
              <th>{t('linkreq.col.period')}</th>
              <th className="num">{t('common.amount')}</th>
              <th className="num">{t('linkreq.col.term')}</th>
              <th>{t('linkreq.col.dates')}</th>
              <th>{t('common.status')}</th>
              <th>{t('linkreq.col.requested')}</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={8} style={{ color: 'var(--ink-soft)' }}>
                  {t('common.loading')}
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={8} style={{ color: 'var(--ink-soft)' }}>
                  {error ? t('common.no_results') : t(`linkreq.empty.${status}`)}
                </td>
              </tr>
            ) : (
              items.map((r) => (
                <tr key={r.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/link-requests/${r.id}`} style={{ color: 'inherit' }}>
                      {r.renter?.full_name ?? '—'}
                    </Link>
                    <div style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {r.renter?.phone ?? '—'} · <KycStamp status={r.renter?.kyc_status} />
                    </div>
                  </td>
                  <td>
                    <Link href={`/link-requests/${r.id}`} style={{ color: 'inherit' }}>
                      {r.unit?.name ?? '—'}
                    </Link>
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {r.unit?.property_name ?? ''}
                    </div>
                  </td>
                  <td>
                    {r.payment_period?.label ?? '—'}
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {r.payment_period?.days ? t.n('common.day', r.payment_period.days) : ''}
                    </div>
                  </td>
                  <td className="num">
                    <Amount value={r.payment_period?.amount ?? null} />
                  </td>
                  <td className="num">{r.term_days ? t('linkreq.term_short', { days: r.term_days }) : '—'}</td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {fmtDate(r.start_date)} → {fmtDate(r.end_date)}
                  </td>
                  <td>
                    <LinkStatusStamp status={r.status} />
                  </td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {fmtDate(r.created_at)}
                  </td>
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

export default function LinkRequestsPage() {
  return (
    <>
      <InboxBody />
    </>
  );
}
