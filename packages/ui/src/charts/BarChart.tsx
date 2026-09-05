'use client';

/**
 * Columns over time — grouped or stacked, and safe with negatives (a month
 * that spent more than it collected draws below the baseline rather than
 * being clipped to zero).
 *
 * Mark specs are the fixed ones: ≤24px thick, a 4px rounded data-end with the
 * baseline end square, and a 2px surface gap between touching marks. Pointing
 * at a column lifts it by letting the others recede — no colour change, so the
 * series colours keep meaning what they meant. The mark is the hit target
 * here, so there is no crosshair.
 */

import { useCallback, useState } from 'react';
import { GridLines, PAD, XTicks, padLeft } from './axis';
import { ChartFrame, ChartTooltip, HoverLayer, stepIndex, useDismissOnOutside } from './ChartFrame';
import { Legend, type LegendItem } from './Legend';
import { compactNumber, tickStride } from './format';
import { bands, linearScale, niceDomain } from './scale';

export interface BarSeries {
  id: string;
  label: string;
  color: string;
  values: number[];
}

export interface BarChartProps {
  labels: string[];
  headings?: string[];
  series: BarSeries[];
  /** Stack the series instead of standing them side by side. */
  stacked?: boolean;
  height?: number;
  title?: string;
  ariaLabel: string;
  formatValue?: (v: number) => string;
  formatTick?: (v: number) => string;
  empty?: string;
}

const MAX_BAR = 24;
const GAP = 2; // the surface gap, in pixels — one width everywhere

export function BarChart({
  labels,
  headings,
  series,
  stacked = false,
  height = 260,
  title,
  ariaLabel,
  formatValue = (v) => v.toLocaleString('en-US'),
  formatTick = compactNumber,
  empty,
}: BarChartProps) {
  const [active, setActive] = useState<number | null>(null);
  const dismiss = useCallback(() => setActive(null), []);
  useDismissOnOutside(dismiss, active !== null);

  const h = Math.max(240, height);
  const flat = series.flatMap((s) => s.values);
  const nothing = labels.length === 0 || flat.length === 0 || flat.every((v) => !v);

  // A stack's extent is the running sum per side, not the biggest bar.
  const extents = stacked
    ? labels.map((_, i) => {
        let up = 0;
        let down = 0;
        for (const s of series) {
          const v = s.values[i] ?? 0;
          if (v >= 0) up += v;
          else down += v;
        }
        return [up, down];
      })
    : [flat];
  const domain = niceDomain(extents.flat());

  const legendItems: LegendItem[] = series.map((s) => ({ label: s.label, color: s.color, shape: 'rect' }));

  const geometry = (width: number) => {
    const x0 = padLeft(width);
    const x1 = Math.max(x0 + 10, width - PAD.right);
    const band = bands(labels.length, x0, x1);
    return {
      x0,
      x1,
      y0: PAD.top,
      y1: h - PAD.bottom,
      y: linearScale([domain.min, domain.max], [h - PAD.bottom, PAD.top]),
      band,
      centers: band.map((b) => b.center),
      stride: tickStride(labels.length, band[0]?.slot ?? x1 - x0),
    };
  };

  return (
    <ChartFrame
      title={title}
      ariaLabel={ariaLabel}
      height={h}
      legend={<Legend items={legendItems} />}
      empty={nothing ? (empty ?? 'No activity in this period.') : undefined}
      onKeyStep={(d) => setActive((a) => stepIndex(a, d, labels.length))}
      overlay={(width) => {
        const { centers } = geometry(width);
        if (active === null || centers[active] === undefined) return null;
        return (
          <ChartTooltip
            heading={(headings ?? labels)[active] ?? ''}
            x={centers[active]}
            width={width}
            rows={series.map((s) => ({
              label: s.label,
              color: s.color,
              value: formatValue(s.values[active] ?? 0),
            }))}
          />
        );
      }}
    >
      {(width) => {
        const { x0, x1, y0, y1, y, band, centers, stride } = geometry(width);
        const zero = y(0);
        const slot = band[0]?.slot ?? x1 - x0;
        const groupW = Math.min(MAX_BAR * series.length + GAP * (series.length - 1), slot * 0.7);
        const barW = stacked
          ? Math.min(MAX_BAR, slot * 0.6)
          : Math.max(2, (groupW - GAP * (series.length - 1)) / Math.max(1, series.length));

        return (
          <>
            <GridLines ticks={domain.ticks} y={y} x0={x0} x1={x1} format={formatTick} />
            <XTicks labels={labels} centers={centers} stride={stride} y={y1 + 8} />

            {labels.map((label, i) => {
              const c = centers[i];
              if (c === undefined) return null;
              let up = 0;
              let down = 0;
              return (
                <g key={`${label}-${i}`} opacity={active === null || active === i ? 1 : 0.55}>
                  {series.map((s, k) => {
                    const v = s.values[i] ?? 0;
                    let x: number;
                    let top: number;
                    let bottom: number;
                    if (stacked) {
                      x = c - barW / 2;
                      if (v >= 0) {
                        top = y(up + v);
                        bottom = y(up);
                        up += v;
                      } else {
                        top = y(down);
                        bottom = y(down + v);
                        down += v;
                      }
                      // The 2px surface gap between touching segments.
                      if (Math.abs(bottom - top) > GAP) {
                        if (v >= 0) bottom -= GAP;
                        else top += GAP;
                      }
                    } else {
                      x = c - groupW / 2 + k * (barW + GAP);
                      top = v >= 0 ? y(v) : zero;
                      bottom = v >= 0 ? zero : y(v);
                    }
                    const hgt = Math.max(0, bottom - top);
                    if (hgt <= 0.5) return null;
                    // 4px rounded data-end, square where it meets the baseline.
                    const r = Math.min(4, barW / 2, hgt);
                    const grows = v >= 0;
                    const d = grows
                      ? `M${x} ${bottom} L${x} ${top + r} Q${x} ${top} ${x + r} ${top} L${x + barW - r} ${top} Q${
                          x + barW
                        } ${top} ${x + barW} ${top + r} L${x + barW} ${bottom} Z`
                      : `M${x} ${top} L${x} ${bottom - r} Q${x} ${bottom} ${x + r} ${bottom} L${x + barW - r} ${bottom} Q${
                          x + barW
                        } ${bottom} ${x + barW} ${bottom - r} L${x + barW} ${top} Z`;
                    return <path key={s.id} d={d} fill={s.color} />;
                  })}
                </g>
              );
            })}

            <line
              x1={x0}
              x2={x1}
              y1={Math.round(zero) + 0.5}
              y2={Math.round(zero) + 0.5}
              stroke="var(--chart-axis)"
              strokeWidth={1}
              shapeRendering="crispEdges"
            />

            <HoverLayer
              centers={centers}
              x0={x0}
              x1={x1}
              y0={y0}
              y1={y1}
              active={active}
              onActive={setActive}
              crosshair={false}
            />
          </>
        );
      }}
    </ChartFrame>
  );
}
