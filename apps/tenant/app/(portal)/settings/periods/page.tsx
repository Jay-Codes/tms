'use client';

/** Payment periods, on their own page so Settings stays short. */

import Link from 'next/link';
import { useT } from '@tms/ui';
import { PeriodsManager } from '../../../../components/PeriodsManager';
import { PageHead } from '../../../../components/PageHead';

export default function PeriodsPage() {
  const t = useT();
  return (
    <>
      <PageHead
        title={t('periods.title')}
        lead={t('periods.lead')}
        actions={
          <Link href="/settings" className="btn btn-quiet">
            {t('settings.back')}
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
