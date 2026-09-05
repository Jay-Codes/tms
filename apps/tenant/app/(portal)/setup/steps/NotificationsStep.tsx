'use client';

/**
 * Wizard step 6 (FLOWS flow 1 step 3.6): the same notification settings form
 * the Settings screen shows, compact. A landlord who finishes setup has already
 * decided when renters hear from them, so nothing else has to chase this later.
 */

import { useT } from '@tms/ui';
import { NotificationSettingsForm } from '../../../../components/NotificationSettingsForm';

export function NotificationsStep() {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>{t('setup.notifications.lead')}</p>
      <NotificationSettingsForm compact saveLabel={t('notifysettings.save')} />
    </div>
  );
}
