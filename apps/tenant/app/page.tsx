'use client';

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useState, type ReactNode } from 'react';
import { PageHead, Shell } from '../components/Shell';
import { propertiesApi, type Property } from '../lib/api';
import { useMe } from '../lib/auth';

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

function DashboardBody() {
  const { user, org } = useMe();
  const [firstProperty, setFirstProperty] = useState<Property | null>(null);

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

      <p style={{ marginTop: 'var(--sp-5)' }}>
        Nothing has been recorded yet. Add your first property and units, then print the QR stickers so
        renters can connect themselves.
      </p>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
          gap: 'var(--sp-4)',
          marginTop: 'var(--sp-5)',
        }}
      >
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
