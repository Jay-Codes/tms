'use client';

/**
 * One renter's record: KYC profile (NIDA masked by the server, never in full),
 * every link request they sent this org, and their contracts (Phase 4).
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { ContractsTable } from '../../../../components/ContractBits';
import { ProblemNote } from '../../../../components/FormBits';
import { NotificationLogTable } from '../../../../components/NotificationBits';
import { PaymentsTable } from '../../../../components/PaymentBits';
import { ProofsFor } from '../../../../components/ProofBits';
import { StatTile, TileRow } from '../../../../components/ReportBits';
import { Facts, KycStamp, LinkStatusStamp, ViewIdDocButton, localeLabel } from '../../../../components/RenterBits';
import { PageHead } from '../../../../components/PageHead';
import {
  ApiError,
  contractsApi,
  hasKycDoc,
  notificationsApi,
  paymentsApi,
  rentersApi,
  toApiError,
  type Contract,
  type NotificationLogEntry,
  type Payment,
  type RenterDetail,
} from '../../../../lib/api';
import { Amount, fmtDate, fmtTZS } from '../../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

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
  const t = useT();
  const [data, setData] = useState<RenterDetail | null>(null);
  const [contractRows, setContractRows] = useState<Contract[] | null>(null);
  const [payments, setPayments] = useState<Payment[] | null>(null);
  const [messages, setMessages] = useState<NotificationLogEntry[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  // Last twenty SMS to this renter (API.md Phase 6). Its own read, so a
  // notification outage cannot take the KYC record down with it.
  useEffect(() => {
    const ac = new AbortController();
    notificationsApi
      .log({ user_id: userId, limit: 20 }, ac.signal)
      .then((r) => setMessages(r.items ?? []))
      .catch((e) => {
        if (!(e instanceof DOMException)) setMessages([]);
      });
    return () => ac.abort();
  }, [userId]);

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
        <PageHead title={t('common.renter')} />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/renters" className="btn btn-quiet">
            {t('renters.back_to_directory')}
          </Link>
        </p>
      </>
    );
  }

  if (!data) {
    return (
      <>
        <PageHead title={t('common.renter')} />
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
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
        title={profile?.full_name || renter?.full_name || t('common.renter')}
        lead={renter?.phone ?? undefined}
        actions={
          <Link href="/renters" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> {t('renters.directory')}
          </Link>
        }
      />

      <div style={{ marginBottom: 'var(--sp-4)' }}>
        <KycStamp status={profile?.kyc_status ?? renter?.kyc_status} />
      </div>

      {/* Phase 16 §16.3 — what this renter owes, before the record itself.
          Both figures are the backend's, aggregated over their running
          tenancies with this org. */}
      {renter?.next_due_date || (renter?.overdue_amount ?? 0) > 0 ? (
        <div style={{ marginBottom: 'var(--sp-5)' }}>
          <TileRow min={220}>
            <StatTile
              label={t('renters.next_due')}
              value={renter?.next_due_amount != null ? fmtTZS(renter.next_due_amount) : '—'}
              sub={renter?.next_due_date ? fmtDate(renter.next_due_date) : t('renters.next_due.none')}
            />
            {(renter?.overdue_amount ?? 0) > 0 ? (
              <StatTile
                label={t('renters.overdue')}
                tone="overdue"
                value={fmtTZS(renter?.overdue_amount ?? 0)}
                sub={t('renters.overdue.sub')}
              />
            ) : null}
          </TileRow>
        </div>
      ) : null}

      <Section title={t('renters.section.profile')}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <Facts
            rows={[
              [t('renters.field.full_name'), profile?.full_name ?? renter?.full_name ?? '—'],
              [t('common.phone'), renter?.phone ?? '—'],
              [t('common.email'), profile?.email ?? renter?.email ?? '—'],
              [t('renters.locale'), localeLabel(t, renter?.locale)],
              [
                t('renters.field.nida'),
                <span key="nida" className="num" style={{ letterSpacing: '0.08em' }}>
                  {profile?.nida_masked ?? '—'}
                </span>,
              ],
              [t('renters.field.next_of_kin'), profile?.next_of_kin_name ?? '—'],
              [t('renters.field.next_of_kin_phone'), profile?.next_of_kin_phone ?? '—'],
              [t('renters.col.known_since'), fmtDate(renter?.created_at)],
            ]}
          />
          <ViewIdDocButton
            userId={userId}
            disabled={!docOnFile}
            disabledReason={docOnFile ? undefined : t('renters.kyc.no_doc')}
          />
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
            {t('renters.nida_note')}
          </p>
        </div>
      </Section>

      <Section title={t('nav.units')}>
        {(renter?.units ?? []).length === 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('renters.units.empty')}</p>
        ) : (
          <TableScroll label={t('renters.units.table_label')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('common.unit')}</th>
                <th>{t('common.property')}</th>
                <th>{t('renters.col.link_status')}</th>
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
          </TableScroll>
        )}
      </Section>

      <Section title={t('nav.link_requests')}>
        {requests.length === 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('renters.requests.empty')}</p>
        ) : (
          <TableScroll label={t('nav.link_requests')}>
          <table className="ledger">
            <thead>
              <tr>
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
                  <td className="num">{r.term_days ? t('linkreq.term_short', { days: r.term_days }) : '—'}</td>
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
          </TableScroll>
        )}
      </Section>

      <Section title={t('nav.contracts')}>
        <ContractsTable items={contracts} showRenter={false} emptyText={t('renters.contracts.empty')} />
      </Section>

      <Section title={t('renters.section.payments')}>
        <PaymentsTable
          items={payments}
          showRenter={false}
          emptyText={t('renters.payments.empty')}
        />
      </Section>

      <Section title={t('proofs.title')}>
        <ProofsFor renterUserId={userId} />
      </Section>

      <Section title={t('nav.messages')}>
        <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <NotificationLogTable
            items={messages}
            showRenter={false}
            emptyText={t('renters.messages.empty')}
          />
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
            {t('renters.messages.note')}{' '}
            <Link href="/notifications" style={{ color: 'var(--primary)' }}>
              {t('renters.messages.full_log')}
            </Link>
            .
          </p>
        </div>
      </Section>
    </>
  );
}

export default function RenterPage() {
  const params = useParams<{ user_id: string }>();
  const userId = String(params?.user_id ?? '');
  return (
    <>
      <RenterBody userId={userId} />
    </>
  );
}
