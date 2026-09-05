'use client';

import { PageHead, Shell } from './Shell';

/** Nav destination whose screen ships in a later phase. No mocked data. */
export function Placeholder({ title, phase, what }: { title: string; phase: number; what: string }) {
  return (
    <Shell>
      <PageHead title={title} lead={`Coming in Phase ${phase}`} />
      <hr className="rule rule-strong" />
      <p style={{ marginTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>{what}</p>
    </Shell>
  );
}
