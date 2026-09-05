'use client';

/**
 * A raised sheet (dialog). The one place the design system allows a shadow:
 * "genuinely separate sheets" (SPEC §2.0). Escape and the backdrop close it.
 */

import { useEffect, useRef, type ReactNode } from 'react';

export function Sheet({
  open,
  title,
  onClose,
  children,
  width = 560,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  width?: number;
}) {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    const first = ref.current?.querySelector<HTMLElement>('input, select, textarea, button');
    first?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgb(28 43 90 / 0.35)',
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'center',
        padding: 'var(--sp-6)',
        overflowY: 'auto',
        zIndex: 40,
      }}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="sheet sheet-raised"
        style={{ width: '100%', maxWidth: width, padding: 'var(--sp-5)', marginTop: 'var(--sp-6)' }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--sp-4)',
            marginBottom: 'var(--sp-4)',
          }}
        >
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{title}</h2>
          <button type="button" className="btn btn-quiet" onClick={onClose} style={{ minHeight: 32 }}>
            Close
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
