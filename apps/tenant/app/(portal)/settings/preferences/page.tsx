'use client';

/**
 * Settings → Preferences (Phase 13).
 *
 * Personal, not organisational: the language here belongs to the signed-in
 * landlord or manager, not to the business. Changing it repaints the portal
 * immediately and, once `PATCH /org/members/me` is live, follows the account
 * to their next device — and decides which language their own SMS arrive in.
 * The org-wide fallback for renters who have never chosen lives in
 * Settings → Notifications.
 */

import Link from 'next/link';
import { LOCALE_LABELS, useT } from '@tms/ui';
import { Field } from '../../../../components/FormBits';
import { LanguageToggle } from '../../../../components/LanguageToggle';
import { PageHead } from '../../../../components/PageHead';
import { useMe } from '../../../../lib/auth';
import { useLocaleState } from '../../../../lib/locale';

export default function PreferencesPage() {
  const t = useT();
  const { locale, saving } = useLocaleState();
  const { user } = useMe();

  return (
    <>
      <PageHead title={t('prefs.title')} lead={t('prefs.lead')} />
      <hr className="rule rule-strong" />

      <div className="sheet" style={{ padding: 'var(--sp-5)', marginTop: 'var(--sp-5)', maxWidth: 560 }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('prefs.language.title')}</h2>
        <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)' }}>{t('prefs.language.lead')}</p>

        <div style={{ marginTop: 'var(--sp-4)', maxWidth: 280 }}>
          <Field
            id="locale"
            label={t('common.language')}
            hint={saving ? t('common.saving') : t('prefs.language.hint')}
          >
            <LanguageToggle compact={false} />
          </Field>
        </div>

        <p style={{ marginTop: 'var(--sp-4)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {t('prefs.language.current', { language: LOCALE_LABELS[locale] })}
          {user ? ` · ${user.full_name}` : ''}
        </p>
      </div>

      <p style={{ marginTop: 'var(--sp-5)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', maxWidth: 560 }}>
        {t('prefs.renters_note')}{' '}
        <Link href="/settings/notifications">{t('prefs.renters_link')}</Link>
      </p>
    </>
  );
}
