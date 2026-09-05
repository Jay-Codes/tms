/**
 * Charts for the TMS apps (PLAN2 phase 11). Inline SVG, no chart library, one
 * validated palette shared by every chart (`--chart-1…8` in tokens.css).
 *
 * Rules the whole folder holds to (dataviz skill):
 *  - one y-axis, always including zero — never a second scale;
 *  - categorical hues are assigned in the fixed slot order, never cycled;
 *  - a legend whenever two or more series are drawn, plus sparing direct
 *    marks; text never wears a series colour;
 *  - every chart has a hover/tap/keyboard readout, and every figure in that
 *    readout is also in the table beside the chart.
 */

export { LineAreaChart } from './LineAreaChart';
export type { LineAreaChartProps, LineSeries } from './LineAreaChart';
export { BarChart } from './BarChart';
export type { BarChartProps, BarSeries } from './BarChart';
export { Sparkline } from './Sparkline';
export type { SparklineProps } from './Sparkline';
export { StatTile, ChangeMark, RowBar } from './StatTile';
export type { StatTileProps, GoodDirection } from './StatTile';
export { Legend, LegendKey } from './Legend';
export type { LegendItem, LegendShape } from './Legend';
export { ChartFrame, ChartTooltip, HoverLayer, useChartWidth } from './ChartFrame';
export type { TooltipRow } from './ChartFrame';
export { GridLines, XTicks, PAD } from './axis';
export {
  compactNumber,
  compactTZS,
  fullTZS,
  pctLabel,
  changeLabel,
  bucketTick,
  bucketHeading,
  tickStride,
} from './format';
export type { Bucket } from './format';
export {
  linearScale,
  niceDomain,
  bands,
  nearestIndex,
  linePath,
  areaPath,
} from './scale';
export type { LinearScale, NiceDomain, Band } from './scale';

/**
 * The categorical slot order, for callers that colour a variable number of
 * series (expense categories). Assign in order and fold the tail into
 * "Other" — a ninth series is never a generated hue.
 */
export const CHART_SLOTS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--chart-6)',
  'var(--chart-7)',
  'var(--chart-8)',
] as const;

/** Roles the money charts use, so every screen names the same colour. */
export const CHART_ROLES = {
  collected: 'var(--chart-collected)',
  expected: 'var(--chart-expected)',
  expenses: 'var(--chart-expenses)',
  net: 'var(--chart-net)',
  occupancy: 'var(--chart-occupancy)',
} as const;
