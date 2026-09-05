'use client';

/**
 * Wizard step: branding. The same form as `/settings/branding` — one place to
 * fix a bug, and a landlord who edits it later finds exactly what they saw here.
 */

import { useT } from '@tms/ui';
import { BrandingForm } from '../../../../components/BrandingForm';

export function BrandingStep() {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>{t('setup.branding.lead')}</p>
      <BrandingForm />
    </div>
  );
}
