'use client';

/** Expense categories, on their own page so Settings stays short. */

import Link from 'next/link';
import { ExpenseCategoriesManager } from '../../../../components/ExpenseCategoriesManager';
import { PageHead } from '../../../../components/PageHead';

export default function ExpenseCategoriesPage() {
  return (
    <>
      <PageHead
        title="Expense categories"
        lead="What your spending is filed under. Seeded with the usual eight; rename, reorder or add your own."
        actions={
          <Link href="/settings" className="btn btn-quiet">
            Back to settings
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
