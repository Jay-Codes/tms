'use client';

/**
 * Notification settings (FLOWS flow 8; API.md Phase 6).
 *
 * The scheduler decides when each message is due; this screen decides whether
 * it goes at all, at what hour, in which language, and in whose words. The form
 * itself is shared with the setup wizard so a landlord configures this once.
 *
 * Phase 27 adds "Buy credits" below it: bundles, the paying phone, the
 * waiting-for-approval state and the purchase history.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useT } from '@tms/ui';
import { BuyCredits } from '../../../../components/BuyCredits';
import { NotificationSettingsForm } from '../../../../components/NotificationSettingsForm';
import { PageHead } from '../../../../components/PageHead';

function NotificationSettingsBody() {
  const t = useT();
  return (
    <>
      <PageHead
        title={t('notifysettings.title')}
        lead={t('notifysettings.lead')}
        actions={
          <>
            <Link href="/notifications" className="btn btn-secondary">
              <Icon icon="solar:chat-round-line-linear" width={20} /> {t('nav.messages')}
            </Link>
            <Link href="/settings" className="btn btn-quiet">
              <Icon icon="solar:arrow-left-linear" width={20} /> {t('nav.settings')}
            </Link>
          </>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        <NotificationSettingsForm />
      </div>

      {/* Phase 27: the landlord buys credits here with mobile money (Snippe). */}
      <section id="buy-credits" style={{ paddingTop: 'var(--sp-7)' }}>
        <hr className="rule rule-strong" />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0 var(--sp-2)' }}>{t('buycredits.title')}</h2>
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginBottom: 'var(--sp-4)' }}>
          {t('buycredits.lead')}
        </p>
        <BuyCredits />
      </section>
    </>
  );
}

export default function NotificationSettingsPage() {
  return (
    <>
      <NotificationSettingsBody />
    </>
  );
}
