'use client';

import type { CSSProperties, ReactNode } from 'react';

/**
 * Page frame for the renter PWA: a single phone-width column of paper.
 * `bottomBar` reserves room for the fixed nav on the signed-in screens.
 */
export function Screen({
  children,
  bottomBar = false,
  style,
}: {
  children: ReactNode;
  bottomBar?: boolean;
  style?: CSSProperties;
}) {
  return (
    <main
      style={{
        minHeight: '100dvh',
        maxWidth: 440,
        margin: '0 auto',
        padding: `var(--sp-6) var(--sp-4) ${bottomBar ? '96px' : 'var(--sp-8)'}`,
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--sp-5)',
        ...style,
      }}
    >
      {children}
    </main>
  );
}

export function ScreenHeader({
  eyebrow,
  title,
  lead,
}: {
  eyebrow?: string;
  title: string;
  lead?: string;
}) {
  return (
    <header style={{ display: 'grid', gap: 'var(--sp-2)' }}>
      {eyebrow && (
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{eyebrow}</p>
      )}
      <h1 style={{ fontSize: 'var(--text-xl)' }}>{title}</h1>
      {lead && <p style={{ color: 'var(--ink-soft)' }}>{lead}</p>}
    </header>
  );
}

/** A quiet ruled note; `tone="error"` marks it in overdue ink. */
export function Notice({
  tone = 'info',
  children,
}: {
  tone?: 'info' | 'error';
  children: ReactNode;
}) {
  const color = tone === 'error' ? 'var(--stamp-overdue)' : 'var(--ink-soft)';
  return (
    <p
      role={tone === 'error' ? 'alert' : 'status'}
      style={{
        margin: 0,
        padding: 'var(--sp-3)',
        borderLeft: `3px solid ${color}`,
        background: 'var(--sheet-tint)',
        borderRadius: 'var(--radius-sm)',
        color,
        fontSize: 'var(--text-sm)',
      }}
    >
      {children}
    </p>
  );
}
