'use client';

import { StepShell } from './StepShell';

export function UnitsStep() {
  return (
    <StepShell
      lead="The rooms, houses or shops inside that property. Each one gets its own permanent QR code."
      collects={[
        'Custom unit names, e.g. "Room 1", "House B"',
        'Price as an amount per N days (default 30)',
        'Optionally, which payment periods this unit offers',
      ]}
    />
  );
}
