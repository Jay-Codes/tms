/**
 * Scales and tick maths for the charts (Phase 11). Pure functions — no React,
 * no DOM, so they can be reasoned about (and tested) on their own.
 *
 * Every chart in the app is drawn in **pixel space**: the SVG's viewBox is set
 * to the box it was measured at, so a 12px label is 12 real pixels at 375px
 * and at 1280px. That is why there is no "scale the whole drawing" step here.
 */

export interface LinearScale {
  (value: number): number;
  domain: [number, number];
  range: [number, number];
}

export function linearScale(domain: [number, number], range: [number, number]): LinearScale {
  const [d0, d1] = domain;
  const [r0, r1] = range;
  const span = d1 - d0 || 1;
  const fn = ((v: number) => r0 + ((v - d0) / span) * (r1 - r0)) as LinearScale;
  fn.domain = domain;
  fn.range = range;
  return fn;
}

/** Round a step up to the nearest 1, 2 or 5 × a power of ten. */
function niceStep(raw: number): number {
  const exp = Math.floor(Math.log10(raw));
  const pow = 10 ** exp;
  const f = raw / pow;
  if (f <= 1) return pow;
  if (f <= 2) return 2 * pow;
  if (f <= 5) return 5 * pow;
  return 10 * pow;
}

export interface NiceDomain {
  min: number;
  max: number;
  ticks: number[];
}

/**
 * A domain with clean tick values that always contains zero — money charts are
 * read against a baseline, and a truncated y-axis lies about the size of a
 * change (see the dataviz anti-patterns). Negative values (a loss-making net)
 * push the baseline up rather than being clipped.
 */
export function niceDomain(values: number[], targetTicks = 4): NiceDomain {
  const finite = values.filter((v) => Number.isFinite(v));
  const lo = Math.min(0, ...finite);
  const hi = Math.max(0, ...finite);
  if (lo === 0 && hi === 0) return { min: 0, max: 1, ticks: [0, 1] };

  const step = niceStep((hi - lo) / Math.max(1, targetTicks));
  const min = Math.floor(lo / step) * step;
  const max = Math.ceil(hi / step) * step;
  const ticks: number[] = [];
  // Float steps drift; round each tick back onto the step grid.
  for (let i = 0, v = min; v <= max + step / 2 && i < 40; i += 1, v = min + step * i) {
    ticks.push(Number((min + step * i).toPrecision(12)));
  }
  return { min, max: ticks[ticks.length - 1] ?? max, ticks };
}

export interface Band {
  /** Left edge of the slot this index owns. */
  start: number;
  /** Centre of the slot — where a point or a grouped pair is anchored. */
  center: number;
  /** Full slot width, padding included. */
  slot: number;
}

/** Evenly divide `[x0, x1]` into `count` slots. */
export function bands(count: number, x0: number, x1: number): Band[] {
  const slot = count > 0 ? (x1 - x0) / count : x1 - x0;
  return Array.from({ length: count }, (_, i) => ({
    start: x0 + slot * i,
    center: x0 + slot * i + slot / 2,
    slot,
  }));
}

/** The index whose band centre is nearest `x` — the crosshair's snap. */
export function nearestIndex(xs: number[], x: number): number {
  let best = 0;
  let bestD = Infinity;
  for (let i = 0; i < xs.length; i += 1) {
    const d = Math.abs(xs[i] - x);
    if (d < bestD) {
      bestD = d;
      best = i;
    }
  }
  return best;
}

/** SVG path through `points`, skipping nulls (a gap is honest about missing data). */
export function linePath(points: Array<[number, number] | null>): string {
  let d = '';
  let pen = false;
  for (const p of points) {
    if (!p) {
      pen = false;
      continue;
    }
    d += `${pen ? 'L' : 'M'}${p[0].toFixed(2)} ${p[1].toFixed(2)} `;
    pen = true;
  }
  return d.trim();
}

/** The same path closed down to `baseY`, for the area wash under a line. */
export function areaPath(points: Array<[number, number] | null>, baseY: number): string {
  const solid = points.filter((p): p is [number, number] => p !== null);
  if (solid.length === 0) return '';
  const first = solid[0];
  const last = solid[solid.length - 1];
  return `${linePath(solid)} L${last[0].toFixed(2)} ${baseY.toFixed(2)} L${first[0].toFixed(2)} ${baseY.toFixed(
    2,
  )} Z`;
}
