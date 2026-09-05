'use client';

/* ==================================================================
   PHASE 3 STUB — do not build on this yet.

   Flow 2 step 4 (FLOWS.md) collects the renter's KYC: full name,
   NIDA number, next of kin (name + phone), contact number
   (prefilled), email, optional ID photo upload (MinIO presigned PUT
   from the backend — never a direct bucket write).

   Endpoint arrives with SPEC §5.4 in Phase 3; API.md will carry the
   exact shape. Until then this renders a placeholder so the profile
   screen has the section in the right place.
   ================================================================== */

export function KycForm() {
  return (
    <section
      className="sheet"
      style={{ padding: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-2)' }}
      aria-labelledby="kyc-heading"
    >
      <h2 id="kyc-heading" style={{ fontSize: 'var(--text-lg)' }}>
        Your details
      </h2>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        NIDA number, next of kin and ID photo are collected when you connect to
        a unit. Nothing to do here yet.
      </p>
    </section>
  );
}
