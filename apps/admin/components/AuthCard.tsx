'use client';

import type { ReactNode } from 'react';

/** Centred single sheet used by /login. */
export function AuthCard({
  title,
  lead,
  children,
  footer,
}: {
  title: string;
  lead?: string;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <main
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 'var(--sp-6) var(--sp-4)',
      }}
    >
      <div style={{ width: '100%', maxWidth: 440 }}>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>TMS — platform admin</p>
        <div className="sheet" style={{ padding: 'var(--sp-6)', marginTop: 'var(--sp-3)' }}>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>{title}</h1>
          {lead ? <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)' }}>{lead}</p> : null}
          <div style={{ marginTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)' }}>{children}</div>
        </div>
        {footer ? (
          <div style={{ marginTop: 'var(--sp-4)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {footer}
          </div>
        ) : null}
      </div>
    </main>
  );
}
