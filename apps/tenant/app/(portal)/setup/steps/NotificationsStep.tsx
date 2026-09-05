'use client';

/**
 * Wizard step 6 (FLOWS flow 1 step 3.6): the same notification settings form
 * the Settings screen shows, compact. A landlord who finishes setup has already
 * decided when renters hear from them, so nothing else has to chase this later.
 */

import { NotificationSettingsForm } from '../../../components/NotificationSettingsForm';

export function NotificationsStep() {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>
        Rent reminders go out on their own. Choose which ones, at what hour, and in which language — you can change
        any of it later under Settings → Notifications.
      </p>
      <NotificationSettingsForm compact saveLabel="Save notification settings" />
    </div>
  );
}
