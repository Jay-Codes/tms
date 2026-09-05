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
import { NotificationSettingsForm } from '../../../../components/NotificationSettingsForm';
import { PageHead } from '../../../../components/PageHead';

function NotificationSettingsBody() {
  return (
    <>
      <PageHead
        title="Notifications"
        lead="Rent reminders go out automatically. This is where you decide which ones, when, and how they read."
        actions={
          <>
            <Link href="/notifications" className="btn btn-secondary">
              <Icon icon="solar:chat-round-line-linear" width={20} /> Messages
            </Link>
            <Link href="/settings" className="btn btn-quiet">
              <Icon icon="solar:arrow-left-linear" width={20} /> Settings
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
