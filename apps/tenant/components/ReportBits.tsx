'use client';

/**
 * Shared pieces for the Phase 7 reports and the dashboard (FLOWS flow 9).
 *
 * Everything here is a pure renderer over figures the backend has already
 * computed — no arithmetic beyond laying a number out. The one exception is
 * the chart, which turns money into pixels, and even that only scales; it
 * never sums.
 */

import { Icon } from '@iconify/react';
import { formatDateIntl, useT } from '@tms/ui';
import type { ReactNode } from 'react';
import type { CollectionsBucket, PaymentStatusValue } from '../lib/api';
import { fmtTZS, formatLocale } from '../lib/format';

/* --------------------------------- tabs ---------------------------------- */

export interface TabDef<T extends string> {
  value: T;
  label: string;
}

/** Horizontal tab strip, same shape the payments screen uses. */
export function TabBar<T extends string>({
  tabs,
  value,
  onChange,
}: {
  tabs: readonly TabDef<T>[];
  value: T;
  onChange: (v: NoInfer<T>) => void;
}) {
  return (
    <div
      role="tablist"
      style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}
    >
      {tabs.map((t) => {
        const active = value === t.value;
        return (
          <button
            key={t.value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(t.value)}
            style={{
              minHeight: 'var(--touch-min)',
              padding: '0 var(--sp-4)',
              border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
              borderRadius: 'var(--radius-md)',
              background: active ? 'var(--primary-soft)' : 'transparent',
              color: 'var(--ink)',
              fontWeight: active ? 600 : 400,
              fontSize: 'var(--text-sm)',
              cursor: 'pointer',
            }}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}

/* -------------------------------- tiles ---------------------------------- */

/** One figure with its label. Flat — no card grid, no shadow (SPEC §2.0). */
export function StatTile({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  tone?: 'overdue' | 'paid';
}) {
  const color =
    tone === 'overdue' ? 'var(--stamp-overdue)' : tone === 'paid' ? 'var(--stamp-paid)' : 'var(--ink)';
  return (
    <div
      style={{
        borderTop: '1px solid var(--rule-strong)',
        paddingTop: 'var(--sp-3)',
        display: 'grid',
        gap: 'var(--sp-1)',
        alignContent: 'start',
      }}
    >
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</span>
      <strong style={{ fontSize: 'var(--text-xl)', color, fontVariantNumeric: 'tabular-nums lining-nums' }}>
        {value}
      </strong>
      {/* `stat-tile-sub` is the hook the phone rules use to turn a bare link
          here into a real touch target (tokens.css, Phase 15). */}
      {sub ? (
        <span className="stat-tile-sub" style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {sub}
        </span>
      ) : null}
    </div>
  );
}

/** Auto-fitting row of tiles. */
export function TileRow({ children, min = 200 }: { children: ReactNode; min?: number }) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: `repeat(auto-fit, minmax(${min}px, 1fr))`,
        gap: 'var(--sp-5)',
      }}
    >
      {children}
    </div>
  );
}

export function SectionHead({ icon, title, aside }: { icon: string; title: string; aside?: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: 'var(--sp-4)',
        flexWrap: 'wrap',
      }}
    >
      <h2 style={{ fontSize: 'var(--text-lg)', display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
        <Icon icon={icon} width={20} /> {title}
      </h2>
      {aside}
    </div>
  );
}

/* -------------------------------- stamps --------------------------------- */

/**
 * A renter's payment standing. Overdue is stamped in red, paid in green; the
 * two open states are only pencilled, because nothing has happened yet.
 */
export function PaymentStatusStampCell({ status }: { status: PaymentStatusValue }) {
  const t = useT();
  if (status === 'overdue') return <span className="stamp stamp-overdue">{t('reports.stamp.overdue')}</span>;
  if (status === 'paid') return <span className="stamp stamp-paid">{t('reports.stamp.paid')}</span>;
  if (status === 'partial') return <span className="pencil">{t('reports.stamp.partial')}</span>;
  return <span className="pencil">{t('reports.stamp.pending')}</span>;
}

/* -------------------------------- chart ---------------------------------- */

const CHART_W = 720;
const CHART_H = 220;
const PAD_L = 8;
const PAD_R = 8;
const PAD_T = 8;
const PAD_B = 34;

/**
 * `2026-09-01` → `Sep`, or `1 Sep` for a day/week bucket — in the active
 * portal language, through the shared `formatDateIntl` helper.
 */
function bucketLabel(start: string, group: 'day' | 'week' | 'month'): string {
  const d = new Date(start.length === 10 ? `${start}T00:00:00Z` : start);
  if (Number.isNaN(d.getTime())) return start;
  const locale = formatLocale();
  if (group === 'month') {
    return formatDateIntl(locale, start, { day: undefined, month: 'short', year: undefined });
  }
  return formatDateIntl(locale, start, { day: 'numeric', month: 'short', year: undefined });
}

/**
 * Expected against collected, one pair of bars per bucket. Inline SVG, no chart
 * library: expected is drawn as an open outline (what was written down),
 * collected as a solid fill (what actually came in), so the gap between the two
 * is the shortfall you can read at a glance.
 */
export function CollectionsChart({
  buckets,
  group,
}: {
  buckets: CollectionsBucket[];
  group: 'day' | 'week' | 'month';
}) {
  const t = useT();
  if (buckets.length === 0) return null;

  const max = Math.max(1, ...buckets.map((b) => Math.max(b.expected, b.collected)));
  const plotW = CHART_W - PAD_L - PAD_R;
  const plotH = CHART_H - PAD_T - PAD_B;
  const slot = plotW / buckets.length;
  const barW = Math.max(4, Math.min(28, slot * 0.34));
  const gap = Math.min(6, slot * 0.08);
  const y = (v: number) => PAD_T + plotH - (Math.max(0, v) / max) * plotH;
  // A label per bucket is unreadable past a couple of dozen; thin them out.
  const every = Math.ceil(buckets.length / 14);

  return (
    <figure style={{ margin: 0, display: 'grid', gap: 'var(--sp-3)' }}>
      <svg
        viewBox={`0 0 ${CHART_W} ${CHART_H}`}
        width="100%"
        height={CHART_H}
        role="img"
        aria-label={t('reports.collections.chart_bars_aria', { count: buckets.length })}
        style={{ overflow: 'visible' }}
      >
        <line
          x1={0}
          y1={PAD_T + plotH}
          x2={CHART_W}
          y2={PAD_T + plotH}
          stroke="var(--rule-strong)"
          strokeWidth={1}
        />
        {buckets.map((b, i) => {
          const cx = PAD_L + slot * i + slot / 2;
          const ex = cx - barW - gap / 2;
          const cxx = cx + gap / 2;
          return (
            <g key={b.start}>
              <title>
                {t('reports.collections.chart_tooltip', {
                  label: bucketLabel(b.start, group),
                  expected: fmtTZS(b.expected),
                  collected: fmtTZS(b.collected),
                })}
              </title>
              <rect
                x={ex}
                y={y(b.expected)}
                width={barW}
                height={Math.max(0, PAD_T + plotH - y(b.expected))}
                fill="none"
                stroke="var(--ink-soft)"
                strokeWidth={1}
              />
              <rect
                x={cxx}
                y={y(b.collected)}
                width={barW}
                height={Math.max(0, PAD_T + plotH - y(b.collected))}
                fill="var(--primary)"
              />
              {i % every === 0 ? (
                <text
                  x={cx}
                  y={PAD_T + plotH + 20}
                  textAnchor="middle"
                  fontSize={12}
                  fill="var(--ink-soft)"
                >
                  {bucketLabel(b.start, group)}
                </text>
              ) : null}
            </g>
          );
        })}
      </svg>
      <figcaption
        style={{
          display: 'flex',
          gap: 'var(--sp-5)',
          fontSize: 'var(--text-sm)',
          color: 'var(--ink-soft)',
        }}
      >
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          <span
            aria-hidden
            style={{ width: 14, height: 14, border: '1px solid var(--ink-soft)', display: 'inline-block' }}
          />
          {t('reports.series.expected')}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          <span aria-hidden style={{ width: 14, height: 14, background: 'var(--primary)', display: 'inline-block' }} />
          {t('reports.series.collected')}
        </span>
      </figcaption>
    </figure>
  );
}

export { bucketLabel };
