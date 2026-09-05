'use client';

import { useT } from '@tms/ui';
import { PageHead } from './PageHead';

/** Nav destination whose screen ships in a later phase. No mocked data. */
export function Placeholder({ title, phase, what }: { title: string; phase: number; what: string }) {
  const t = useT();
  return (
    <>
      <PageHead title={title} lead={t('common.coming_phase', { phase })} />
      <hr className="rule rule-strong" />
      <p style={{ marginTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>{what}</p>
    </>
  );
}
