'use client';

import { StepShell } from './StepShell';

/** Not built in Phase 1 — the periods API arrives with properties and units. */
export function PaymentPeriodsStep() {
  return (
    <StepShell
      lead="Your org is seeded with Monthly (30d), Quarterly (90d), Half-year (180d) and Yearly (365d). Keep, remove or add your own."
      collects={[
        'Which recommended presets to keep',
        'Custom periods as a label plus a number of days, e.g. "Weekly" 7d, "45 days"',
        'Ordering of the list renters see',
      ]}
    />
  );
}
