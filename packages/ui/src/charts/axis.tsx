'use client';

/**
 * The chart's chrome: gridlines, the baseline, and the two sets of tick
 * labels. All of it is deliberately recessive — hairline rules in the ledger's
 * own rule colour, labels in soft ink — so the data is the only loud thing on
 * the paper (dataviz: marks-and-anatomy).
 */

import type { LinearScale } from './scale';

export const AXIS_FONT = 12;

/** Padding around the plot area. Left is wide enough for `TZS 1.2M`. */
export interface Pad {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export const PAD: Pad = { left: 62, right: 12, top: 12, bottom: 30 };

/**
 * The y-axis gutter, narrowed on a phone. `TZS 4.4M` needs about 52px at 12px;
 * the desktop gutter is roomier so the labels sit clear of the plot.
 */
export function padLeft(width: number): number {
  return width < 420 ? 52 : PAD.left;
}

export function GridLines({
  ticks,
  y,
  x0,
  x1,
  format,
}: {
  ticks: number[];
  y: LinearScale;
  x0: number;
  x1: number;
  format: (v: number) => string;
}) {
  return (
    <g aria-hidden>
      {ticks.map((t) => {
        const yy = Math.round(y(t)) + 0.5;
        const zero = t === 0;
        return (
          <g key={t}>
            <line
              x1={x0}
              x2={x1}
              y1={yy}
              y2={yy}
              stroke={zero ? 'var(--chart-axis)' : 'var(--chart-grid)'}
              strokeWidth={1}
              shapeRendering="crispEdges"
            />
            <text
              x={x0 - 8}
              y={yy}
              textAnchor="end"
              dominantBaseline="central"
              fontSize={AXIS_FONT}
              fill="var(--chart-label)"
              style={{ fontVariantNumeric: 'tabular-nums lining-nums' }}
            >
              {format(t)}
            </text>
          </g>
        );
      })}
    </g>
  );
}

export function XTicks({
  labels,
  centers,
  stride,
  y,
}: {
  labels: string[];
  centers: number[];
  stride: number;
  y: number;
}) {
  return (
    <g aria-hidden>
      {labels.map((label, i) =>
        i % stride === 0 ? (
          <text
            key={`${label}-${i}`}
            x={centers[i]}
            y={y}
            textAnchor="middle"
            dominantBaseline="hanging"
            fontSize={AXIS_FONT}
            fill="var(--chart-label)"
          >
            {label}
          </text>
        ) : null,
      )}
    </g>
  );
}
