'use client';

/**
 * Horizontal scroll box for a wide ledger. Wrap a table in it and the table
 * scrolls inside its own bounds instead of pushing the whole page sideways on
 * a phone; the edge fade (see `.table-scroll` in tokens.css) shows only on the
 * side that still has content off-screen.
 *
 * `tabIndex={0}` is deliberate: a scroll box that a mouse can pan must be
 * reachable by keyboard too.
 */

import type { ReactNode } from 'react';

export function TableScroll({
  children,
  label = 'Table',
  className,
  style,
}: {
  children: ReactNode;
  /** Accessible name — say what the table holds, e.g. "Payments". */
  label?: string;
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <div
      className={className ? `table-scroll ${className}` : 'table-scroll'}
      role="region"
      aria-label={label}
      tabIndex={0}
      style={style}
    >
      {children}
    </div>
  );
}
