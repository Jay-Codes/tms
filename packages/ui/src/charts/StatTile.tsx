'use client';

/**
 * The tile contract from the dataviz skill, in ledger clothes: a label, the
 * figure, an optional signed change against a *named* period, and an optional
 * sparkline.
 *
 * The colour rules, which are the part that goes wrong most often:
 *  - the direction is coloured by **direction × whether up is good**, so a
 *    rise in expenses is not painted green just because it is a rise;
 *  - `goodDirection="none"` (spending, which is neither) stays soft ink;
 *  - a null change means the previous window held nothing — a percentage
 *    against zero is not a fact, so it is muted and says so, never `0%`;
 *  - the arrow carries the direction as well as the colour, so nothing is
 *    stated by hue alone.
 *
 * The figure keeps proportional figures (tabular-nums makes a large standalone
 * number look loose); columns of numbers in the tables use tabular instead.
 */

import type { ReactNode } from 'react';
import { changeLabel } from './format';

export type GoodDirection = 'up' | 'down' | 'none';

export interface StatTileProps {
  label: string;
  value: ReactNode;
  /** Percent change vs the previous window; null when there is no comparison. */
  change?: number | null;
  /** What the change is against — "vs previous month". */
  changeLabelText?: string;
  goodDirection?: GoodDirection;
  sub?: ReactNode;
  /** Drawn to the right of the figure. */
  trend?: ReactNode;
  tone?: 'paid' | 'overdue';
  /** Localised text shown when there is no previous-period comparison. */
  noComparisonLabel?: string;
}

function Arrow({ dir }: { dir: 'up' | 'down' | 'flat' }) {
  const d = dir === 'up' ? 'M4 11 L8 5 L12 11' : dir === 'down' ? 'M4 5 L8 11 L12 5' : 'M4 8 L12 8';
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden focusable="false" style={{ flex: 'none' }}>
      <path d={d} fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function ChangeMark({
  change,
  goodDirection = 'up',
  suffix,
  noComparisonLabel,
}: {
  change: number | null | undefined;
  goodDirection?: GoodDirection;
  suffix?: string;
  noComparisonLabel?: string;
}) {
  if (change === null || change === undefined || !Number.isFinite(change)) {
    return (
      <span className="pencil" title={noComparisonLabel ?? "There was nothing in the previous period to compare against."}>
        {noComparisonLabel ?? 'no comparison'}
      </span>
    );
  }
  const r = Math.round(change);
  const dir = r > 0 ? 'up' : r < 0 ? 'down' : 'flat';
  const good = goodDirection === 'none' || r === 0 ? null : (goodDirection === 'up') === (r > 0);
  const color = good === null ? 'var(--ink-soft)' : good ? 'var(--stamp-paid)' : 'var(--stamp-overdue)';
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--sp-1)',
        color,
        fontSize: 'var(--text-sm)',
        fontWeight: 600,
        whiteSpace: 'nowrap',
      }}
    >
      <Arrow dir={dir} />
      {changeLabel(r)}
      {suffix ? <span style={{ fontWeight: 400, color: 'var(--ink-soft)' }}>{suffix}</span> : null}
    </span>
  );
}

export function StatTile({
  label,
  value,
  change,
  changeLabelText,
  goodDirection = 'up',
  sub,
  trend,
  tone,
  noComparisonLabel,
}: StatTileProps) {
  const color = tone === 'overdue' ? 'var(--stamp-overdue)' : tone === 'paid' ? 'var(--stamp-paid)' : 'var(--ink)';
  return (
    <div
      style={{
        borderTop: '1px solid var(--rule-strong)',
        paddingTop: 'var(--sp-3)',
        display: 'grid',
        gap: 'var(--sp-1)',
        alignContent: 'start',
        minWidth: 0,
      }}
    >
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</span>
      {/* The figure never wraps (it is one amount), so where the tile is
          narrower than figure + sparkline the sparkline drops to its own line
          rather than the pair spilling out of the tile. */}
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          justifyContent: 'space-between',
          gap: 'var(--sp-3)',
          flexWrap: 'wrap',
          minWidth: 0,
        }}
      >
        <strong style={{ fontSize: 'var(--text-xl)', color, lineHeight: 1.1 }}>{value}</strong>
        {trend}
      </div>
      {change !== undefined ? (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <ChangeMark change={change} goodDirection={goodDirection} noComparisonLabel={noComparisonLabel} />
          {changeLabelText ? (
            <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{changeLabelText}</span>
          ) : null}
        </span>
      ) : null}
      {sub ? <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{sub}</span> : null}
    </div>
  );
}

/**
 * A horizontal bar sized against the biggest row in its table — the breakdown
 * form, where the category names are the y-axis and the table carries the
 * numbers. Negatives grow left of the centre line.
 */
export function RowBar({
  value,
  max,
  color = 'var(--chart-1)',
  negativeColor = 'var(--stamp-overdue)',
  width = 120,
  height = 10,
  ariaLabel,
}: {
  value: number;
  max: number;
  color?: string;
  negativeColor?: string;
  width?: number;
  height?: number;
  ariaLabel: string;
}) {
  const m = Math.max(1, Math.abs(max));
  const frac = Math.min(1, Math.abs(value) / m);
  const neg = value < 0;
  const half = width / 2;
  // A table with no negative row uses the full width from the left edge; one
  // with negatives is centred, so the sign is visible as a direction.
  const centred = max < 0 || neg;
  const w = Math.max(value === 0 ? 0 : 2, frac * (centred ? half : width));
  const x = centred ? (neg ? half - w : half) : 0;
  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      role="img"
      aria-label={ariaLabel}
      style={{ display: 'block' }}
    >
      <rect x={x} y={0} width={w} height={height} rx={3} fill={neg ? negativeColor : color} />
    </svg>
  );
}
