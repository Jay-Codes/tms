'use client';

/**
 * Branding (SPEC §5.2, API.md Branding). One colour, one typeface, a logo, a
 * letterhead and a footer line — the whole of what a landlord may change about
 * how the platform looks. Saving re-themes the portal immediately, without a
 * reload, because <BrandingForm> applies the theme as part of adopting the
 * saved record.
 */

import Link from 'next/link';
import { BrandingForm } from '../../../../components/BrandingForm';
import { PageHead } from '../../../../components/PageHead';

function BrandingBody() {
  return (
    <>
      <PageHead
        title="Branding"
        lead="How your business looks to renters — on the scan page, in the portal and on every printed contract."
        actions={
          <Link href="/settings" className="btn btn-quiet">
            Settings
          </Link>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        <BrandingForm />
      </div>
    </>
  );
}

export default function BrandingPage() {
  return (
    <>
      <BrandingBody />
    </>
  );
}
