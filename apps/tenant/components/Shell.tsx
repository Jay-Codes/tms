'use client';

/**
 * Authenticated portal chrome: org header, left tab rail, verification banner.
 * Wrap every protected page body in <Shell>.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState, type ReactNode } from 'react';
import { ApiError, authApi, linkRequestsApi } from '../lib/api';
import { RequireAuth, useMe } from '../lib/auth';
import { loadAndApplyOrgTheme } from '../lib/branding';
import { useReadyToCountersign } from './ContractBits';

/** How many pending requests the badge will count before it gives up and says "50+". */
const PENDING_BADGE_CAP = 50;

/**
 * Pending link-request count for the nav badge (FLOWS flow 3 step 1). The list
 * endpoint has no count, so we ask for one page and read its length; a full
 * page means "at least this many". Errors are silent — a badge must never
 * break the chrome.
 */
export function usePendingLinkRequests(): number | null {
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    linkRequestsApi
      .list({ status: 'pending', limit: PENDING_BADGE_CAP }, ac.signal)
      .then((r) => setCount(typeof r.total === 'number' ? r.total : (r.items ?? []).length))
      .catch(() => setCount(null));
    return () => ac.abort();
  }, []);

  return count;
}

export function pendingLabel(count: number): string {
  return count >= PENDING_BADGE_CAP ? `${PENDING_BADGE_CAP}+` : String(count);
}

export interface NavItem {
  href: string;
  label: string;
  icon: string;
  /** Phase in which the screen ships; undefined = functional now. */
  phase?: number;
  /** Indented links shown under this one when the section is open. */
  sub?: { href: string; label: string }[];
}

export const NAV: NavItem[] = [
  { href: '/', label: 'Dashboard', icon: 'solar:home-2-linear' },
  { href: '/properties', label: 'Properties', icon: 'solar:buildings-2-linear' },
  { href: '/units', label: 'Units', icon: 'solar:widget-4-linear' },
  { href: '/link-requests', label: 'Link requests', icon: 'solar:inbox-in-linear' },
  { href: '/renters', label: 'Renters', icon: 'solar:users-group-rounded-linear' },
  {
    href: '/contracts',
    label: 'Contracts',
    icon: 'solar:document-text-linear',
    sub: [{ href: '/contracts/templates', label: 'Templates' }],
  },
  { href: '/payments', label: 'Payments', icon: 'solar:wallet-money-linear', phase: 5 },
  { href: '/reports', label: 'Reports', icon: 'solar:chart-square-linear', phase: 7 },
  {
    href: '/settings',
    label: 'Settings',
    icon: 'solar:settings-linear',
    sub: [{ href: '/settings/branding', label: 'Branding' }],
  },
  { href: '/audit', label: 'Audit', icon: 'solar:history-linear' },
];

function VerifyBanner() {
  const { user } = useMe();
  const [state, setState] = useState<'idle' | 'sending' | 'sent'>('idle');
  const [error, setError] = useState<ApiError | null>(null);

  if (!user || user.email_verified) return null;

  const resend = async () => {
    setState('sending');
    setError(null);
    try {
      await authApi.resendVerification();
      setState('sent');
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
      setState('idle');
    }
  };

  return (
    <div
      className="shell-banner"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-3)',
        flexWrap: 'wrap',
        padding: 'var(--sp-2) var(--sp-6)',
        borderBottom: '1px solid var(--rule)',
        background: 'var(--primary-soft)',
        fontSize: 'var(--text-sm)',
      }}
    >
      <Icon icon="solar:letter-linear" width={20} />
      <span>
        {state === 'sent'
          ? 'Verification email sent. Open the link to confirm your address.'
          : `Your email ${user.email ?? ''} is not verified yet.`}
      </span>
      {error ? <span style={{ color: 'var(--stamp-overdue)' }}>{error.detail}</span> : null}
      {state !== 'sent' ? (
        <button type="button" className="btn btn-quiet" onClick={resend} disabled={state === 'sending'} style={{ minHeight: 32 }}>
          {state === 'sending' ? 'Sending…' : 'Resend'}
        </button>
      ) : null}
    </div>
  );
}

function Rail() {
  const pathname = usePathname();
  const { org } = useMe();
  const pending = usePendingLinkRequests();
  const countersign = useReadyToCountersign();

  return (
    <aside
      className="shell-rail"
      style={{
        background: 'var(--paper)',
        borderRight: '1px solid var(--rule)',
        padding: 'var(--sp-5) 0',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-3)',
          padding: '0 var(--sp-4)',
          marginBottom: 'var(--sp-6)',
        }}
      >
        <span
          aria-hidden
          style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', background: 'var(--primary)', flexShrink: 0 }}
        />
        <div style={{ lineHeight: 1.15, minWidth: 0 }}>
          <div style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {org?.name ?? '—'}
          </div>
          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
            {org?.role === 'org_owner' ? 'Owner' : org?.role === 'org_manager' ? 'Manager' : ''}
          </div>
        </div>
      </div>

      <nav className="tabs" aria-label="Sections">
        {NAV.map((item) => {
          const current = pathname === item.href || (item.href !== '/' && pathname.startsWith(item.href));
          // A sub-link owns `aria-current` when the reader is on it, so the
          // section and its child never both claim to be the current page.
          const onSub = (item.sub ?? []).some((s) => pathname.startsWith(s.href));
          return (
            <div key={item.href} style={{ display: 'contents' }}>
              <Link href={item.href} className="tab" aria-current={current && !onSub ? 'page' : undefined}>
                <Icon icon={item.icon} width={20} />
                <span style={{ flex: 1 }}>{item.label}</span>
                {item.href === '/contracts' && countersign ? (
                  <span
                    aria-label={`${countersign} ready to countersign`}
                    style={{
                      minWidth: 20,
                      padding: '0 6px',
                      borderRadius: 999,
                      background: 'var(--primary)',
                      color: 'var(--on-primary)',
                      fontSize: 'var(--text-xs)',
                      fontWeight: 600,
                      textAlign: 'center',
                      lineHeight: '18px',
                    }}
                  >
                    {countersign}
                  </span>
                ) : null}
                {item.href === '/link-requests' && pending ? (
                  <span
                    aria-label={`${pendingLabel(pending)} pending`}
                    style={{
                      minWidth: 20,
                      padding: '0 6px',
                      borderRadius: 999,
                      background: 'var(--primary)',
                      color: 'var(--on-primary)',
                      fontSize: 'var(--text-xs)',
                      fontWeight: 600,
                      textAlign: 'center',
                      lineHeight: '18px',
                    }}
                  >
                    {pendingLabel(pending)}
                  </span>
                ) : null}
                {item.phase ? (
                  <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-faint)' }}>P{item.phase}</span>
                ) : null}
              </Link>
              {current && item.sub
                ? item.sub.map((s) => (
                    <Link
                      key={s.href}
                      href={s.href}
                      className="tab"
                      aria-current={pathname.startsWith(s.href) ? 'page' : undefined}
                      style={{ paddingLeft: 'calc(var(--sp-4) + 28px)', fontSize: 'var(--text-sm)', fontWeight: 400 }}
                    >
                      {s.label}
                    </Link>
                  ))
                : null}
            </div>
          );
        })}
      </nav>
    </aside>
  );
}

function TopBar() {
  const { user, logout } = useMe();
  return (
    <header
      className="shell-topbar"
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--sp-4)',
        padding: 'var(--sp-3) var(--sp-6)',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
        {user?.full_name}
        {user?.email ? ` · ${user.email}` : ''}
      </span>
      <button type="button" className="btn btn-quiet" onClick={() => void logout()} style={{ minHeight: 36 }}>
        <Icon icon="solar:logout-2-linear" width={18} /> Sign out
      </button>
    </header>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  // The org's colour and typeface are applied as soon as the chrome mounts, so
  // a landlord's own branding is on screen before the page body loads. Failure
  // is silent — the platform default is a working theme.
  useEffect(() => {
    const ac = new AbortController();
    void loadAndApplyOrgTheme(ac.signal);
    return () => ac.abort();
  }, []);

  return (
    <RequireAuth>
      <div className="shell-grid" style={{ display: 'grid', gridTemplateColumns: '220px 1fr', minHeight: '100vh' }}>
        <Rail />
        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
          <TopBar />
          <VerifyBanner />
          <div className="shell-body" style={{ padding: 'var(--sp-6)', flex: 1 }}>{children}</div>
        </div>
      </div>
    </RequireAuth>
  );
}

/** Page header used inside <Shell>. */
export function PageHead({ title, lead, actions }: { title: string; lead?: string; actions?: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'space-between',
        gap: 'var(--sp-4)',
        marginBottom: 'var(--sp-5)',
      }}
    >
      <div>
        <h1 style={{ fontSize: 'var(--text-2xl)' }}>{title}</h1>
        {lead ? <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)' }}>{lead}</p> : null}
      </div>
      {actions ? <div style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>{actions}</div> : null}
    </div>
  );
}
