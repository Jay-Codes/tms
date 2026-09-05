'use client';

import { StepShell } from './StepShell';

export function BrandingStep() {
  return (
    <StepShell
      lead="How your business appears to renters, on screen and on printed contracts."
      collects={[
        'Display name, e.g. "JJnE Rentals"',
        'Logo upload (presigned MinIO URL)',
        'Optional letterhead image and document footer text',
        'Theme colour and typeface from the whitelist',
      ]}
    />
  );
}
