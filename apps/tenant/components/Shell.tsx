'use client';

/**
 * Authenticated portal chrome: org header, left tab rail (desktop), drawer +
 * bottom bar (mobile), verification banner.
 *
 * Mounted ONCE by `app/(portal)/layout.tsx`. Pages must never render it
 * themselves — that is what made the nav reload on every route change
 * (PLAN2 #7), and `.eslintrc.json` fails the build if a page imports it.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState, type ReactNode } from 'react';
import { ApiError, authApi } from '../lib/api';
import { RequireAuth, useMe } from '../lib/auth';
import { loadAndApplyOrgTheme } from '../lib/branding';
import { pendingLabel, useNavBadges, type Badges } from './NavBadges';

export { pendingLabel, usePendingLinkRequests } from './NavBadges';

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
  { href: '/payments', label: 'Payments', icon: 'solar:wallet-money-linear' },
  { href: '/expenses', label: 'Expenses', icon: 'solar:bill-list-linear' },
  { href: '/notifications', label: 'Messages', icon: 'solar:chat-round-line-linear' },
  { href: '/reports', label: 'Reports', icon: 'solar:chart-square-linear' },
  {
    href: '/settings',
    label: 'Settings',
    icon: 'solar:settings-linear',
    sub: [
      { href: '/settings/branding', label: 'Branding' },
      { href: '/settings/bank-account', label: 'Bank account' },
      { href: '/settings/notifications', label: 'Notifications' },
      { href: '/settings/expense-categories', label: 'Expense categories' },
    ],
  },
  { href: '/audit', label: 'Audit', icon: 'solar:history-linear' },
];

/** The five sections the bottom bar carries under 768px; "More" opens the drawer. */
const BOTTOM: Array<{ href: string; label: string; icon: string }> = [
  { href: '/', label: 'Dashboard', icon: 'solar:home-2-linear' },
  { href: '/properties', label: 'Properties', icon: 'solar:buildings-2-linear' },
  { href: '/payments', label: 'Payments', icon: 'solar:wallet-money-linear' },
  { href: '/contracts', label: 'Contracts', icon: 'solar:document-text-linear' },
];

function isCurrent(pathname: string, href: string): boolean {
  return pathname === href || (href !== '/' && pathname.startsWith(href));
}

function Badge({ count, label }: { count: number; label: string }) {
  return (
    <span
      aria-label={label}
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
      {count}
    </span>
  );
}

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

function OrgMark() {
  const { org } = useMe();
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minWidth: 0 }}>
      <span
        aria-hidden
        style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', background: 'var(--primary)', flexShrink: 0 }}
      />
      <div style={{ lineHeight: 1.15, minWidth: 0 }}>
        <div style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>{org?.name ?? '—'}</div>
        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
          {org?.role === 'org_owner' ? 'Owner' : org?.role === 'org_manager' ? 'Manager' : ''}
        </div>
      </div>
    </div>
  );
}

/** The nav list itself, shared by the desktop rail and the mobile drawer. */
function NavList({ badges, onNavigate }: { badges: Badges; onNavigate?: () => void }) {
  const pathname = usePathname();

  return (
    <nav className="tabs" aria-label="Sections">
      {NAV.map((item) => {
        const current = isCurrent(pathname, item.href);
        // A sub-link owns `aria-current` when the reader is on it, so the
        // section and its child never both claim to be the current page.
        const onSub = (item.sub ?? []).some((s) => pathname.startsWith(s.href));
        return (
          <div key={item.href} style={{ display: 'contents' }}>
            <Link
              href={item.href}
              className="tab"
              aria-current={current && !onSub ? 'page' : undefined}
              onClick={onNavigate}
            >
              <Icon icon={item.icon} width={20} />
              <span style={{ flex: 1 }}>{item.label}</span>
              {item.href === '/contracts' && badges.countersign ? (
                <Badge count={badges.countersign} label={`${badges.countersign} ready to countersign`} />
              ) : null}
              {item.href === '/link-requests' && badges.pending ? (
                <Badge count={badges.pending} label={`${pendingLabel(badges.pending)} pending`} />
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
                    onClick={onNavigate}
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
  );
}

function Rail({ badges }: { badges: Badges }) {
  return (
    <aside className="shell-rail">
      <div style={{ padding: '0 var(--sp-4)', marginBottom: 'var(--sp-6)' }}>
        <OrgMark />
      </div>
      <NavList badges={badges} />
    </aside>
  );
}

function TopBar() {
  const { user, logout } = useMe();
  return (
    <header className="shell-topbar">
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

/** Mobile header: org identity plus the drawer trigger. Hidden from 768px up. */
function MobileBar({ onOpen }: { onOpen: () => void }) {
  return (
    <header className="shell-mobilebar">
      <OrgMark />
      <button
        type="button"
        className="btn btn-quiet"
        aria-label="Open menu"
        aria-haspopup="dialog"
        onClick={onOpen}
        style={{ minHeight: 'var(--touch-min)', minWidth: 'var(--touch-min)', justifyContent: 'center' }}
      >
        <Icon icon="solar:hamburger-menu-linear" width={24} />
      </button>
    </header>
  );
}

function Drawer({ open, onClose, badges }: { open: boolean; onClose: () => void; badges: Badges }) {
  const { user, logout } = useMe();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="shell-drawer-backdrop"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="shell-drawer" role="dialog" aria-modal="true" aria-label="Menu">
        <div className="shell-drawer-head">
          <OrgMark />
          <button
            type="button"
            className="btn btn-quiet"
            aria-label="Close menu"
            onClick={onClose}
            style={{ minHeight: 'var(--touch-min)', minWidth: 'var(--touch-min)', justifyContent: 'center' }}
          >
            <Icon icon="solar:close-circle-linear" width={24} />
          </button>
        </div>
        <div className="shell-drawer-body">
          <NavList badges={badges} onNavigate={onClose} />
        </div>
        <div className="shell-drawer-foot">
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {user?.full_name}
          </span>
          <button type="button" className="btn btn-quiet" onClick={() => void logout()} style={{ minHeight: 40 }}>
            <Icon icon="solar:logout-2-linear" width={18} /> Sign out
          </button>
        </div>
      </div>
    </div>
  );
}

function BottomBar({ badges, onMore }: { badges: Badges; onMore: () => void }) {
  const pathname = usePathname();
  const onMain = BOTTOM.some((b) => isCurrent(pathname, b.href));

  return (
    <nav className="bottom-bar shell-bottom" aria-label="Main sections">
      {BOTTOM.map((b) => (
        <Link key={b.href} href={b.href} className="tab" aria-current={isCurrent(pathname, b.href) ? 'page' : undefined}>
          <span style={{ position: 'relative', display: 'inline-flex' }}>
            <Icon icon={b.icon} width={22} />
            {b.href === '/contracts' && badges.countersign ? <span className="shell-dot" aria-hidden /> : null}
          </span>
          {b.label}
        </Link>
      ))}
      <button
        type="button"
        className="tab"
        onClick={onMore}
        aria-haspopup="dialog"
        aria-current={onMain ? undefined : 'page'}
        style={{ background: 'transparent', border: 0, borderTop: '3px solid transparent', font: 'inherit', fontSize: 'var(--text-xs)' }}
      >
        <span style={{ position: 'relative', display: 'inline-flex' }}>
          <Icon icon="solar:menu-dots-linear" width={22} />
          {badges.pending ? <span className="shell-dot" aria-hidden /> : null}
        </span>
        More
      </button>
    </nav>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const [drawer, setDrawer] = useState(false);
  const badges = useNavBadges();

  // The org's colour and typeface are applied as soon as the chrome mounts, so
  // a landlord's own branding is on screen before the page body loads. Failure
  // is silent — the platform default is a working theme. This runs once for
  // the whole session: the chrome lives in the (portal) layout.
  useEffect(() => {
    if (process.env.NODE_ENV === 'development') {
      // Fires once per session. A second line means a page is rendering <Shell>
      // itself again and the nav is remounting (PLAN2 #7).
      // eslint-disable-next-line no-console
      console.debug('shell mount');
    }
    const ac = new AbortController();
    void loadAndApplyOrgTheme(ac.signal);
    return () => ac.abort();
  }, []);

  return (
    <RequireAuth>
      <div className="shell-grid">
        <Rail badges={badges} />
        <div className="shell-main">
          <MobileBar onOpen={() => setDrawer(true)} />
          <TopBar />
          <VerifyBanner />
          <div className="shell-body">{children}</div>
        </div>
        <BottomBar badges={badges} onMore={() => setDrawer(true)} />
        <Drawer open={drawer} onClose={() => setDrawer(false)} badges={badges} />
      </div>
    </RequireAuth>
  );
}

export { PageHead } from './PageHead';
