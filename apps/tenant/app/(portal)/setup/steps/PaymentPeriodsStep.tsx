'use client';

/** Wizard step 3 (FLOWS flow 1 step 3.3) — the same editor as Settings. */

import { PeriodsManager } from '../../../../components/PeriodsManager';

export function PaymentPeriodsStep() {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>
        Your business starts with Monthly (30d), Quarterly (90d), Half-year (180d) and Yearly (365d).
        Keep them, deactivate the ones you never use, and add your own — any number of days.
      </p>
      <PeriodsManager />
    </div>
  );
}
