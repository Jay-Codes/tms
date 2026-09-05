'use client';

/**
 * Which payment periods a unit offers (`units.allowed_period_ids`).
 * Empty selection = NULL = every active org period is offered (SPEC §4).
 */

import type { PaymentPeriod } from '../lib/api';

export function PeriodPicker({
  periods,
  value,
  onChange,
  idPrefix = 'per',
}: {
  periods: PaymentPeriod[];
  /** null / [] means "all periods". */
  value: string[] | null;
  onChange: (next: string[] | null) => void;
  idPrefix?: string;
}) {
  const selected = value ?? [];
  const all = selected.length === 0;

  const toggle = (id: string) => {
    const next = selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id];
    onChange(next.length === 0 ? null : next);
  };

  return (
    <div className="field">
      <span style={{ fontSize: 'var(--text-sm)', fontWeight: 500, color: 'var(--ink-soft)' }}>
        Payment periods offered
      </span>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-3)', margin: 'var(--sp-2) 0' }}>
        {periods.length === 0 ? (
          <span className="pencil">No active periods. Add some in Settings first.</span>
        ) : (
          periods.map((p) => (
            <label
              key={p.id}
              htmlFor={`${idPrefix}-${p.id}`}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 'var(--sp-2)',
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-3)',
                border: `1px solid ${selected.includes(p.id) ? 'var(--primary)' : 'var(--rule)'}`,
                borderRadius: 'var(--radius-md)',
                background: selected.includes(p.id) ? 'var(--primary-soft)' : 'transparent',
                fontSize: 'var(--text-sm)',
                cursor: 'pointer',
              }}
            >
              <input
                id={`${idPrefix}-${p.id}`}
                type="checkbox"
                checked={selected.includes(p.id)}
                onChange={() => toggle(p.id)}
                style={{ width: 16, height: 16 }}
              />
              {p.label} <span className="num">({p.days}d)</span>
            </label>
          ))
        )}
      </div>
      <span className="hint">
        {all
          ? 'None ticked — this unit offers every active period.'
          : `Only the ${selected.length} ticked period${selected.length === 1 ? '' : 's'} will be offered.`}
      </span>
    </div>
  );
}
