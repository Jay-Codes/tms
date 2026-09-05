'use client';

/**
 * Page title block. Lives outside Shell.tsx on purpose: the portal chrome is
 * mounted once by `app/(portal)/layout.tsx`, and a page that reaches for
 * `components/Shell` is a page that is about to remount the nav (PLAN2 #7).
 * The lint rule in .eslintrc.json makes that a build failure, so the header a
 * page *does* need has its own module.
 */

import type { ReactNode } from 'react';

export function PageHead({ title, lead, actions }: { title: string; lead?: string; actions?: ReactNode }) {
  return (
    <div
      className="page-head"
      style={{
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'space-between',
        gap: 'var(--sp-4)',
        marginBottom: 'var(--sp-5)',
      }}
    >
      <div style={{ minWidth: 0 }}>
        <h1 style={{ fontSize: 'var(--text-2xl)' }}>{title}</h1>
        {lead ? <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)' }}>{lead}</p> : null}
      </div>
      {actions ? (
        <div className="page-head-actions" style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>
          {actions}
        </div>
      ) : null}
    </div>
  );
}
