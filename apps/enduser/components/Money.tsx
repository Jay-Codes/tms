'use client';

/**
 * A ledger amount, in the design system's money style: `.amount` for the
 * tabular figures and `.amount .currency` for the small raised currency mark
 * (packages/ui/src/tokens.css). Use it for the numbers in a ledger column;
 * `money()` from lib/format stays the right tool inside a sentence.
 */

import type { CSSProperties } from 'react';
import { formatAmount } from '../lib/format';

export function Money({
  amount,
  currency = 'TZS',
  struck = false,
  style,
}: {
  amount: number;
  currency?: string;
  /** Reversed payments are struck through, not hidden. */
  struck?: boolean;
  style?: CSSProperties;
}) {
  return (
    <span
      className="amount"
      style={{ textDecoration: struck ? 'line-through' : undefined, ...style }}
    >
      <span className="currency">{currency}</span>
      {formatAmount(amount)}
    </span>
  );
}
