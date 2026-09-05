'use client';

/**
 * Wizard step: branding. The same form as `/settings/branding` — one place to
 * fix a bug, and a landlord who edits it later finds exactly what they saw here.
 */

import { BrandingForm } from '../../../components/BrandingForm';

export function BrandingStep() {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>
        How your business appears to renters, on screen and on printed contracts. You can change any of it
        later under Settings → Branding.
      </p>
      <BrandingForm />
    </div>
  );
}
