export {
  FONT_IDS,
  FONT_LABELS,
  DEFAULT_THEME,
  derivePrimaryTokens,
  applyOrgTheme,
} from './theme';
export type { FontId, OrgTheme } from './theme';
export { ThemeSwitcher } from './ThemeSwitcher';
export { TableScroll } from './TableScroll';
export { PeriodPicker } from './PeriodPicker';
export type { PeriodPickerProps } from './PeriodPicker';
export {
  CADENCES,
  CADENCE_LABELS,
  addDays,
  daysBetween,
  formatDate,
  parseDate,
  periodLabel,
  resolvePeriod,
  shiftPeriod,
  today,
} from './period';
export type { Cadence, PeriodValue } from './period';

/* Charts (phase 11) — inline SVG, no chart library. See charts/index.ts. */
export {
  LineAreaChart,
  BarChart,
  Sparkline,
  StatTile,
  ChangeMark,
  RowBar,
  Legend,
  LegendKey,
  ChartFrame,
  ChartTooltip,
  HoverLayer,
  useChartWidth,
  GridLines,
  XTicks,
  PAD,
  CHART_SLOTS,
  CHART_ROLES,
  compactNumber,
  compactTZS,
  fullTZS,
  pctLabel,
  changeLabel,
  bucketTick,
  bucketHeading,
  tickStride,
  linearScale,
  niceDomain,
  bands,
  nearestIndex,
  linePath,
  areaPath,
} from './charts';
export type {
  LineAreaChartProps,
  LineSeries,
  BarChartProps,
  BarSeries,
  SparklineProps,
  StatTileProps,
  GoodDirection,
  LegendItem,
  LegendShape,
  TooltipRow,
  Bucket,
  LinearScale,
  NiceDomain,
  Band,
} from './charts';
