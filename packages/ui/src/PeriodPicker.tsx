'use client';

/**
 * The one period control (PLAN2 phase 9, used by every report and dashboard
 * card in phase 11): a cadence segment, prev/next arrows and the window's
 * label. `custom` swaps the label for two date inputs.
 *
 * The value it emits is the window the backend expects — **[from, to)**, `to`
 * exclusive — so a caller never has to think about whether the last day is in
 * or out. The maths lives in `period.ts` and is unit-tested there.
 */

import { useId } from 'react';
import {
  CADENCES,
  CADENCE_LABELS,
  addDays,
  endsBefore,
  periodLabel,
  resolvePeriod,
  shiftPeriod,
  startsAfter,
  type Cadence,
  type PeriodValue,
} from './period';

export interface PeriodPickerProps {
  value: PeriodValue;
  onChange: (next: PeriodValue) => void;
  /** Earliest selectable day, inclusive (YYYY-MM-DD). */
  minDate?: string;
  /** Latest selectable day, inclusive (YYYY-MM-DD). */
  maxDate?: string;
  /** Accessible name for the whole control. */
  label?: string;
  /** Localised cadence names; falls back to the English CADENCE_LABELS. */
  cadenceLabels?: Partial<Record<Cadence, string>>;
}

function Arrow({ dir }: { dir: 'prev' | 'next' }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden focusable="false">
      <path
        d={dir === 'prev' ? 'M15 5 8 12l7 7' : 'M9 5l7 7-7 7'}
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function PeriodPicker({ value, onChange, minDate, maxDate, label = 'Period', cadenceLabels }: PeriodPickerProps) {
  const id = useId();
  const prev = shiftPeriod(value, -1);
  const next = shiftPeriod(value, 1);
  const custom = value.cadence === 'custom';

  const pick = (cadence: Cadence) => {
    if (cadence === value.cadence) return;
    if (cadence === 'custom') {
      onChange({ cadence: 'custom', from: value.from, to: value.to });
      return;
    }
    // Keep the reader where they were: resolve the new cadence around the
    // window they are looking at.
    onChange(resolvePeriod(cadence, value.from));
  };

  const setBound = (which: 'from' | 'to', day: string) => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return;
    // The inputs show inclusive days; `to` is stored exclusive.
    const from = which === 'from' ? day : value.from;
    const to = which === 'to' ? addDays(day, 1) : value.to;
    if (!(from < to)) return;
    onChange({ cadence: 'custom', from, to });
  };

  return (
    <div className="period-picker" role="group" aria-label={label}>
      <div className="period-cadence">
        {CADENCES.map((c) => (
          <button
            key={c}
            type="button"
            aria-pressed={c === value.cadence}
            onClick={() => pick(c)}
          >
            {cadenceLabels?.[c] ?? CADENCE_LABELS[c]}
          </button>
        ))}
      </div>

      <div style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
        <button
          type="button"
          className="btn btn-secondary"
          aria-label="Previous period"
          disabled={endsBefore(prev, minDate)}
          onClick={() => onChange(prev)}
          style={{ minHeight: 'var(--touch-min)', minWidth: 'var(--touch-min)', padding: 0, justifyContent: 'center' }}
        >
          <Arrow dir="prev" />
        </button>

        {custom ? (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
            <label htmlFor={`${id}-from`} style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
              From
            </label>
            <input
              id={`${id}-from`}
              className="input"
              type="date"
              value={value.from}
              min={minDate}
              max={maxDate}
              onChange={(e) => setBound('from', e.target.value)}
              style={{ width: 'auto' }}
            />
            <label htmlFor={`${id}-to`} style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
              To
            </label>
            <input
              id={`${id}-to`}
              className="input"
              type="date"
              value={addDays(value.to, -1)}
              min={minDate}
              max={maxDate}
              onChange={(e) => setBound('to', e.target.value)}
              style={{ width: 'auto' }}
            />
          </span>
        ) : (
          <strong aria-live="polite" style={{ minWidth: '9ch', textAlign: 'center', fontSize: 'var(--text-md)' }}>
            {periodLabel(value)}
          </strong>
        )}

        <button
          type="button"
          className="btn btn-secondary"
          aria-label="Next period"
          disabled={startsAfter(next, maxDate)}
          onClick={() => onChange(next)}
          style={{ minHeight: 'var(--touch-min)', minWidth: 'var(--touch-min)', padding: 0, justifyContent: 'center' }}
        >
          <Arrow dir="next" />
        </button>
      </div>

      {custom ? (
        <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{periodLabel(value)}</span>
      ) : null}
    </div>
  );
}
