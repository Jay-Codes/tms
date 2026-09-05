'use client';

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useT } from '@tms/ui';

/**
 * Renter tab bar. Phase 1 ships Home and Profile; Contract and Payments are
 * routed but land on a "coming soon" sheet until Phases 3–5 fill them.
 */
const TABS: Array<{ href: string; key: string; icon: string }> = [
  { href: '/', key: 'nav.home', icon: 'solar:home-2-linear' },
  { href: '/contract', key: 'nav.contract', icon: 'solar:document-text-linear' },
  { href: '/payments', key: 'nav.payments', icon: 'solar:wallet-money-linear' },
  { href: '/profile', key: 'nav.profile', icon: 'solar:user-circle-linear' },
];

export function BottomBar() {
  const pathname = usePathname();
  const t = useT();

  return (
    <nav
      className="bottom-bar"
      aria-label={t('nav.aria')}
      style={{
        position: 'fixed',
        insetInline: 0,
        bottom: 0,
        zIndex: 10,
        paddingBottom: 'env(safe-area-inset-bottom)',
      }}
    >
      <div
        className="bottom-bar"
        style={{ maxWidth: 440, margin: '0 auto', width: '100%', borderTop: 0 }}
      >
        {TABS.map((tab) => {
          const current = tab.href === '/' ? pathname === '/' : pathname.startsWith(tab.href);
          return (
            <Link
              key={tab.href}
              href={tab.href}
              className="tab"
              aria-current={current ? 'page' : undefined}
            >
              <Icon icon={tab.icon} width={22} aria-hidden />
              {t(tab.key)}
            </Link>
          );
        })}
      </div>
    </nav>
  );
}
