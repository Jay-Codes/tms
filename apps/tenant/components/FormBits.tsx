'use client';

/** Small shared form/feedback pieces built only from design-system classes. */

import type { ReactNode } from 'react';
import { ApiError } from '../lib/api';

/** Top-of-form error line for a failed request (problem+json detail). */
export function ProblemNote({ error }: { error: ApiError | null }) {
  if (!error) return null;
  return (
    <p
      role="alert"
      style={{
        color: 'var(--stamp-overdue)',
        fontSize: 'var(--text-sm)',
        border: '1px solid var(--stamp-overdue)',
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-2) var(--sp-3)',
        maxWidth: 'none',
      }}
    >
      {error.detail}
    </p>
  );
}

export function Note({ children }: { children: ReactNode }) {
  return (
    <p
      role="status"
      style={{
        color: 'var(--stamp-paid)',
        fontSize: 'var(--text-sm)',
        border: '1px solid var(--stamp-paid)',
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-2) var(--sp-3)',
        maxWidth: 'none',
      }}
    >
      {children}
    </p>
  );
}

export interface FieldProps {
  id: string;
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}

/** Label above, hint below, error replaces the hint. */
export function Field({ id, label, hint, error, children }: FieldProps) {
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={id}>{label}</label>
      {children}
      {error ? <span className="error">{error}</span> : hint ? <span className="hint">{hint}</span> : null}
    </div>
  );
}
