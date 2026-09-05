'use client';

/**
 * Console chrome: platform header, left tab rail, sign out.
 *
 * The admin app is deliberately NOT org-themed (SPEC §2.0): it never calls
 * applyOrgTheme, so the platform default palette and font are what an admin
 * always sees — a suspended org's branding can never dress up this console.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { ReactNode } from 'react';
import { RequireAuth, useMe } from '../lib/auth';

export interface NavItem {
  href: string;
  label: string;
  icon: string;
}

export const NAV: NavItem[] = [
  { href: '/', label: 'Dashboard', icon: 'solar:chart-square-linear' },
  { href: '/orgs', label: 'Organizations', icon: 'solar:buildings-2-linear' },
  { href: '/audit', label: 'Audit', icon: 'solar:history-linear' },
  { href: '/jobs', label: 'Jobs', icon: 'solar:refresh-circle-linear' },
];

function Rail() {
  const pathname = usePathname();

  return (
    <aside
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
          style={{
            width: 28,
            height: 28,
            borderRadius: 'var(--radius-sm)',
            background: 'var(--ink)',
            flexShrink: 0,
          }}
        />
        <div style={{ lineHeight: 1.15, minWidth: 0 }}>
          <div style={{ fontWeight: 600 }}>TMS</div>
          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>Platform admin</div>
        </div>
      </div>

      <nav className="tabs" aria-label="Sections">
        {NAV.map((item) => {
          const current =
            pathname === item.href || (item.href !== '/' && pathname.startsWith(item.href));
          return (
            <Link
              key={item.href}
              href={item.href}
              className="tab"
              aria-current={current ? 'page' : undefined}
            >
              <Icon icon={item.icon} width={20} />
              <span style={{ flex: 1 }}>{item.label}</span>
            </Link>
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
      <button
        type="button"
        className="btn btn-quiet"
        onClick={() => void logout()}
        style={{ minHeight: 36 }}
      >
        <Icon icon="solar:logout-2-linear" width={18} /> Sign out
      </button>
    </header>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  return (
    <RequireAuth>
      <div style={{ display: 'grid', gridTemplateColumns: '220px 1fr', minHeight: '100vh' }}>
        <Rail />
        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
          <TopBar />
          <div style={{ padding: 'var(--sp-6)', flex: 1 }}>{children}</div>
        </div>
      </div>
    </RequireAuth>
  );
}

/** Page header used inside <Shell>. */
export function PageHead({
  title,
  lead,
  actions,
}: {
  title: string;
  lead?: string;
  actions?: ReactNode;
}) {
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
      {actions ? (
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>{actions}</div>
      ) : null}
    </div>
  );
}
