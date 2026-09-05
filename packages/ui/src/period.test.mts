/**
 * Unit tests for the PeriodPicker's window maths.
 *
 * No test runner exists in this workspace, so these run on node's own:
 *   npm test -w packages/ui
 * Node ≥ 22.18 strips the types in `period.ts` on the fly.
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import {
  addDays,
  daysBetween,
  periodLabel,
  resolvePeriod,
  shiftPeriod,
  type PeriodValue,
} from './period.ts';

test('resolvePeriod: month is [1st, 1st of next month)', () => {
  assert.deepEqual(resolvePeriod('month', '2026-09-15'), {
    cadence: 'month',
    from: '2026-09-01',
    to: '2026-10-01',
  });
  // December rolls the year over.
  assert.deepEqual(resolvePeriod('month', '2026-12-31'), {
    cadence: 'month',
    from: '2026-12-01',
    to: '2027-01-01',
  });
});

test('resolvePeriod: quarter snaps to Jan/Apr/Jul/Oct', () => {
  assert.deepEqual(resolvePeriod('quarter', '2026-09-05'), {
    cadence: 'quarter',
    from: '2026-07-01',
    to: '2026-10-01',
  });
  assert.equal(resolvePeriod('quarter', '2026-01-01').from, '2026-01-01');
  assert.equal(resolvePeriod('quarter', '2026-11-30').from, '2026-10-01');
  assert.equal(resolvePeriod('quarter', '2026-11-30').to, '2027-01-01');
});

test('resolvePeriod: half year and year', () => {
  assert.deepEqual(resolvePeriod('half_year', '2026-06-30'), {
    cadence: 'half_year',
    from: '2026-01-01',
    to: '2026-07-01',
  });
  assert.deepEqual(resolvePeriod('half_year', '2026-07-01'), {
    cadence: 'half_year',
    from: '2026-07-01',
    to: '2027-01-01',
  });
  assert.deepEqual(resolvePeriod('year', '2026-02-28'), {
    cadence: 'year',
    from: '2026-01-01',
    to: '2027-01-01',
  });
});

test('windows are half-open: length matches the calendar', () => {
  assert.equal(daysBetween('2026-09-01', '2026-10-01'), 30);
  assert.equal(daysBetween('2026-02-01', '2026-03-01'), 28);
  assert.equal(daysBetween('2024-02-01', '2024-03-01'), 29, 'leap year');
  const year = resolvePeriod('year', '2026-05-05');
  assert.equal(daysBetween(year.from, year.to), 365);
});

test('shiftPeriod: steps by one unit of the cadence', () => {
  assert.deepEqual(shiftPeriod(resolvePeriod('month', '2026-01-10'), -1), {
    cadence: 'month',
    from: '2025-12-01',
    to: '2026-01-01',
  });
  assert.deepEqual(shiftPeriod(resolvePeriod('quarter', '2026-09-05'), 1), {
    cadence: 'quarter',
    from: '2026-10-01',
    to: '2027-01-01',
  });
  assert.deepEqual(shiftPeriod(resolvePeriod('half_year', '2026-09-05'), 1), {
    cadence: 'half_year',
    from: '2027-01-01',
    to: '2027-07-01',
  });
  assert.deepEqual(shiftPeriod(resolvePeriod('year', '2026-09-05'), -2), {
    cadence: 'year',
    from: '2024-01-01',
    to: '2025-01-01',
  });
});

test('shiftPeriod: a custom range slides by its own length', () => {
  const v: PeriodValue = { cadence: 'custom', from: '2026-09-06', to: '2026-12-06' }; // 91 days
  const back = shiftPeriod(v, -1);
  assert.equal(daysBetween(back.from, back.to), 91);
  assert.equal(back.to, '2026-09-06');
  const fwd = shiftPeriod(v, 1);
  assert.equal(fwd.from, '2026-12-06');
});

test('shiftPeriod round-trips', () => {
  const v = resolvePeriod('quarter', '2026-09-05');
  assert.deepEqual(shiftPeriod(shiftPeriod(v, 1), -1), v);
});

test('periodLabel prints the window a reader recognises', () => {
  assert.equal(periodLabel(resolvePeriod('month', '2026-09-15')), 'Sep 2026');
  assert.equal(periodLabel(resolvePeriod('quarter', '2026-09-15')), 'Q3 2026');
  assert.equal(periodLabel(resolvePeriod('half_year', '2026-09-15')), 'Jul–Dec 2026');
  assert.equal(periodLabel(resolvePeriod('half_year', '2026-02-15')), 'Jan–Jun 2026');
  assert.equal(periodLabel(resolvePeriod('year', '2026-09-15')), '2026');
  // Custom shows the last day inside the window, not the exclusive bound.
  assert.equal(periodLabel({ cadence: 'custom', from: '2026-09-06', to: '2026-12-06' }), '6 Sep – 5 Dec 2026');
  assert.equal(periodLabel({ cadence: 'custom', from: '2026-09-06', to: '2026-09-10' }), '6 – 9 Sep 2026');
  assert.equal(
    periodLabel({ cadence: 'custom', from: '2026-12-30', to: '2027-01-03' }),
    '30 Dec 2026 – 2 Jan 2027',
  );
});

test('addDays crosses month and year ends', () => {
  assert.equal(addDays('2026-02-28', 1), '2026-03-01');
  assert.equal(addDays('2024-02-28', 1), '2024-02-29');
  assert.equal(addDays('2026-01-01', -1), '2025-12-31');
});
