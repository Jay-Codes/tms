'use client';

/**
 * Console chrome: platform header, left tab rail (desktop), drawer + bottom
 * bar (mobile), sign out.
 *
 * Mounted ONCE by `app/(portal)/layout.tsx`; pages must never render it (see
 * the no-restricted-imports rule in .eslintrc.json — PLAN2 #7).
 *
 * The admin app is deliberately NOT org-themed (SPEC §2.0): it never calls
 * applyOrgTheme, so the platform default palette and font are what an admin
 * always sees — a suspended org's branding can never dress up this console.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState, type ReactNode } from 'react';
import { RequireAuth, useMe } from '../lib/auth';

export interface NavItem {
  href: string;
  label: string;
  icon: string;
}

export const NAV: NavItem[] = [
  { href: '/', label: 'Dashboard', icon: 'solar:chart-square-linear' },
  { href: '/orgs', label: 'Organizations', icon: 'solar:buildings-2-linear' },
  { href: '/templates', label: 'Templates', icon: 'solar:chat-square-code-linear' },
  { href: '/audit', label: 'Audit', icon: 'solar:history-linear' },
  { href: '/jobs', label: 'Jobs', icon: 'solar:refresh-circle-linear' },
];

function isCurrent(pathname: string, href: string): boolean {
  return pathname === href || (href !== '/' && pathname.startsWith(href));
}

function Mark() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minWidth: 0 }}>
      <span
        aria-hidden
        style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', background: 'var(--ink)', flexShrink: 0 }}
      />
      <div style={{ lineHeight: 1.15, minWidth: 0 }}>
        <div style={{ fontWeight: 600 }}>TMS</div>
        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>Platform admin</div>
      </div>
    </div>
  );
}

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <nav className="tabs" aria-label="Sections">
      {NAV.map((item) => (
        <Link
          key={item.href}
          href={item.href}
          className="tab"
          aria-current={isCurrent(pathname, item.href) ? 'page' : undefined}
          onClick={onNavigate}
        >
          <Icon icon={item.icon} width={20} />
          <span style={{ flex: 1 }}>{item.label}</span>
        </Link>
      ))}
    </nav>
  );
}

function Rail() {
  return (
    <aside className="shell-rail">
      <div style={{ padding: '0 var(--sp-4)', marginBottom: 'var(--sp-6)' }}>
        <Mark />
      </div>
      <NavList />
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

function MobileBar({ onOpen }: { onOpen: () => void }) {
  return (
    <header className="shell-mobilebar">
      <Mark />
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

function Drawer({ open, onClose }: { open: boolean; onClose: () => void }) {
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
          <Mark />
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
          <NavList onNavigate={onClose} />
        </div>
        <div className="shell-drawer-foot">
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{user?.full_name}</span>
          <button type="button" className="btn btn-quiet" onClick={() => void logout()} style={{ minHeight: 40 }}>
            <Icon icon="solar:logout-2-linear" width={18} /> Sign out
          </button>
        </div>
      </div>
    </div>
  );
}

function BottomBar({ onMore }: { onMore: () => void }) {
  const pathname = usePathname();
  return (
    <nav className="bottom-bar shell-bottom" aria-label="Main sections">
      {/* Four fit across a 375 px phone beside "More"; the drawer has them all. */}
      {NAV.slice(0, 4).map((item) => (
        <Link
          key={item.href}
          href={item.href}
          className="tab"
          aria-current={isCurrent(pathname, item.href) ? 'page' : undefined}
        >
          <Icon icon={item.icon} width={22} />
          {item.label === 'Organizations' ? 'Orgs' : item.label}
        </Link>
      ))}
      <button
        type="button"
        className="tab"
        onClick={onMore}
        aria-haspopup="dialog"
        style={{ background: 'transparent', border: 0, borderTop: '3px solid transparent', font: 'inherit', fontSize: 'var(--text-xs)' }}
      >
        <Icon icon="solar:menu-dots-linear" width={22} />
        More
      </button>
    </nav>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const [drawer, setDrawer] = useState(false);

  useEffect(() => {
    if (process.env.NODE_ENV === 'development') {
      // eslint-disable-next-line no-console
      console.debug('shell mount');
    }
  }, []);

  return (
    <RequireAuth>
      <div className="shell-grid">
        <Rail />
        <div className="shell-main">
          <MobileBar onOpen={() => setDrawer(true)} />
          <TopBar />
          <div className="shell-body">{children}</div>
        </div>
        <BottomBar onMore={() => setDrawer(true)} />
        <Drawer open={drawer} onClose={() => setDrawer(false)} />
      </div>
    </RequireAuth>
  );
}

export { PageHead } from './PageHead';
