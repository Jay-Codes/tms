'use client';

/**
 * The frame every chart is drawn in.
 *
 * Two things live here because every chart needs them and none should
 * re-invent them:
 *
 *  1. **Measured width.** The SVG's viewBox is the box it was actually
 *     measured at, so one pixel of viewBox is one pixel on screen and a 12px
 *     label stays 12px at 375px as well as at 1280px. (A fixed viewBox scaled
 *     to `width:100%` shrinks the type along with the drawing, which is how
 *     charts end up with 6px axis labels on a phone.)
 *  2. **The hover layer.** A crosshair that snaps to the nearest bucket, a
 *     tooltip listing every series at that bucket, and the same readout on
 *     tap and on keyboard focus — the tooltip enhances, it never gates: every
 *     figure in it is also in the table beneath the chart.
 */

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

/** Width of the box the chart is in; falls back to a sensible desktop width. */
export function useChartWidth(fallback = 720): [React.RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(fallback);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const set = () => setWidth(Math.max(240, Math.round(el.getBoundingClientRect().width)));
    set();
    // Both, on purpose: the observer catches a box that changes without the
    // window doing so (a drawer opening, a tab switching), and the window
    // listener is the backstop for the cases where observer callbacks are not
    // delivered — a throttled or occluded tab, or a browser without one.
    window.addEventListener('resize', set);
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(set);
    ro?.observe(el);
    return () => {
      window.removeEventListener('resize', set);
      ro?.disconnect();
    };
  }, []);

  return [ref, width];
}

export interface TooltipRow {
  label: string;
  value: string;
  color: string;
  dashed?: boolean;
}

/**
 * The readout. Values lead and labels follow (the reader already knows which
 * series they are after; they want the number), and each row is keyed with a
 * short stroke of the series colour rather than a filled block.
 */
export function ChartTooltip({
  heading,
  rows,
  x,
  width,
  boxWidth = 200,
}: {
  heading: string;
  rows: TooltipRow[];
  x: number;
  width: number;
  boxWidth?: number;
}) {
  // Flip the box to the other side of the crosshair rather than let it fall
  // off the edge of a phone screen.
  const left = Math.min(Math.max(8, x + 12), Math.max(8, width - boxWidth - 8));
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        position: 'absolute',
        top: 8,
        left,
        width: boxWidth,
        pointerEvents: 'none',
        background: 'var(--sheet)',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-sm)',
        boxShadow: '0 6px 18px rgb(28 43 90 / 0.12)',
        padding: 'var(--sp-2) var(--sp-3)',
        fontSize: 'var(--text-sm)',
        display: 'grid',
        gap: 2,
        zIndex: 2,
      }}
    >
      <span style={{ color: 'var(--ink-soft)' }}>{heading}</span>
      {rows.map((r) => (
        <span
          key={r.label}
          style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', justifyContent: 'space-between' }}
        >
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', minWidth: 0 }}>
            <svg width="14" height="8" aria-hidden focusable="false" style={{ flex: 'none' }}>
              <line
                x1={0}
                y1={4}
                x2={14}
                y2={4}
                stroke={r.color}
                strokeWidth={2}
                strokeDasharray={r.dashed ? '4 3' : undefined}
              />
            </svg>
            <span style={{ color: 'var(--ink-soft)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{r.label}</span>
          </span>
          <strong style={{ fontVariantNumeric: 'tabular-nums lining-nums', whiteSpace: 'nowrap' }}>{r.value}</strong>
        </span>
      ))}
    </div>
  );
}

export interface HoverLayerProps {
  /** Band centres, in viewBox pixels. */
  centers: number[];
  x0: number;
  x1: number;
  y0: number;
  y1: number;
  active: number | null;
  onActive: (i: number | null) => void;
  /** Draw the vertical hairline (lines/areas). Bars key off the mark instead. */
  crosshair?: boolean;
}

/**
 * One transparent rect over the plot, because aiming at a 2px line is not a
 * thing anyone can do — the pointer only has to be *nearest*. Tap works the
 * same way, and arrow keys walk the buckets for a keyboard reader.
 */
export function HoverLayer({ centers, x0, x1, y0, y1, active, onActive, crosshair = true }: HoverLayerProps) {
  const pick = useCallback(
    (clientX: number, target: SVGRectElement) => {
      const box = target.getBoundingClientRect();
      if (box.width === 0 || centers.length === 0) return;
      // The rect is drawn in viewBox units and rendered 1:1, but guard anyway.
      const scale = (x1 - x0) / box.width;
      const x = x0 + (clientX - box.left) * scale;
      let best = 0;
      let bestD = Infinity;
      for (let i = 0; i < centers.length; i += 1) {
        const d = Math.abs(centers[i] - x);
        if (d < bestD) {
          bestD = d;
          best = i;
        }
      }
      onActive(best);
    },
    [centers, onActive, x0, x1],
  );

  return (
    <>
      {crosshair && active !== null && centers[active] !== undefined ? (
        <line
          x1={centers[active]}
          x2={centers[active]}
          y1={y0}
          y2={y1}
          stroke="var(--chart-axis)"
          strokeWidth={1}
          shapeRendering="crispEdges"
          aria-hidden
        />
      ) : null}
      <rect
        x={x0}
        y={y0}
        width={Math.max(0, x1 - x0)}
        height={Math.max(0, y1 - y0)}
        fill="transparent"
        style={{ touchAction: 'pan-y' }}
        onPointerMove={(e) => pick(e.clientX, e.currentTarget)}
        onPointerDown={(e) => pick(e.clientX, e.currentTarget)}
        onPointerLeave={() => onActive(null)}
        onPointerCancel={() => onActive(null)}
      />
    </>
  );
}

/**
 * Title, chart, legend, caption — one figure. The chart body is a render prop
 * so it gets the measured width without every caller wiring the observer.
 */
export function ChartFrame({
  title,
  ariaLabel,
  height,
  children,
  overlay,
  legend,
  footer,
  empty,
  onKeyStep,
}: {
  title?: ReactNode;
  ariaLabel: string;
  height: number;
  children: (width: number) => ReactNode;
  /**
   * The tooltip. It is HTML beside the SVG rather than a `foreignObject`
   * inside it — same layout, no Safari foreignObject quirks, and it can
   * overflow the plot without being clipped.
   */
  overlay?: (width: number) => ReactNode;
  legend?: ReactNode;
  footer?: ReactNode;
  /** Shown instead of the plot when there is nothing to draw. */
  empty?: ReactNode;
  onKeyStep?: (delta: number) => void;
}) {
  const [ref, width] = useChartWidth();
  const id = useId();

  return (
    <figure style={{ margin: 0, display: 'grid', gap: 'var(--sp-3)' }}>
      {title ? (
        <figcaption style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }} id={`${id}-t`}>
          {title}
        </figcaption>
      ) : null}
      <div ref={ref} style={{ position: 'relative', width: '100%' }}>
        {empty ? (
          <div
            style={{
              minHeight: height,
              display: 'grid',
              placeItems: 'center',
              border: '1px dashed var(--rule)',
              borderRadius: 'var(--radius-sm)',
              color: 'var(--ink-soft)',
              fontSize: 'var(--text-sm)',
              textAlign: 'center',
              padding: 'var(--sp-4)',
            }}
          >
            {empty}
          </div>
        ) : (
          <svg
            viewBox={`0 0 ${width} ${height}`}
            width="100%"
            height={height}
            role="img"
            aria-label={ariaLabel}
            tabIndex={onKeyStep ? 0 : undefined}
            onKeyDown={
              onKeyStep
                ? (e) => {
                    if (e.key === 'ArrowRight') {
                      onKeyStep(1);
                      e.preventDefault();
                    } else if (e.key === 'ArrowLeft') {
                      onKeyStep(-1);
                      e.preventDefault();
                    }
                  }
                : undefined
            }
            style={{ display: 'block', width: '100%', height, touchAction: 'pan-y' }}
          >
            {children(width)}
          </svg>
        )}
        {!empty && overlay ? overlay(width) : null}
      </div>
      {legend}
      {footer}
    </figure>
  );
}

/** Clamp an index into `[0, count)`, or null when there is nothing to point at. */
export function stepIndex(active: number | null, delta: number, count: number): number | null {
  if (count === 0) return null;
  if (active === null) return delta > 0 ? 0 : count - 1;
  return Math.min(count - 1, Math.max(0, active + delta));
}

/** Close the tooltip when the reader taps somewhere else on the page. */
export function useDismissOnOutside(onDismiss: () => void, active: boolean) {
  useEffect(() => {
    if (!active) return;
    const h = (e: Event) => {
      const t = e.target as HTMLElement | null;
      if (t && t.closest('figure')) return;
      onDismiss();
    };
    document.addEventListener('pointerdown', h);
    return () => document.removeEventListener('pointerdown', h);
  }, [active, onDismiss]);
}
