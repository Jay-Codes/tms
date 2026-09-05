'use client';

/**
 * Lines (optionally washed with an area fill) over time.
 *
 * Used for collected against expected, for net, and for occupancy. Rules the
 * dataviz skill fixes and this component enforces:
 *
 *  - **One y-axis, always including zero.** Two measures of different size get
 *    two charts, never a second scale.
 *  - 2px strokes, ≥8px end markers with a 2px surface ring, area at ~10%.
 *  - A dashed series is a *reference* (what was expected), drawn in soft ink
 *    rather than a categorical hue, so it never competes with what happened.
 *  - Crosshair snaps to the nearest bucket; the tooltip lists every series.
 */

import { useCallback, useState } from 'react';
import { GridLines, PAD, XTicks, padLeft } from './axis';
import { ChartFrame, ChartTooltip, HoverLayer, stepIndex, useDismissOnOutside } from './ChartFrame';
import { Legend, type LegendItem } from './Legend';
import { compactNumber, tickStride } from './format';
import { areaPath, bands, linePath, linearScale, niceDomain } from './scale';

export interface LineSeries {
  id: string;
  label: string;
  /** A CSS colour — in this app always a `var(--chart-…)` token. */
  color: string;
  values: Array<number | null>;
  /** Draw as a reference line: dashed, no area, no end marker. */
  dashed?: boolean;
  /** Wash the space under the line at 10%. Only ever on one series. */
  area?: boolean;
}

export interface LineAreaChartProps {
  /** One label per x position; also the tooltip heading source. */
  labels: string[];
  /** Longer heading for the tooltip, when the tick label is abbreviated. */
  headings?: string[];
  series: LineSeries[];
  height?: number;
  title?: string;
  ariaLabel: string;
  /** Tooltip formatting. Axis ticks use the compact form. */
  formatValue?: (v: number) => string;
  formatTick?: (v: number) => string;
  /** Shown in place of the plot when there is nothing to draw. */
  empty?: string;
}

export function LineAreaChart({
  labels,
  headings,
  series,
  height = 260,
  title,
  ariaLabel,
  formatValue = (v) => v.toLocaleString('en-US'),
  formatTick = compactNumber,
  empty,
}: LineAreaChartProps) {
  const [active, setActive] = useState<number | null>(null);
  const dismiss = useCallback(() => setActive(null), []);
  useDismissOnOutside(dismiss, active !== null);

  const h = Math.max(240, height);
  const all = series.flatMap((s) => s.values.filter((v): v is number => v !== null));
  const nothing = labels.length === 0 || all.length === 0 || all.every((v) => v === 0);
  const domain = niceDomain(all);

  const legendItems: LegendItem[] = series.map((s) => ({
    label: s.label,
    color: s.color,
    shape: s.dashed ? 'dashed' : 'line',
  }));

  /** Plot geometry for a measured width — shared by the SVG and the tooltip. */
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
              dashed: s.dashed,
              value:
                s.values[active] === null || s.values[active] === undefined
                  ? '—'
                  : formatValue(s.values[active] as number),
            }))}
          />
        );
      }}
    >
      {(width) => {
        const { x0, x1, y0, y1, y, centers, stride } = geometry(width);

        return (
          <>
            <GridLines ticks={domain.ticks} y={y} x0={x0} x1={x1} format={formatTick} />
            <XTicks labels={labels} centers={centers} stride={stride} y={y1 + 8} />

            {series.map((s) => {
              const pts = s.values.map((v, i) =>
                v === null || centers[i] === undefined ? null : ([centers[i], y(v)] as [number, number]),
              );
              return (
                <g key={s.id}>
                  {s.area ? <path d={areaPath(pts, y(Math.max(domain.min, 0)))} fill={s.color} opacity={0.1} /> : null}
                  <path
                    d={linePath(pts)}
                    fill="none"
                    stroke={s.color}
                    strokeWidth={2}
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeDasharray={s.dashed ? '6 4' : undefined}
                  />
                  {/* End marker: the one direct mark on the line, ringed in the
                      surface colour so it stays legible where lines cross. */}
                  {!s.dashed
                    ? (() => {
                        const last = [...pts].reverse().find((p): p is [number, number] => p !== null);
                        return last ? (
                          <circle
                            cx={last[0]}
                            cy={last[1]}
                            r={4}
                            fill={s.color}
                            stroke="var(--chart-surface)"
                            strokeWidth={2}
                          />
                        ) : null;
                      })()
                    : null}
                </g>
              );
            })}

            {/* The hovered bucket's dots, so the crosshair reading is unambiguous. */}
            {active !== null
              ? series.map((s) => {
                  const v = s.values[active];
                  if (v === null || v === undefined || centers[active] === undefined) return null;
                  return (
                    <circle
                      key={`a-${s.id}`}
                      cx={centers[active]}
                      cy={y(v)}
                      r={4.5}
                      fill={s.color}
                      stroke="var(--chart-surface)"
                      strokeWidth={2}
                    />
                  );
                })
              : null}

            <line
              x1={x0}
              x2={x1}
              y1={Math.round(y(Math.max(domain.min, 0))) + 0.5}
              y2={Math.round(y(Math.max(domain.min, 0))) + 0.5}
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
            />
          </>
        );
      }}
    </ChartFrame>
  );
}
