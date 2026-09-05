'use client';

import { StepShell } from './StepShell';

export function ContractTemplateStep() {
  return (
    <StepShell
      lead="The terms every new contract starts from. Editing the template never changes contracts that are already active."
      collects={[
        'Terms text, edited from the default (variables like {{renter_name}}, {{rent}})',
        'Default due day of the month',
        'Grace days before a payment counts as overdue',
      ]}
    />
  );
}
