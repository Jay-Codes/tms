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
import { TableScroll } from '@tms/ui';

const TABS: { value: LinkRequestStatus; label: string }[] = [
  { value: 'pending', label: 'Pending' },
  { value: 'approved', label: 'Approved' },
  { value: 'rejected', label: 'Rejected' },
  { value: 'cancelled', label: 'Cancelled' },
];

function InboxBody() {
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
      <PageHead
        title="Link requests"
        lead="Renters asking to be connected to one of your units. Open a request to read their KYC before deciding."
      />

      <div style={{ marginBottom: 'var(--sp-4)' }}>
        <FilterTabs<LinkRequestStatus> value={status} options={TABS} onChange={setStatus} label="Request status" />
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        <TableScroll label="Link requests">
        <table className="ledger">
          <thead>
            <tr>
              <th>Renter</th>
              <th>Unit</th>
              <th>Period</th>
              <th className="num">Amount</th>
              <th className="num">Term</th>
              <th>Dates</th>
              <th>Status</th>
              <th>Requested</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={8} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={8} style={{ color: 'var(--ink-soft)' }}>
                  {error ? 'Nothing to show.' : `No ${status} requests.`}
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
                      {r.payment_period?.days ? `${r.payment_period.days} days` : ''}
                    </div>
                  </td>
                  <td className="num">
                    <Amount value={r.payment_period?.amount ?? null} />
                  </td>
                  <td className="num">{r.term_days ? `${r.term_days} d` : '—'}</td>
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
