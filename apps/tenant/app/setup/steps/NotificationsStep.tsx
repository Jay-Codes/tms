'use client';

import { StepShell } from './StepShell';

export function NotificationsStep() {
  return (
    <StepShell
      lead="When renters hear from you, and in which language."
      collects={[
        'Reminder timings (default: one week before, and on the due date)',
        'SMS language, Swahili or English',
        'SMS sender name',
      ]}
    />
  );
}
