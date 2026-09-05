'use client';

/** Payment periods, on their own page so Settings stays short. */

import Link from 'next/link';
import { PeriodsManager } from '../../../../components/PeriodsManager';
import { PageHead } from '../../../../components/PageHead';

export default function PeriodsPage() {
  return (
    <>
      <PageHead
        title="Payment periods"
        lead="The cadences a renter can choose when a contract is written. Any number of days, as many as you like."
        actions={
          <Link href="/settings" className="btn btn-quiet">
            Back to settings
          </Link>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        <PeriodsManager />
      </div>
    </>
  );
}
