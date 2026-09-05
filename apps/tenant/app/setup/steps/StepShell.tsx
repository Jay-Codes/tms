'use client';

import type { ReactNode } from 'react';

/**
 * Frame every wizard step body shares. `collects` lists what the finished step
 * will ask for — it is the brief for whoever fills this step in later.
 */
export function StepShell({
  lead,
  collects,
  children,
}: {
  lead: string;
  collects: string[];
  children?: ReactNode;
}) {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 'var(--measure)' }}>
      <p>{lead}</p>
      <div>
        <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', fontWeight: 500 }}>
          This step will collect
        </h3>
        <ul style={{ margin: 'var(--sp-2) 0 0', paddingLeft: '1.2em', color: 'var(--ink-soft)' }}>
          {collects.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      </div>
      {children}
    </div>
  );
}
