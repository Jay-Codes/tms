'use client';

/**
 * A sparkline is a shape, not a chart: no axis, no grid, no tooltip. It sits
 * beside a figure to say which way the figure has been moving, and the number
 * it belongs to carries the actual value.
 */

import { areaPath, linePath, linearScale, niceDomain } from './scale';

export interface SparklineProps {
  values: number[];
  color?: string;
  width?: number;
  height?: number;
  /** Fill the space under the line at 10%. */
  area?: boolean;
  /** Sentence a screen reader gets instead of the drawing. */
  ariaLabel: string;
}

export function Sparkline({
  values,
  color = 'var(--chart-1)',
  width = 120,
  height = 32,
  area = true,
  ariaLabel,
}: SparklineProps) {
  const clean = values.filter((v) => Number.isFinite(v));
  if (clean.length < 2) {
    return (
      <span className="pencil" style={{ fontSize: 'var(--text-sm)' }}>
        not enough history
      </span>
    );
  }

  const pad = 3;
  const domain = niceDomain(clean, 2);
  const y = linearScale([domain.min, domain.max], [height - pad, pad]);
  const step = (width - pad * 2) / (clean.length - 1);
  const pts = clean.map((v, i) => [pad + step * i, y(v)] as [number, number]);
  const last = pts[pts.length - 1];

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      role="img"
      aria-label={ariaLabel}
      style={{ display: 'block', overflow: 'visible' }}
    >
      {area ? <path d={areaPath(pts, y(Math.max(domain.min, 0)))} fill={color} opacity={0.1} /> : null}
      <path d={linePath(pts)} fill="none" stroke={color} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" />
      <circle cx={last[0]} cy={last[1]} r={2.5} fill={color} stroke="var(--chart-surface)" strokeWidth={2} />
    </svg>
  );
}
