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
import { useT, type Translator } from '@tms/ui';
import { ApiError, authApi } from '../lib/api';
import { RequireAuth, useMe } from '../lib/auth';
import { loadAndApplyOrgTheme } from '../lib/branding';
import { LanguageToggle } from './LanguageToggle';
import { pendingLabel, useNavBadges, type Badges } from './NavBadges';

export { pendingLabel, usePendingLinkRequests } from './NavBadges';

export interface NavItem {
  href: string;
  /** Dictionary key, resolved at render — the nav is bilingual (Phase 13). */
  labelKey: string;
  icon: string;
  /** Phase in which the screen ships; undefined = functional now. */
  phase?: number;
  /** Indented links shown under this one when the section is open. */
  sub?: { href: string; labelKey: string }[];
}

export const NAV: NavItem[] = [
  { href: '/', labelKey: 'nav.dashboard', icon: 'solar:home-2-linear' },
  { href: '/properties', labelKey: 'nav.properties', icon: 'solar:buildings-2-linear' },
  { href: '/units', labelKey: 'nav.units', icon: 'solar:widget-4-linear' },
  { href: '/link-requests', labelKey: 'nav.link_requests', icon: 'solar:inbox-in-linear' },
  { href: '/renters', labelKey: 'nav.renters', icon: 'solar:users-group-rounded-linear' },
  {
    href: '/contracts',
    labelKey: 'nav.contracts',
    icon: 'solar:document-text-linear',
    sub: [{ href: '/contracts/templates', labelKey: 'nav.templates' }],
  },
  { href: '/payments', labelKey: 'nav.payments', icon: 'solar:wallet-money-linear' },
  { href: '/expenses', labelKey: 'nav.expenses', icon: 'solar:bill-list-linear' },
  { href: '/notifications', labelKey: 'nav.messages', icon: 'solar:chat-round-line-linear' },
  { href: '/reports', labelKey: 'nav.reports', icon: 'solar:chart-square-linear' },
  {
    href: '/settings',
    labelKey: 'nav.settings',
    icon: 'solar:settings-linear',
    sub: [
      { href: '/settings/preferences', labelKey: 'nav.preferences' },
      { href: '/settings/branding', labelKey: 'nav.branding' },
      { href: '/settings/bank-account', labelKey: 'nav.bank_account' },
      { href: '/settings/notifications', labelKey: 'nav.notifications' },
      { href: '/settings/expense-categories', labelKey: 'nav.expense_categories' },
    ],
  },
  { href: '/audit', labelKey: 'nav.audit', icon: 'solar:history-linear' },
];

/**
 * The four sections the bottom bar carries under 768px; "More" opens the
 * drawer. The labels use the `nav.short.*` keys so Swahili — which runs longer
 * than English — still fits five items across a 375px screen.
 */
const BOTTOM: Array<{ href: string; labelKey: string; icon: string }> = [
  { href: '/', labelKey: 'nav.short.dashboard', icon: 'solar:home-2-linear' },
  { href: '/properties', labelKey: 'nav.short.properties', icon: 'solar:buildings-2-linear' },
  { href: '/payments', labelKey: 'nav.short.payments', icon: 'solar:wallet-money-linear' },
  { href: '/contracts', labelKey: 'nav.short.contracts', icon: 'solar:document-text-linear' },
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
  const t = useT();
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
          ? t('shell.verify.sent')
          : t('shell.verify.unverified', { email: user.email ?? '' })}
      </span>
      {error ? <span style={{ color: 'var(--stamp-overdue)' }}>{error.detail}</span> : null}
      {state !== 'sent' ? (
        <button type="button" className="btn btn-quiet" onClick={resend} disabled={state === 'sending'} style={{ minHeight: 32 }}>
          {state === 'sending' ? t('shell.verify.sending') : t('shell.verify.resend')}
        </button>
      ) : null}
    </div>
  );
}

function OrgMark() {
  const { org } = useMe();
  const t = useT();
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minWidth: 0 }}>
      <span
        aria-hidden
        style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', background: 'var(--primary)', flexShrink: 0 }}
      />
      <div style={{ lineHeight: 1.15, minWidth: 0 }}>
        <div style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>{org?.name ?? '—'}</div>
        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
          {org?.role === 'org_owner'
            ? t('shell.role.owner')
            : org?.role === 'org_manager'
              ? t('shell.role.manager')
              : ''}
        </div>
      </div>
    </div>
  );
}

/** The nav list itself, shared by the desktop rail and the mobile drawer. */
function NavList({ badges, onNavigate }: { badges: Badges; onNavigate?: () => void }) {
  const pathname = usePathname();
  const t: Translator = useT();

  return (
    <nav className="tabs" aria-label={t('nav.sections')}>
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
              <span style={{ flex: 1 }}>{t(item.labelKey)}</span>
              {item.href === '/contracts' && badges.countersign ? (
                <Badge
                  count={badges.countersign}
                  label={t('shell.badge.countersign', { count: badges.countersign })}
                />
              ) : null}
              {item.href === '/payments' && badges.proofs ? (
                <Badge count={badges.proofs} label={t('shell.badge.proofs', { count: badges.proofs })} />
              ) : null}
              {item.href === '/link-requests' && badges.pending ? (
                <Badge
                  count={badges.pending}
                  label={t('shell.badge.pending', { count: pendingLabel(badges.pending) })}
                />
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
                    {t(s.labelKey)}
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
  const t = useT();
  return (
    <header className="shell-topbar">
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
        {user?.full_name}
        {user?.email ? ` · ${user.email}` : ''}
      </span>
      <button type="button" className="btn btn-quiet" onClick={() => void logout()} style={{ minHeight: 36 }}>
        <Icon icon="solar:logout-2-linear" width={18} /> {t('common.sign_out')}
      </button>
    </header>
  );
}

/** Mobile header: org identity plus the drawer trigger. Hidden from 768px up. */
function MobileBar({ onOpen }: { onOpen: () => void }) {
  const t = useT();
  return (
    <header className="shell-mobilebar">
      <OrgMark />
      <button
        type="button"
        className="btn btn-quiet"
        aria-label={t('shell.open_menu')}
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
  const t = useT();

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
      <div className="shell-drawer" role="dialog" aria-modal="true" aria-label={t('shell.menu')}>
        <div className="shell-drawer-head">
          <OrgMark />
          <button
            type="button"
            className="btn btn-quiet"
            aria-label={t('shell.close_menu')}
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
          {/* The drawer is the only chrome a phone shows, so the language
              switch lives here as well as in Settings → Preferences. */}
          <LanguageToggle />
          <button type="button" className="btn btn-quiet" onClick={() => void logout()} style={{ minHeight: 40 }}>
            <Icon icon="solar:logout-2-linear" width={18} /> {t('common.sign_out')}
          </button>
        </div>
      </div>
    </div>
  );
}

function BottomBar({ badges, onMore }: { badges: Badges; onMore: () => void }) {
  const pathname = usePathname();
  const t = useT();
  const onMain = BOTTOM.some((b) => isCurrent(pathname, b.href));

  return (
    <nav className="bottom-bar shell-bottom" aria-label={t('nav.main_sections')}>
      {BOTTOM.map((b) => (
        <Link key={b.href} href={b.href} className="tab" aria-current={isCurrent(pathname, b.href) ? 'page' : undefined}>
          <span style={{ position: 'relative', display: 'inline-flex' }}>
            <Icon icon={b.icon} width={22} />
            {b.href === '/contracts' && badges.countersign ? <span className="shell-dot" aria-hidden /> : null}
            {/* Proofs carry the number, not a dot: "three people say they have
                paid" is a different morning from "someone has". */}
            {b.href === '/payments' && badges.proofs ? (
              <span
                aria-label={t('shell.badge.proofs', { count: badges.proofs })}
                style={{
                  position: 'absolute',
                  top: -6,
                  right: -10,
                  minWidth: 18,
                  padding: '0 5px',
                  borderRadius: 999,
                  background: 'var(--primary)',
                  color: 'var(--on-primary)',
                  fontSize: 'var(--text-xs)',
                  fontWeight: 600,
                  lineHeight: '18px',
                  textAlign: 'center',
                }}
              >
                {pendingLabel(badges.proofs)}
              </span>
            ) : null}
          </span>
          {t(b.labelKey)}
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
        {t('common.more')}
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
