'use client';

/**
 * One renter's record: KYC profile (NIDA masked by the server, never in full),
 * every link request they sent this org, and their contracts (Phase 4).
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { ContractsTable } from '../../../components/ContractBits';
import { ProblemNote } from '../../../components/FormBits';
import { PaymentsTable } from '../../../components/PaymentBits';
import { Facts, KycStamp, LinkStatusStamp, ViewIdDocButton } from '../../../components/RenterBits';
import { PageHead, Shell } from '../../../components/Shell';
import {
  ApiError,
  contractsApi,
  hasKycDoc,
  paymentsApi,
  rentersApi,
  toApiError,
  type Contract,
  type Payment,
  type RenterDetail,
} from '../../../lib/api';
import { Amount, fmtDate } from '../../../lib/format';

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ paddingTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{title}</h2>
      {children}
    </section>
  );
}

function RenterBody({ userId }: { userId: string }) {
  const [data, setData] = useState<RenterDetail | null>(null);
  const [contractRows, setContractRows] = useState<Contract[] | null>(null);
  const [payments, setPayments] = useState<Payment[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  // Payment history across every contract this renter has with the org. It is
  // a read on its own so a Phase 5 outage cannot take the KYC record down.
  useEffect(() => {
    const ac = new AbortController();
    paymentsApi
      .list({ renter_user_id: userId, limit: 200 }, ac.signal)
      .then((r) => setPayments(r.items ?? []))
      .catch((e) => {
        if (!(e instanceof DOMException)) setPayments([]);
      });
    return () => ac.abort();
  }, [userId]);

  useEffect(() => {
    const ac = new AbortController();
    contractsApi
      .list({ renter_user_id: userId, limit: 200 }, ac.signal)
      .then((r) => setContractRows(r.items ?? []))
      .catch(() => setContractRows(null));
    return () => ac.abort();
  }, [userId]);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setData(await rentersApi.get(userId, signal));
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [userId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  if (error && !data) {
    return (
      <>
        <PageHead title="Renter" />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/renters" className="btn btn-quiet">
            Back to the directory
          </Link>
        </p>
      </>
    );
  }

  if (!data) {
    return (
      <>
        <PageHead title="Renter" />
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      </>
    );
  }

  const renter = data.renter;
  const profile = data.profile;
  const requests = data.link_requests ?? [];
  // `GET /renters/{id}` carries contracts too, but the list endpoint is the one
  // that fills in `schedules_summary`, so it wins when it has answered.
  const contracts = contractRows ?? data.contracts ?? [];
  const docOnFile = hasKycDoc(profile);

  return (
    <>
      <PageHead
        title={profile?.full_name || renter?.full_name || 'Renter'}
        lead={renter?.phone ?? undefined}
        actions={
          <Link href="/renters" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> Directory
          </Link>
        }
      />

      <div style={{ marginBottom: 'var(--sp-4)' }}>
        <KycStamp status={profile?.kyc_status ?? renter?.kyc_status} />
      </div>

      <Section title="Profile">
        <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <Facts
            rows={[
              ['Full name', profile?.full_name ?? renter?.full_name ?? '—'],
              ['Phone', renter?.phone ?? '—'],
              ['Email', profile?.email ?? renter?.email ?? '—'],
              [
                'NIDA',
                <span key="nida" className="num" style={{ letterSpacing: '0.08em' }}>
                  {profile?.nida_masked ?? '—'}
                </span>,
              ],
              ['Next of kin', profile?.next_of_kin_name ?? '—'],
              ['Next of kin phone', profile?.next_of_kin_phone ?? '—'],
              ['Known since', fmtDate(renter?.created_at)],
            ]}
          />
          <ViewIdDocButton
            userId={userId}
            disabled={!docOnFile}
            disabledReason={docOnFile ? undefined : 'No ID photo was uploaded.'}
          />
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
            The NIDA number is stored encrypted; only the last four digits are ever shown.
          </p>
        </div>
      </Section>

      <Section title="Units">
        {(renter?.units ?? []).length === 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>Not linked to any of your units yet.</p>
        ) : (
          <table className="ledger">
            <thead>
              <tr>
                <th>Unit</th>
                <th>Property</th>
                <th>Link status</th>
              </tr>
            </thead>
            <tbody>
              {(renter?.units ?? []).map((u) => (
                <tr key={`${u.unit_id}-${u.link_status}`}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/units/${u.unit_id}`} style={{ color: 'inherit' }}>
                      {u.unit_name}
                    </Link>
                  </td>
                  <td style={{ color: 'var(--ink-soft)' }}>{u.property_name}</td>
                  <td>
                    <LinkStatusStamp status={u.link_status} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>

      <Section title="Link requests">
        {requests.length === 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>No link requests from this renter.</p>
        ) : (
          <table className="ledger">
            <thead>
              <tr>
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
              {requests.map((r) => (
                <tr key={r.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/link-requests/${r.id}`} style={{ color: 'inherit' }}>
                      {r.unit?.name ?? '—'}
                    </Link>
                    <div style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {r.unit?.property_name ?? ''}
                    </div>
                  </td>
                  <td>{r.payment_period?.label ?? '—'}</td>
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
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(r.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>

      <Section title="Contracts">
        <ContractsTable items={contracts} showRenter={false} emptyText="No contracts with this renter yet." />
      </Section>

      <Section title="Payment history">
        <PaymentsTable
          items={payments}
          showRenter={false}
          emptyText="No payments recorded from this renter yet."
        />
      </Section>
    </>
  );
}

export default function RenterPage() {
  const params = useParams<{ user_id: string }>();
  const userId = String(params?.user_id ?? '');
  return (
    <Shell>
      <RenterBody userId={userId} />
    </Shell>
  );
}
