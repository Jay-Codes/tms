'use client';

/**
 * Notification settings (FLOWS flow 8; API.md Phase 6).
 *
 * The scheduler decides when each message is due; this screen decides whether
 * it goes at all, at what hour, in which language, and in whose words. The form
 * itself is shared with the setup wizard so a landlord configures this once.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useT } from '@tms/ui';
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
