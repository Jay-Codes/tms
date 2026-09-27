'use client';

/**
 * Phase 28 — projections, break-even and ROI.
 *
 *  - `ProjectionsTab` is the Reports → Projections tab: scenario controls that
 *    re-query `POST /reports/projection` (debounced), the headline figures, a
 *    chart of cumulative cash against the investment with the break-even
 *    point, the per-property table, the monthly table and the assumptions the
 *    backend used. Saved scenarios load, save and delete by name.
 *  - `PropertyInvestment` is the three investment fields on a property page.
 *
 * Every figure is computed by the backend (CLAUDE.md: no business logic in the
 * frontend). This file sends the scenario, lays the answer out and scales it
 * to pixels — it never sums, averages or divides money.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import {
  CHART_ROLES,
  LineAreaChart,
  StatTile,
  TableScroll,
  bucketHeading,
  bucketTick,
  compactNumber,
  useT,
  type Translator,
} from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import { SectionHead, TileRow } from './ReportBits';
import {
  ApiError,
  projectionApi,
  propertiesApi,
  toApiError,
  unwrapProperty,
  type BreakEvenStatus,
  type ProjectionParams,
  type ProjectionReport,
  type ProjectionScenario,
  type Property,
  type RateSource,
} from '../lib/api';
import { fmtDate, fmtTZS } from '../lib/format';

/** How long the sliders settle before the forecast is asked again. */
const DEBOUNCE_MS = 350;

const HORIZONS = [12, 24, 36, 60, 120] as const;

const money = (v: number) => `TZS ${compactNumber(v)}`;

/** A percentage the server already rounded, printed as it came. */
const pct = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : `${v.toLocaleString('en-US', { maximumFractionDigits: 1 })}%`;

const monthTick = (m: string) => bucketTick(`${m}-01`, 'month');
/**
 * Past a year the chart skips labels to keep them apart, so the ones it keeps
 * land on arbitrary months — `Sep, Aug, Jul…` read as time running backwards.
 * Long horizons therefore label every tick with its year (`Sep 2026`).
 */
const longTick = (m: string) => bucketHeading(`${m}-01`, 'month');
const monthHeading = (m: string) => bucketHeading(`${m}-01`, 'month');

function breakEvenValue(t: Translator, status: BreakEvenStatus, month: string | null): string {
  if (month && (status === 'reached' || status === 'projected')) return monthHeading(month);
  return t(`proj.breakeven.${status}`);
}

function sourceLabel(t: Translator, s: RateSource): string {
  return t(`proj.source.${s}`);
}

/** A labelled range with its value printed beside it and an optional reset. */
function Slider({
  id,
  label,
  value,
  min,
  max,
  step = 1,
  suffix = '%',
  signed = false,
  onChange,
  onReset,
  resetLabel,
  hint,
}: {
  id: string;
  label: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  suffix?: string;
  signed?: boolean;
  onChange: (v: number) => void;
  onReset?: () => void;
  resetLabel?: string;
  hint?: string;
}) {
  const shown = `${signed && value > 0 ? '+' : ''}${value.toLocaleString('en-US', { maximumFractionDigits: 1 })}${suffix}`;
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-1)', minWidth: 0 }}>
      <label htmlFor={id} style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--sp-2)', fontSize: 'var(--text-sm)' }}>
        <span>{label}</span>
        <span className="num" style={{ fontWeight: 600 }}>
          {shown}
        </span>
      </label>
      <input
        id={id}
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        style={{ width: '100%' }}
      />
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--sp-2)', minHeight: 24 }}>
        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{hint}</span>
        {onReset ? (
          <button type="button" className="btn btn-quiet" onClick={onReset} style={{ minHeight: 24, padding: '0 var(--sp-2)', fontSize: 'var(--text-xs)' }}>
            {resetLabel}
          </button>
        ) : null}
      </div>
    </div>
  );
}

interface ScenarioState {
  horizon: number;
  rent: number;
  expense: number;
  /** null = the trailing twelve months. */
  occupancy: number | null;
  collection: number | null;
}

const BASE_SCENARIO: ScenarioState = { horizon: 24, rent: 0, expense: 0, occupancy: null, collection: null };

const toParams = (s: ScenarioState): ProjectionParams => ({
  horizon_months: s.horizon,
  rent_change_pct: s.rent,
  expense_change_pct: s.expense,
  occupancy_pct: s.occupancy,
  collection_rate_pct: s.collection,
});

const fromSaved = (s: ProjectionScenario): ScenarioState => ({
  horizon: s.horizon_months,
  rent: s.rent_change_pct,
  expense: s.expense_change_pct,
  occupancy: s.occupancy_pct,
  collection: s.collection_rate_pct,
});

/* --------------------------------------------------------- saved scenarios -- */

function SavedScenarios({ current, onLoad }: { current: ScenarioState; onLoad: (s: ScenarioState) => void }) {
  const t = useT();
  const [items, setItems] = useState<ProjectionScenario[]>([]);
  const [picked, setPicked] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await projectionApi.scenarios(signal);
      setItems(res.items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await projectionApi.saveScenario({ name: name.trim(), ...toParams(current) });
      setName('');
      setPicked(res.scenario.id);
      setNote(t('proj.saved.done'));
      await load();
    } catch (err) {
      const asErr = toApiError(err);
      setError(
        asErr.status === 409 && asErr.code === 'scenario_exists'
          ? new ApiError(409, { type: asErr.code, detail: t('proj.saved.exists') })
          : asErr,
      );
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    const s = items.find((x) => x.id === picked);
    if (!s || !window.confirm(t('proj.saved.delete_confirm', { name: s.name }))) return;
    setBusy(true);
    setError(null);
    try {
      await projectionApi.removeScenario(s.id);
      setPicked('');
      await load();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <ProblemNote error={error} />
      {note ? <Note>{note}</Note> : null}
      <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <Field id="proj_saved" label={t('proj.saved.label')}>
          <select
            id="proj_saved"
            className="input"
            value={picked}
            onChange={(e) => {
              setPicked(e.target.value);
              const s = items.find((x) => x.id === e.target.value);
              if (s) onLoad(fromSaved(s));
            }}
            style={{ minWidth: 200 }}
          >
            <option value="">{items.length ? t('proj.saved.pick') : t('proj.saved.none')}</option>
            {items.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        {picked ? (
          <button type="button" className="btn btn-quiet" onClick={() => void remove()} disabled={busy} style={{ minHeight: 40, color: 'var(--stamp-overdue)' }}>
            {t('common.delete')}
          </button>
        ) : null}
        <form onSubmit={save} style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'flex-end', flexWrap: 'wrap' }} noValidate>
          <Field id="proj_name" label={t('proj.saved.name')} error={error?.errors.name}>
            <input
              id="proj_name"
              className="input"
              value={name}
              maxLength={60}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('proj.saved.name_ph')}
              style={{ width: 220 }}
            />
          </Field>
          <button type="submit" className="btn btn-secondary" disabled={busy || !name.trim()} style={{ minHeight: 40 }}>
            <Icon icon="solar:diskette-linear" width={18} /> {t('proj.saved.save')}
          </button>
        </form>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------ the tab -- */

export function ProjectionsTab({ propertyId }: { propertyId: string }) {
  const t = useT();
  const [sc, setSc] = useState<ScenarioState>(BASE_SCENARIO);
  const [data, setData] = useState<ProjectionReport | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    const timer = window.setTimeout(() => {
      projectionApi
        .run({ ...toParams(sc), property_id: propertyId || undefined }, ac.signal)
        .then((r) => {
          if (ac.signal.aborted) return;
          setData(r);
          setError(null);
          setLoading(false);
        })
        .catch((e) => {
          if (ac.signal.aborted) return;
          setError(toApiError(e));
          setLoading(false);
        });
    }, DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      ac.abort();
    };
  }, [sc, propertyId]);

  const set = (patch: Partial<ScenarioState>) => setSc((s) => ({ ...s, ...patch }));
  const applied = data?.applied;
  const base = data?.baseline;
  const inv = data?.investment;
  const months = data?.months ?? [];
  const labels = months.map((m) => (months.length > 12 ? longTick(m.month) : monthTick(m.month)));
  const headings = months.map((m) => monthHeading(m.month));
  const beIndex =
    inv?.break_even_status === 'projected' && inv.break_even_month
      ? months.findIndex((m) => m.month === inv.break_even_month)
      : -1;
  const priced = inv ? inv.total !== null : false;

  return (
    <div style={{ opacity: loading ? 0.7 : 1, transition: 'opacity 120ms linear', display: 'grid', gap: 'var(--sp-6)' }} aria-busy={loading}>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', maxWidth: 'var(--measure)' }}>
        {t('proj.lead', { start: data ? monthHeading(data.start_month) : '…' })}
      </p>
      <ProblemNote error={error} />

      <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <SectionHead icon="solar:tuning-2-linear" title={t('proj.scenario.title')} />
        <div style={{ display: 'grid', gap: 'var(--sp-5)', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))' }}>
          <Field id="proj_horizon" label={t('proj.scenario.horizon')}>
            <select
              id="proj_horizon"
              className="input"
              value={sc.horizon}
              onChange={(e) => set({ horizon: Number(e.target.value) })}
            >
              {HORIZONS.map((h) => (
                <option key={h} value={h}>
                  {t('proj.scenario.horizon_months', { count: h })}
                </option>
              ))}
            </select>
          </Field>
          <Slider
            id="proj_rent"
            label={t('proj.scenario.rent')}
            hint={t('proj.scenario.rent_hint')}
            value={sc.rent}
            min={-50}
            max={100}
            signed
            onChange={(v) => set({ rent: v })}
            onReset={sc.rent !== 0 ? () => set({ rent: 0 }) : undefined}
            resetLabel={t('proj.scenario.reset')}
          />
          <Slider
            id="proj_occ"
            label={t('proj.scenario.occupancy')}
            hint={applied ? sourceLabel(t, sc.occupancy === null ? applied.occupancy_source : 'scenario') : undefined}
            value={sc.occupancy ?? applied?.occupancy_pct ?? 100}
            min={0}
            max={100}
            onChange={(v) => set({ occupancy: v })}
            onReset={sc.occupancy !== null ? () => set({ occupancy: null }) : undefined}
            resetLabel={t('proj.scenario.use_trailing')}
          />
          <Slider
            id="proj_coll"
            label={t('proj.scenario.collection')}
            hint={applied ? sourceLabel(t, sc.collection === null ? applied.collection_rate_source : 'scenario') : undefined}
            value={sc.collection ?? applied?.collection_rate_pct ?? 100}
            min={0}
            max={100}
            onChange={(v) => set({ collection: v })}
            onReset={sc.collection !== null ? () => set({ collection: null }) : undefined}
            resetLabel={t('proj.scenario.use_trailing')}
          />
          <Slider
            id="proj_exp"
            label={t('proj.scenario.expenses')}
            hint={t('proj.scenario.expenses_hint')}
            value={sc.expense}
            min={-50}
            max={100}
            signed
            onChange={(v) => set({ expense: v })}
            onReset={sc.expense !== 0 ? () => set({ expense: 0 }) : undefined}
            resetLabel={t('proj.scenario.reset')}
          />
        </div>
        <SavedScenarios current={sc} onLoad={setSc} />
      </section>

      {inv && !priced ? (
        <Note>
          {t('proj.no_price')}{' '}
          {propertyId ? (
            <Link href={`/properties/${propertyId}`}>{t('proj.no_price.link')}</Link>
          ) : null}
        </Note>
      ) : null}
      {inv && priced && inv.incomplete ? (
        <Note>{t('proj.incomplete', { priced: inv.priced_properties, total: data?.properties.length ?? 0 })}</Note>
      ) : null}

      <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <SectionHead icon="solar:graph-up-linear" title={t('proj.headline.title')} />
        <TileRow min={180}>
          <StatTile
            label={t('proj.tile.annual_net')}
            value={fmtTZS(inv?.projected_annual_net ?? null)}
            tone={inv && inv.projected_annual_net < 0 ? 'overdue' : undefined}
            sub={inv ? t('proj.tile.annual_net_sub', { trailing: fmtTZS(inv.trailing_annual_net) }) : undefined}
          />
          <StatTile
            label={t('proj.tile.roi')}
            value={pct(inv?.roi_projected_pct)}
            sub={inv && priced ? t('proj.tile.roi_sub', { trailing: pct(inv.roi_trailing_pct), investment: fmtTZS(inv.total) }) : t('proj.tile.needs_price')}
          />
          <StatTile
            label={t('proj.tile.yield')}
            value={pct(inv?.yield_pct)}
            sub={inv?.current_value ? t('proj.tile.yield_sub', { value: fmtTZS(inv.current_value) }) : t('proj.tile.needs_value')}
          />
          <StatTile
            label={t('proj.tile.payback')}
            value={inv?.payback_years === null || inv?.payback_years === undefined ? '—' : t('proj.years', { n: inv.payback_years })}
            sub={priced ? undefined : t('proj.tile.needs_price')}
          />
          <StatTile
            label={t('proj.tile.break_even')}
            value={inv ? breakEvenValue(t, inv.break_even_status, inv.break_even_month) : '—'}
            tone={inv?.break_even_status === 'reached' ? 'paid' : inv?.break_even_status === 'not_profitable' ? 'overdue' : undefined}
            sub={inv && priced ? t(`proj.breakeven_sub.${inv.break_even_status}`) : undefined}
          />
        </TileRow>
      </section>

      <LineAreaChart
        title={t('proj.chart.title')}
        ariaLabel={t('proj.chart.aria')}
        labels={labels}
        headings={headings}
        formatValue={fmtTZS}
        formatTick={money}
        empty={t('proj.chart.empty')}
        series={[
          {
            id: 'cumulative',
            label: t('proj.series.cumulative'),
            color: CHART_ROLES.net,
            values: months.map((m) => m.cumulative),
            area: true,
          },
          ...(inv && inv.total !== null
            ? [
                {
                  id: 'investment',
                  label: t('proj.series.investment'),
                  color: CHART_ROLES.expected,
                  values: months.map(() => inv.total),
                  dashed: true,
                },
              ]
            : []),
          // The break-even month as a one-point series: the chart draws a
          // ringed end marker on a series' last value, so a series that is
          // empty except at that month is a dot exactly there, with its own
          // legend entry and tooltip row.
          ...(beIndex >= 0
            ? [
                {
                  id: 'break_even',
                  label: t('proj.chart.break_even'),
                  color: CHART_ROLES.collected,
                  values: months.map((m, i) => (i === beIndex ? m.cumulative : null)),
                },
              ]
            : []),
        ]}
      />

      {data && data.scope === 'portfolio' && data.properties.length > 0 ? (
        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <SectionHead icon="solar:buildings-2-linear" title={t('proj.by_property.title')} />
          <TableScroll label={t('proj.by_property.title')}>
            <table className="ledger">
              <thead>
                <tr>
                  <th>{t('common.property')}</th>
                  <th className="num">{t('proj.tile.annual_net')}</th>
                  <th className="num">{t('proj.tile.roi')}</th>
                  <th className="num">{t('proj.tile.yield')}</th>
                  <th className="num">{t('proj.tile.payback')}</th>
                  <th>{t('proj.tile.break_even')}</th>
                </tr>
              </thead>
              <tbody>
                {data.properties.map((p) => (
                  <tr key={p.id}>
                    <td style={{ fontWeight: 500 }}>
                      <Link href={`/properties/${p.id}`} style={{ color: 'inherit' }}>
                        {p.name}
                      </Link>
                    </td>
                    <td className="num">{fmtTZS(p.projected_annual_net)}</td>
                    <td className="num">{pct(p.roi_projected_pct)}</td>
                    <td className="num">{pct(p.yield_pct)}</td>
                    <td className="num">{p.payback_years === null ? '—' : t('proj.years', { n: p.payback_years })}</td>
                    <td>{breakEvenValue(t, p.break_even_status, p.break_even_month)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        </section>
      ) : null}

      <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <SectionHead icon="solar:calendar-linear" title={t('proj.months.title')} />
        <TableScroll label={t('proj.months.title')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('proj.col.month')}</th>
                <th className="num">{t('proj.col.income')}</th>
                <th className="num">{t('proj.col.running')}</th>
                <th className="num">{t('proj.col.net')}</th>
                <th className="num">{t('proj.col.cumulative')}</th>
              </tr>
            </thead>
            <tbody>
              {months.map((m) => (
                <tr key={m.month} style={m.month === inv?.break_even_month ? { fontWeight: 600 } : undefined}>
                  <td>{monthHeading(m.month)}</td>
                  <td className="num">{fmtTZS(m.income)}</td>
                  <td className="num">{fmtTZS(m.running_expenses)}</td>
                  <td className="num" style={m.net < 0 ? { color: 'var(--stamp-overdue)' } : undefined}>
                    {fmtTZS(m.net)}
                  </td>
                  <td className="num">{fmtTZS(m.cumulative)}</td>
                </tr>
              ))}
            </tbody>
            {data ? (
              <tfoot>
                <tr>
                  <th>{t('proj.col.total')}</th>
                  <th className="num">{fmtTZS(data.totals.income)}</th>
                  <th className="num">{fmtTZS(data.totals.running_expenses)}</th>
                  <th className="num">{fmtTZS(data.totals.net)}</th>
                  <th />
                </tr>
              </tfoot>
            ) : null}
          </table>
        </TableScroll>
      </section>

      {base && applied ? (
        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <SectionHead icon="solar:notebook-linear" title={t('proj.baseline.title')} />
          <dl style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: 'var(--sp-2) var(--sp-4)', maxWidth: 640, margin: 0 }}>
            <dt>{t('proj.baseline.window')}</dt>
            <dd className="num" style={{ margin: 0 }}>
              {t('proj.baseline.window_value', { from: fmtDate(base.history_from), to: fmtDate(base.history_to), n: base.history_months })}
            </dd>
            <dt>{t('proj.baseline.collection')}</dt>
            <dd className="num" style={{ margin: 0 }}>
              {pct(base.collection_rate_pct)} ({fmtTZS(base.collected)} / {fmtTZS(base.expected)})
            </dd>
            <dt>{t('proj.baseline.occupancy')}</dt>
            <dd className="num" style={{ margin: 0 }}>{pct(base.occupancy_pct)}</dd>
            <dt>{t('proj.baseline.running')}</dt>
            <dd className="num" style={{ margin: 0 }}>{fmtTZS(base.running_expenses_monthly)}</dd>
            {base.categories.map((c) => (
              <FragmentRow key={c.id || c.name} label={`· ${c.name || t('proj.baseline.uncategorised')}`} value={fmtTZS(c.monthly)} />
            ))}
            <dt>{t('proj.baseline.capital')}</dt>
            <dd className="num" style={{ margin: 0 }}>{fmtTZS(base.capital_expenses)}</dd>
            <dt>{t('proj.baseline.units')}</dt>
            <dd className="num" style={{ margin: 0 }}>
              {t('proj.baseline.units_value', { total: base.units_total, let: base.units_let, open: base.units_open })}
            </dd>
            <dt>{t('proj.baseline.market_rent')}</dt>
            <dd className="num" style={{ margin: 0 }}>{fmtTZS(base.market_rent_monthly)}</dd>
            {inv ? (
              <>
                <dt>{t('proj.baseline.cash_to_date')}</dt>
                <dd className="num" style={{ margin: 0 }}>{fmtTZS(inv.cash_to_date)}</dd>
              </>
            ) : null}
          </dl>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', maxWidth: 'var(--measure)' }}>{t('proj.baseline.note')}</p>
        </section>
      ) : null}
    </div>
  );
}

function FragmentRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt style={{ color: 'var(--ink-soft)', paddingLeft: 'var(--sp-3)' }}>{label}</dt>
      <dd className="num" style={{ margin: 0, color: 'var(--ink-soft)' }}>
        {value}
      </dd>
    </>
  );
}

/* ------------------------------------------------- property settings -- */

/** Whole shillings typed with or without separators; '' is "not set". */
const parseShillings = (raw: string): number | null => {
  const digits = raw.replace(/[\s,]/g, '');
  return digits === '' ? null : Number(digits);
};

export function PropertyInvestment({ property, onSaved }: { property: Property; onSaved: (p: Property) => void }) {
  const t = useT();
  const [price, setPrice] = useState(property.purchase_price?.toString() ?? '');
  const [date, setDate] = useState(property.purchase_date ?? '');
  const [value, setValue] = useState(property.current_value?.toString() ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const res = await propertiesApi.update(property.id, {
        purchase_price: parseShillings(price),
        purchase_date: date || null,
        current_value: parseShillings(value),
      });
      onSaved(unwrapProperty(res));
      setNote(t('common.saved'));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={save} style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 720 }} noValidate>
      <h3 style={{ fontSize: 'var(--text-lg)' }}>{t('proj.invest.title')}</h3>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', maxWidth: 'var(--measure)' }}>{t('proj.invest.lead')}</p>
      <ProblemNote error={error} />
      {note ? <Note>{note}</Note> : null}
      <div style={{ display: 'grid', gap: 'var(--sp-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))' }}>
        <Field id="inv_price" label={t('proj.invest.price')} hint={t('proj.invest.price_hint')} error={error?.errors.purchase_price}>
          <input id="inv_price" className="input" inputMode="numeric" value={price} onChange={(e) => setPrice(e.target.value)} placeholder="85,000,000" />
        </Field>
        <Field id="inv_date" label={t('proj.invest.date')} hint={t('proj.invest.date_hint')} error={error?.errors.purchase_date}>
          <input id="inv_date" className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field id="inv_value" label={t('proj.invest.value')} hint={t('proj.invest.value_hint')} error={error?.errors.current_value}>
          <input id="inv_value" className="input" inputMode="numeric" value={value} onChange={(e) => setValue(e.target.value)} placeholder="120,000,000" />
        </Field>
      </div>
      <div>
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.saving') : t('common.save')}
        </button>
      </div>
    </form>
  );
}
