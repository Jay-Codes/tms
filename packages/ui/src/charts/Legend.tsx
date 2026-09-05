'use client';

/**
 * A legend is present whenever a chart draws two or more series — identity is
 * never carried by colour alone (dataviz: accessibility pass). One series
 * needs none: the chart's own title already says what is plotted.
 *
 * The key mirrors the mark: a rectangle for bars and area fills, a stroke for
 * lines, the same stroke dashed for a reference series (what was *expected*,
 * as opposed to what happened).
 */

export type LegendShape = 'line' | 'dashed' | 'rect';

export interface LegendItem {
  label: string;
  color: string;
  shape?: LegendShape;
}

export function Legend({ items }: { items: LegendItem[] }) {
  if (items.length < 2) return null;
  return (
    <ul
      style={{
        listStyle: 'none',
        margin: 0,
        padding: 0,
        display: 'flex',
        flexWrap: 'wrap',
        gap: 'var(--sp-2) var(--sp-5)',
        fontSize: 'var(--text-sm)',
        color: 'var(--ink-soft)',
      }}
    >
      {items.map((it) => (
        <li key={it.label} style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          <LegendKey color={it.color} shape={it.shape ?? 'line'} />
          {it.label}
        </li>
      ))}
    </ul>
  );
}

export function LegendKey({ color, shape = 'line' }: { color: string; shape?: LegendShape }) {
  if (shape === 'rect') {
    return (
      <span
        aria-hidden
        style={{
          width: 12,
          height: 12,
          background: color,
          borderRadius: 2,
          display: 'inline-block',
          flex: 'none',
        }}
      />
    );
  }
  return (
    <svg width="16" height="8" aria-hidden focusable="false" style={{ flex: 'none' }}>
      <line
        x1={0}
        y1={4}
        x2={16}
        y2={4}
        stroke={color}
        strokeWidth={2}
        strokeLinecap="round"
        strokeDasharray={shape === 'dashed' ? '4 3' : undefined}
      />
    </svg>
  );
}
