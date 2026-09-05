'use client';

/** Wizard step 3 (FLOWS flow 1 step 3.3) — the same editor as Settings. */

import { useT } from '@tms/ui';
import { PeriodsManager } from '../../../../components/PeriodsManager';

export function PaymentPeriodsStep() {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>{t('setup.periods.lead')}</p>
      <PeriodsManager />
    </div>
  );
}
