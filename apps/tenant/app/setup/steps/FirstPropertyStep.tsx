'use client';

import { StepShell } from './StepShell';

export function FirstPropertyStep() {
  return (
    <StepShell
      lead="One property to start with. You can add more from the Properties page later."
      collects={['Property name, e.g. "Mbezi Beach Block A"', 'Location / address']}
    />
  );
}
