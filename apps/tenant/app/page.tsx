'use client';

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useState, type ReactNode } from 'react';
import { useReadyToCountersign } from '../components/ContractBits';
import { PageHead, Shell, pendingLabel, usePendingLinkRequests } from '../components/Shell';
import { paymentsApi, propertiesApi, schedulesApi, type Property } from '../lib/api';
import { useMe } from '../lib/auth';
import { fmtTZS, monthStartISO } from '../lib/format';

/**
 * Dashboard shell (FLOWS flow 1 step 4). Ledger data arrives in later phases;
 * for now the page shows the org identity and the three setup prompts.
 */

function EmptyCard({
  icon,
  title,
  body,
  action,
}: {
  icon: string;
  title: string;
  body: string;
  action: ReactNode;
}) {
  return (
    <div className="sheet" style={{ padding: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-3)', alignContent: 'start' }}>
      <Icon icon={icon} width={24} />
      <h2 style={{ fontSize: 'var(--text-lg)' }}>{title}</h2>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{body}</p>
      <div>{action}</div>
    </div>
  );
}

/**
 * The two numbers a landlord opens the app for (FLOWS flow 9): what is late and
 * what came in this month. Both are read straight off the Phase 5 endpoints and
 * summed for display only — the backend still owns every figure. A failure is
 * silent and the card simply says nothing, because a dashboard tile must never
 * take the page with it.
 */
function useCollections() {
  const [overdue, setOverdue] = useState<{ count: number; total: number } | null>(null);
  const [collected, setCollected] = useState<number | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    schedulesApi
      .list({ status: 'overdue', limit: 200 }, ac.signal)
      .then((r) => {
        const rows = r.items ?? [];
        setOverdue({
          count: rows.length,
          total: rows.reduce((t, s) => t + Math.max(0, (s.amount ?? 0) - (s.paid_amount ?? 0)), 0),
        });
      })
      .catch(() => setOverdue(null));
    paymentsApi
      .list({ from: monthStartISO(), limit: 200 }, ac.signal)
      .then((r) =>
        setCollected(
          (r.items ?? [])
            .filter((p) => p.status !== 'reversed')
            .reduce((t, p) => t + (p.amount ?? 0), 0),
        ),
      )
      .catch(() => setCollected(null));
    return () => ac.abort();
  }, []);

  return { overdue, collected };
}

function MoneyCard({
  icon,
  label,
  value,
  sub,
  tone,
  href,
  cta,
}: {
  icon: string;
  label: string;
  value: string;
  sub?: string;
  tone?: 'overdue';
  href: string;
  cta: string;
}) {
  return (
    <div className="sheet" style={{ padding: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-2)', alignContent: 'start' }}>
      <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        <Icon icon={icon} width={18} /> {label}
      </span>
      <strong
        style={{
          fontSize: 'var(--text-2xl)',
          color: tone === 'overdue' ? 'var(--stamp-overdue)' : 'var(--ink)',
        }}
      >
        {value}
      </strong>
      {sub ? <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{sub}</span> : null}
      <div style={{ marginTop: 'var(--sp-2)' }}>
        <Link href={href} className="btn btn-quiet" style={{ minHeight: 36 }}>
          {cta}
        </Link>
      </div>
    </div>
  );
}

function DashboardBody() {
  const { user, org } = useMe();
  const [firstProperty, setFirstProperty] = useState<Property | null>(null);
  const pending = usePendingLinkRequests();
  const countersign = useReadyToCountersign();
  const { overdue, collected } = useCollections();

  // The QR empty-state card jumps straight to a printable sheet once there is
  // something to print; until then it points at the properties screen.
  useEffect(() => {
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 1 }, ac.signal)
      .then((r) => setFirstProperty((r.items ?? [])[0] ?? null))
      .catch(() => setFirstProperty(null));
    return () => ac.abort();
  }, []);

  const today = new Date().toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  });

  return (
    <>
      <PageHead
        title={org?.name ?? 'Dashboard'}
        lead={`${today} · signed in as ${user?.full_name ?? ''}`}
        actions={
          <Link href="/setup" className="btn btn-primary">
            <Icon icon="solar:checklist-minimalistic-linear" width={20} /> Finish setup
          </Link>
        }
      />

      <hr className="rule rule-strong" />

      {overdue || collected !== null ? (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
            gap: 'var(--sp-4)',
            marginTop: 'var(--sp-5)',
          }}
        >
          {overdue ? (
            <MoneyCard
              icon="solar:bell-bing-linear"
              label="Overdue"
              tone={overdue.count > 0 ? 'overdue' : undefined}
              value={
                overdue.count === 0
                  ? 'Nothing overdue'
                  : `${overdue.count} · ${fmtTZS(overdue.total)}`
              }
              sub={
                overdue.count === 0
                  ? 'Every payment that has fallen due has been settled.'
                  : `${overdue.count} payment${overdue.count === 1 ? '' : 's'} past their due date.`
              }
              href="/payments?tab=overdue"
              cta={overdue.count === 0 ? 'Open payments' : 'Chase them'}
            />
          ) : null}
          {collected !== null ? (
            <MoneyCard
              icon="solar:wallet-money-linear"
              label="Collected this month"
              value={fmtTZS(collected)}
              sub="Payments recorded since the first of the month, reversals excluded."
              href="/payments?tab=history"
              cta="Payment history"
            />
          ) : null}
        </div>
      ) : null}

      {firstProperty === null ? (
        <p style={{ marginTop: 'var(--sp-5)' }}>
          Nothing has been recorded yet. Add your first property and units, then print the QR stickers so
          renters can connect themselves.
        </p>
      ) : null}

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
          gap: 'var(--sp-4)',
          marginTop: 'var(--sp-5)',
        }}
      >
        {/* FLOWS flow 3 step 1 — the pending badge lives here as well as in the rail. */}
        <EmptyCard
          icon="solar:inbox-in-linear"
          title={
            pending ? `${pendingLabel(pending)} link request${pending === 1 ? '' : 's'} waiting` : 'Link requests'
          }
          body={
            pending
              ? 'A renter scanned one of your QR codes. Read their KYC, then approve or reject.'
              : 'When a renter scans a unit QR code and asks to be linked, the request lands here.'
          }
          action={
            <Link href="/link-requests" className={pending ? 'btn btn-primary' : 'btn btn-secondary'}>
              {pending ? 'Review requests' : 'Open the inbox'}
            </Link>
          }
        />
        {/* FLOWS flow 3 step 5 — the landlord's turn, once the renter has signed. */}
        <EmptyCard
          icon="solar:document-text-linear"
          title={countersign ? `Ready to countersign: ${countersign}` : 'Contracts'}
          body={
            countersign
              ? 'A renter has signed. Activating countersigns the contract, writes the payment schedule and marks the unit occupied.'
              : 'Contracts you have issued, what each is waiting for, and the terms they are written from.'
          }
          action={
            <Link href="/contracts" className={countersign ? 'btn btn-primary' : 'btn btn-secondary'}>
              {countersign ? 'Countersign now' : 'Open contracts'}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:qr-code-linear"
          title="Print QR codes"
          body="Each unit gets a permanent sticker. A renter scans it and starts their own registration."
          action={
            <Link
              href={firstProperty ? `/properties/${firstProperty.id}/qr` : '/properties'}
              className="btn btn-secondary"
            >
              {firstProperty ? 'Print QR sheet' : 'Set up units'}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:users-group-rounded-linear"
          title="Invite staff"
          body="Give a manager access to record payments and approve renters, without sharing your password."
          action={
            <Link href="/settings" className="btn btn-secondary">
              Invite a manager
            </Link>
          }
        />
        <EmptyCard
          icon="solar:checklist-minimalistic-linear"
          title="Finish setup"
          body="Branding, first property, payment periods, units, contract template and reminders."
          action={
            <Link href="/setup" className="btn btn-secondary">
              Open the wizard
            </Link>
          }
        />
      </div>
    </>
  );
}

export default function DashboardPage() {
  return (
    <Shell>
      <DashboardBody />
    </Shell>
  );
}
