'use client';

/** Expense categories, on their own page so Settings stays short. */

import Link from 'next/link';
import { useT } from '@tms/ui';
import { ExpenseCategoriesManager } from '../../../../components/ExpenseCategoriesManager';
import { PageHead } from '../../../../components/PageHead';

export default function ExpenseCategoriesPage() {
  const t = useT();
  return (
    <>
      <PageHead
        title={t('expcat.title')}
        lead={t('expcat.lead')}
        actions={
          <Link href="/settings" className="btn btn-quiet">
            {t('settings.back')}
          </Link>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        <ExpenseCategoriesManager />
      </div>
    </>
  );
}
