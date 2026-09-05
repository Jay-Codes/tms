'use client';

/**
 * Reports (SPEC §5.9, FLOWS flow 9; reworked in PLAN2 phase 11).
 *
 * One window governs the whole screen. The `PeriodPicker` at the top emits the
 * half-open range `[from, to)` the API takes, the property filter beside it
 * narrows the same window to one building, and every tab below re-reads
 * against that slice — so no two numbers on this page can be answering
 * different questions. The window is remembered between visits; nothing else
 * is kept in the browser.
 *
 *  - Overview — the six figures that describe the period, each against the
 *    period before it.
 *  - Revenue — collected against expected over time, what it cost, what was
 *    left, and the same window cut by property.
 *  - Expenses — where the money went, by category.
 *  - Occupancy — how full the buildings were.
 *  - Payment status — one row per renter, exportable as CSV.
 *  - Collections — expected against collected, bucket by bucket.
 *
 * Every figure is computed by the backend. This screen lays it out and, in the
 * charts, scales it to pixels — it never sums.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  BarChart,
  CHART_ROLES,
  LineAreaChart,
  PeriodPicker,
  RowBar,
  Sparkline,
  StatTile,
  TableScroll,
  bucketHeading,
  bucketTick,
  compactNumber,
  pctLabel,
  periodLabel,
  useT,
  type PeriodValue,
  type Translator,
} from '@tms/ui';
import { Field, ProblemNote } from '../../../components/FormBits';
import { PaymentStatusStampCell, SectionHead, TabBar, TileRow, type TabDef } from '../../../components/ReportBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  propertiesApi,
  reportsApi,
  expensesApi,
  type CollectionsGroup,
  type CollectionsReport,
  type ExpenseSummary,
  type OccupancyReport,
  type PaymentStatusRow,
  type PaymentStatusValue,
  type Property,
  type ReportBucket,
  type ReportSummary,
  type RevenueByPropertyReport,
  type RevenueReport,
} from '../../../lib/api';
import { fmtDate, fmtDateTime, fmtTZS } from '../../../lib/format';
import { loadReportPeriod, periodQuery, saveReportPeriod } from '../../../lib/reportPeriod';

type TabId = 'overview' | 'revenue' | 'expenses' | 'occupancy' | 'payment_status' | 'collections';

/** Tab ids in order; the strip labels them through `reports.tab.*`. */
const TAB_IDS: readonly TabId[] = ['overview', 'revenue', 'expenses', 'occupancy', 'payment_status', 'collections'];

const STATUS_OPTIONS: { value: PaymentStatusValue | ''; key: string }[] = [
  { value: '', key: 'reports.status.all' },
  { value: 'overdue', key: 'reports.status.overdue' },
  { value: 'partial', key: 'reports.status.partial' },
  { value: 'pending', key: 'reports.status.pending' },
  { value: 'paid', key: 'reports.status.paid' },
];

/** `month` → the word a chart title puts after "by". */
const bucketWord = (t: Translator, bucket: ReportBucket | CollectionsGroup) => t(`reports.bucket.${bucket}`);

const asError = (e: unknown) => (e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));

/** Money on an axis: the currency is said once, the figures stay short. */
const money = (v: number) => `TZS ${compactNumber(v)}`;

/**
 * `occupancy_pct` comes back as a percentage (83.3), while `collection_rate`
 * and the summary's `occupancy_rate` are fractions (0.833). `pctLabel` takes
 * fractions, so the percentage is divided here — once, named, rather than in
 * four call sites.
 */
const frac = (pct: number | null | undefined): number | null =>
  pct === null || pct === undefined || !Number.isFinite(pct) ? null : pct / 100;

interface Scope {
  period: PeriodValue;
  propertyId: string;
  /** What the tiles compare against, spelled out: "vs previous quarter". */
  previousLabel: string;
}

/* ------------------------------ data plumbing ----------------------------- */

interface Async<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
}

/**
 * One read, aborted when the window changes. The previous render is kept while
 * a new one loads (the frame does not flash) — the tab only dims.
 */
function useRead<T>(load: (signal: AbortSignal) => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ data: null, error: null, loading: true });

  useEffect(() => {
    const ac = new AbortController();
    setState((s) => ({ ...s, loading: true, error: null }));
    load(ac.signal)
      .then((data) => {
        if (!ac.signal.aborted) setState({ data, error: null, loading: false });
      })
      .catch((e) => {
        if (ac.signal.aborted) return;
        setState({ data: null, error: asError(e), loading: false });
      });
    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}

/** The org's properties, for the filter. Silent on failure. */
function useProperties(): Property[] {
  const [items, setItems] = useState<Property[]>([]);
  useEffect(() => {
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => setItems(r.items ?? []))
      .catch(() => setItems([]));
    return () => ac.abort();
  }, []);
  return items;
}

/** Dim, don't blank: a reloading tab keeps its figures until the new ones land. */
function Loading({ busy, children }: { busy: boolean; children: React.ReactNode }) {
  return (
    <div style={{ opacity: busy ? 0.55 : 1, transition: 'opacity 120ms linear' }} aria-busy={busy}>
      {children}
    </div>
  );
}

function EmptyNote({ children }: { children: React.ReactNode }) {
  return (
    <p style={{ color: 'var(--ink-soft)', border: '1px dashed var(--rule)', borderRadius: 'var(--radius-sm)', padding: 'var(--sp-5)' }}>
      {children}
    </p>
  );
}

/* -------------------------------- overview -------------------------------- */

function OverviewTab({ period, propertyId, previousLabel }: Scope) {
  const q = periodQuery(period);
  const revenue = useRead<RevenueReport>(
    (s) => reportsApi.revenue({ ...q, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );
  const occupancy = useRead<OccupancyReport>(
    (s) => reportsApi.occupancy({ ...q, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );
  const summary = useRead<ReportSummary>((s) => reportsApi.summary({ ...q }, s), [q.from, q.to, q.cadence]);
  const t9 = useT();

  const r = revenue.data;
  const t = r?.totals;
  const change = r?.change_pct ?? {};
  const spark = (r?.buckets ?? []).map((b) => b.collected);
  const nothing = t ? !t.expected && !t.collected && !t.expenses : false;
  const a = summary.data?.assets;

  return (
    <Loading busy={revenue.loading || occupancy.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
        <ProblemNote error={revenue.error ?? occupancy.error} />

        {nothing ? <EmptyNote>{t9('reports.empty.no_activity')}</EmptyNote> : null}

        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <SectionHead icon="solar:wallet-money-linear" title={t9('reports.overview.this_period')} />
          <TileRow min={220}>
            <StatTile
              label={t9('reports.tile.collected')}
              value={fmtTZS(t?.collected ?? null)}
              change={change.collected ?? null}
              changeLabelText={previousLabel}
              goodDirection="up"
              tone="paid"
              trend={
                spark.length > 1 ? (
                  <Sparkline
                    values={spark}
                    color={CHART_ROLES.collected}
                    ariaLabel={t9('reports.spark.collected_aria', {
                      count: spark.length,
                      bucket: bucketWord(t9, r?.bucket ?? 'day'),
                    })}
                  />
                ) : undefined
              }
            />
            <StatTile
              label={t9('reports.tile.expected')}
              value={fmtTZS(t?.expected ?? null)}
              change={change.expected ?? null}
              changeLabelText={previousLabel}
              goodDirection="none"
              sub={t9('reports.tile.expected_sub')}
            />
            <StatTile
              label={t9('reports.tile.expenses')}
              value={fmtTZS(t?.expenses ?? null)}
              change={change.expenses ?? null}
              changeLabelText={previousLabel}
              goodDirection="down"
            />
            <StatTile
              label={t9('reports.tile.net')}
              value={fmtTZS(t?.net ?? null)}
              change={change.net ?? null}
              changeLabelText={previousLabel}
              goodDirection="up"
              tone={t && t.net < 0 ? 'overdue' : undefined}
              sub={t9('reports.tile.net_sub')}
            />
            <StatTile
              label={t9('reports.tile.collection_rate')}
              value={pctLabel(r?.collection_rate ?? null)}
              sub={
                t
                  ? t9('reports.tile.collection_rate_sub', {
                      collected: fmtTZS(t.collected),
                      expected: fmtTZS(t.expected),
                    })
                  : undefined
              }
            />
            <StatTile
              label={t9('reports.tile.occupancy')}
              value={pctLabel(occupancy.data ? frac(occupancy.data.current.occupancy_pct) : (a?.occupancy_rate ?? null))}
              sub={
                occupancy.data
                  ? t9('reports.tile.occupancy_sub', {
                      occupied: occupancy.data.current.units_occupied,
                      total: occupancy.data.current.units_total,
                    })
                  : a
                    ? t9('reports.tile.occupancy_sub', { occupied: a.occupied, total: a.units })
                    : undefined
              }
            />
          </TileRow>
        </section>

        {summary.data && a ? (
          <>
            <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
              <SectionHead icon="solar:buildings-2-linear" title={t9('reports.overview.assets')} />
              <TileRow>
                <StatTile label={t9('reports.tile.properties')} value={a.properties} />
                <StatTile
                  label={t9('reports.tile.units')}
                  value={a.units}
                  sub={t9('reports.tile.units_sub', { vacant: a.vacant, maintenance: a.maintenance })}
                />
                <StatTile
                  label={t9('reports.tile.active_renters')}
                  value={summary.data.renters.active}
                  sub={t9('reports.tile.active_renters_sub')}
                />
                <StatTile
                  label={t9('reports.tile.contracts')}
                  value={summary.data.contracts.active}
                  sub={t9('reports.tile.contracts_sub', {
                    expiring: summary.data.contracts.expiring,
                    pending: summary.data.contracts.pending_signature,
                  })}
                />
              </TileRow>
            </section>

            <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
              <SectionHead
                icon="solar:home-smile-linear"
                title={t9('reports.vacant.title')}
                aside={
                  <Link href="/units?status=vacant" className="btn btn-quiet" style={{ minHeight: 36 }}>
                    {t9('reports.vacant.board')}
                  </Link>
                }
              />
              <TableScroll label={t9('reports.vacant.title')}>
                <table className="ledger">
                  <thead>
                    <tr>
                      <th>{t9('common.unit')}</th>
                      <th>{t9('common.property')}</th>
                      <th className="num">{t9('reports.col.days_vacant')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(summary.data.vacant_units ?? []).length === 0 ? (
                      <tr>
                        <td colSpan={3} style={{ color: 'var(--ink-soft)' }}>
                          {t9('reports.vacant.none')}
                        </td>
                      </tr>
                    ) : (
                      summary.data.vacant_units.map((v) => (
                        <tr key={v.unit_id}>
                          <td style={{ fontWeight: 500 }}>
                            <Link href={`/units/${v.unit_id}`}>{v.name}</Link>
                          </td>
                          <td style={{ color: 'var(--ink-soft)' }}>{v.property_name}</td>
                          <td className="num">{v.days_vacant}</td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </TableScroll>
            </section>
          </>
        ) : null}
      </div>
    </Loading>
  );
}

/* -------------------------------- revenue --------------------------------- */

function RevenueTab({ period, propertyId, previousLabel }: Scope) {
  const q = periodQuery(period);
  const t9 = useT();
  const [byProperty, setByProperty] = useState(false);

  const revenue = useRead<RevenueReport>(
    (s) => reportsApi.revenue({ ...q, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );
  // Only read the per-property cut when it is on screen — the toggle is a
  // different question, not a different rendering of the same answer.
  const groups = useRead<RevenueByPropertyReport | null>(
    (s) => (byProperty ? reportsApi.revenueByProperty({ ...q }, s) : Promise.resolve(null)),
    [q.from, q.to, q.cadence, byProperty],
  );

  const r = revenue.data;
  const bucket: ReportBucket = r?.bucket ?? 'month';
  const buckets = r?.buckets ?? [];
  const labels = buckets.map((b) => bucketTick(b.start, bucket));
  const headings = buckets.map((b) => bucketHeading(b.start, bucket));
  const t = r?.totals;
  const change = r?.change_pct ?? {};
  const netMax = Math.max(1, ...(groups.data?.groups ?? []).map((g) => Math.abs(g.net)));

  return (
    <Loading busy={revenue.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
        <ProblemNote error={revenue.error} />

        <TileRow min={200}>
          <StatTile
            label={t9('reports.tile.collected')}
            value={fmtTZS(t?.collected ?? null)}
            change={change.collected ?? null}
            changeLabelText={previousLabel}
            tone="paid"
          />
          <StatTile label={t9('reports.tile.expected')} value={fmtTZS(t?.expected ?? null)} goodDirection="none" />
          <StatTile
            label={t9('reports.tile.expenses')}
            value={fmtTZS(t?.expenses ?? null)}
            change={change.expenses ?? null}
            changeLabelText={previousLabel}
            goodDirection="down"
          />
          <StatTile
            label={t9('reports.tile.net')}
            value={fmtTZS(t?.net ?? null)}
            change={change.net ?? null}
            changeLabelText={previousLabel}
            tone={t && t.net < 0 ? 'overdue' : undefined}
          />
        </TileRow>

        <LineAreaChart
          title={t9('reports.chart.revenue.title', { bucket: bucketWord(t9, bucket) })}
          ariaLabel={t9('reports.chart.revenue.aria')}
          labels={labels}
          headings={headings}
          formatValue={fmtTZS}
          formatTick={money}
          empty={t9('reports.empty.no_activity')}
          series={[
            {
              id: 'collected',
              label: t9('reports.series.collected'),
              color: CHART_ROLES.collected,
              values: buckets.map((b) => b.collected),
              area: true,
            },
            {
              id: 'expected',
              label: t9('reports.series.expected'),
              color: CHART_ROLES.expected,
              values: buckets.map((b) => b.expected),
              dashed: true,
            },
            { id: 'net', label: t9('reports.series.net'), color: CHART_ROLES.net, values: buckets.map((b) => b.net) },
          ]}
        />

        <BarChart
          title={t9('reports.chart.expenses.title', { bucket: bucketWord(t9, bucket) })}
          ariaLabel={t9('reports.chart.expenses.aria')}
          labels={labels}
          headings={headings}
          height={200}
          formatValue={fmtTZS}
          formatTick={money}
          empty={t9('reports.empty.no_activity')}
          series={[
            {
              id: 'expenses',
              label: t9('reports.series.expenses'),
              color: CHART_ROLES.expenses,
              values: buckets.map((b) => b.expenses),
            },
          ]}
        />

        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <SectionHead
            icon="solar:chart-2-linear"
            title={byProperty ? t9('reports.revenue.by_property') : t9('reports.revenue.by_period')}
            aside={
              <div className="segmented" role="group" aria-label={t9('reports.revenue.breakdown_aria')}>
                <button type="button" aria-pressed={!byProperty} onClick={() => setByProperty(false)}>
                  {t9('reports.revenue.by_period')}
                </button>
                <button type="button" aria-pressed={byProperty} onClick={() => setByProperty(true)}>
                  {t9('reports.revenue.by_property')}
                </button>
              </div>
            }
          />

          {byProperty ? (
            <>
              <ProblemNote error={groups.error} />
              <TableScroll label={t9('reports.revenue.table_by_property')}>
                <table className="ledger">
                  <thead>
                    <tr>
                      <th>{t9('common.property')}</th>
                      <th className="num">{t9('reports.col.expected')}</th>
                      <th className="num">{t9('reports.col.collected')}</th>
                      <th className="num">{t9('reports.col.expenses')}</th>
                      <th className="num">{t9('reports.col.net')}</th>
                      <th>{t9('reports.col.net_share')}</th>
                      <th className="num">{t9('reports.col.collected')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(groups.data?.groups ?? []).length === 0 ? (
                      <tr>
                        <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                          {groups.loading ? t9('common.loading') : t9('reports.empty.no_activity')}
                        </td>
                      </tr>
                    ) : (
                      (groups.data?.groups ?? []).map((g) => (
                        <tr key={g.id}>
                          <td style={{ fontWeight: 500 }}>
                            <Link href={`/properties/${g.id}`}>{g.name}</Link>
                          </td>
                          <td className="num">{fmtTZS(g.expected)}</td>
                          <td className="num">{fmtTZS(g.collected)}</td>
                          <td className="num">{fmtTZS(g.expenses)}</td>
                          <td className="num" style={{ color: g.net < 0 ? 'var(--stamp-overdue)' : undefined }}>
                            {fmtTZS(g.net)}
                          </td>
                          <td>
                            <RowBar
                              value={g.net}
                              max={netMax}
                              color={CHART_ROLES.net}
                              ariaLabel={t9('reports.row_bar.net_aria', { name: g.name, amount: fmtTZS(g.net) })}
                            />
                          </td>
                          <td className="num">{pctLabel(g.collection_rate)}</td>
                        </tr>
                      ))
                    )}
                    {groups.data && groups.data.groups.length > 0 ? (
                      <tr className="total">
                        <td>{t9('common.total')}</td>
                        <td className="num">{fmtTZS(groups.data.totals.expected)}</td>
                        <td className="num">{fmtTZS(groups.data.totals.collected)}</td>
                        <td className="num">{fmtTZS(groups.data.totals.expenses)}</td>
                        <td className="num">{fmtTZS(groups.data.totals.net)}</td>
                        <td />
                        <td />
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </TableScroll>
            </>
          ) : (
            <TableScroll label={t9('reports.revenue.table_by_period')}>
              <table className="ledger">
                <thead>
                  <tr>
                    <th>{t9('reports.col.period')}</th>
                    <th className="num">{t9('reports.col.expected')}</th>
                    <th className="num">{t9('reports.col.collected')}</th>
                    <th className="num">{t9('reports.col.expenses')}</th>
                    <th className="num">{t9('reports.col.net')}</th>
                  </tr>
                </thead>
                <tbody>
                  {buckets.length === 0 ? (
                    <tr>
                      <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                        {t9('reports.empty.no_activity')}
                      </td>
                    </tr>
                  ) : (
                    buckets.map((b, i) => (
                      <tr key={b.start}>
                        <td>{headings[i]}</td>
                        <td className="num">{fmtTZS(b.expected)}</td>
                        <td className="num">{fmtTZS(b.collected)}</td>
                        <td className="num">{fmtTZS(b.expenses)}</td>
                        <td className="num" style={{ color: b.net < 0 ? 'var(--stamp-overdue)' : undefined }}>
                          {fmtTZS(b.net)}
                        </td>
                      </tr>
                    ))
                  )}
                  {t && buckets.length > 0 ? (
                    <tr className="total">
                      <td>{t9('common.total')}</td>
                      <td className="num">{fmtTZS(t.expected)}</td>
                      <td className="num">{fmtTZS(t.collected)}</td>
                      <td className="num">{fmtTZS(t.expenses)}</td>
                      <td className="num">{fmtTZS(t.net)}</td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </TableScroll>
          )}
        </section>
      </div>
    </Loading>
  );
}

/* -------------------------------- expenses -------------------------------- */

function ExpensesTab({ period, propertyId, previousLabel }: Scope) {
  const q = periodQuery(period);
  const t = useT();
  const summary = useRead<ExpenseSummary>(
    (s) => expensesApi.summary({ ...q, group_by: 'category', property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );
  const revenue = useRead<RevenueReport>(
    (s) => reportsApi.revenue({ ...q, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );

  const bucket: ReportBucket = revenue.data?.bucket ?? 'month';
  const buckets = revenue.data?.buckets ?? [];
  // Sorted big-to-small: a breakdown is read as a ranking, so the order is the
  // ranking rather than whatever order the categories were created in.
  const groups = useMemo(
    () => [...(summary.data?.groups ?? [])].sort((a, b) => b.amount - a.amount),
    [summary.data],
  );
  const total = summary.data?.total.amount ?? 0;
  const max = Math.max(1, ...groups.map((g) => g.amount));

  return (
    <Loading busy={summary.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
        <ProblemNote error={summary.error} />

        <TileRow min={220}>
          <StatTile
            label={t('reports.expenses.spent_this_period')}
            value={fmtTZS(total)}
            change={summary.data?.change_pct ?? null}
            changeLabelText={previousLabel}
            goodDirection="down"
            sub={
              summary.data
                ? t('reports.expenses.spent_sub', {
                    count: t.n('reports.expenses.count', summary.data.total.count),
                    amount: fmtTZS(summary.data.previous_total?.amount ?? 0),
                  })
                : undefined
            }
          />
          <StatTile
            label={t('reports.expenses.largest_category')}
            value={groups[0]?.name ?? '—'}
            sub={groups[0] ? fmtTZS(groups[0].amount) : t('reports.expenses.nothing_recorded')}
          />
        </TileRow>

        <BarChart
          title={t('reports.chart.expenses.title', { bucket: bucketWord(t, bucket) })}
          ariaLabel={t('reports.chart.expenses.aria')}
          labels={buckets.map((b) => bucketTick(b.start, bucket))}
          headings={buckets.map((b) => bucketHeading(b.start, bucket))}
          height={200}
          formatValue={fmtTZS}
          formatTick={money}
          empty={t('reports.expenses.chart_empty')}
          series={[
            {
              id: 'expenses',
              label: t('reports.series.expenses'),
              color: CHART_ROLES.expenses,
              values: buckets.map((b) => b.expenses),
            },
          ]}
        />

        {/*
          A stack by category *over time* would need a category × bucket cut and
          `/expenses/summary` gives the window's categories only — so the
          breakdown is drawn for the window as a whole and time is carried by the
          chart above it. One measure, one hue: the axis already names each
          category, so colouring them differently would be colour by rank.
        */}
        <BarChart
          title={t('reports.expenses.chart_categories_title')}
          ariaLabel={t('reports.expenses.chart_categories_aria')}
          labels={groups.slice(0, 8).map((g) => g.name)}
          height={220}
          formatValue={fmtTZS}
          formatTick={money}
          empty={t('reports.expenses.chart_empty')}
          series={[
            {
              id: 'amount',
              label: t('reports.series.spent'),
              color: CHART_ROLES.expenses,
              values: groups.slice(0, 8).map((g) => g.amount),
            },
          ]}
        />

        <TableScroll label={t('reports.expenses.table')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('reports.col.category')}</th>
                <th className="num">{t('reports.col.expenses')}</th>
                <th className="num">{t('common.amount')}</th>
                <th>{t('reports.col.share')}</th>
              </tr>
            </thead>
            <tbody>
              {groups.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                    {t('reports.empty.no_activity')}
                  </td>
                </tr>
              ) : (
                groups.map((g) => (
                  <tr key={g.id}>
                    <td style={{ fontWeight: 500 }}>{g.name}</td>
                    <td className="num">{g.count}</td>
                    <td className="num">{fmtTZS(g.amount)}</td>
                    <td>
                      <RowBar
                        value={g.amount}
                        max={max}
                        color={CHART_ROLES.expenses}
                        ariaLabel={t('reports.row_bar.aria', { name: g.name, amount: fmtTZS(g.amount) })}
                      />
                    </td>
                  </tr>
                ))
              )}
              {groups.length > 0 ? (
                <tr className="total">
                  <td>{t('common.total')}</td>
                  <td className="num">{summary.data?.total.count ?? 0}</td>
                  <td className="num">{fmtTZS(total)}</td>
                  <td />
                </tr>
              ) : null}
            </tbody>
          </table>
        </TableScroll>

        <p>
          <Link href="/expenses" className="btn btn-quiet" style={{ minHeight: 36 }}>
            {t('reports.expenses.open_ledger')}
          </Link>
        </p>
      </div>
    </Loading>
  );
}

/* ------------------------------- occupancy -------------------------------- */

function OccupancyTab({ period, propertyId }: Scope) {
  const q = periodQuery(period);
  const t = useT();
  const occ = useRead<OccupancyReport>(
    (s) => reportsApi.occupancy({ ...q, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, propertyId],
  );

  const bucket: ReportBucket = occ.data?.bucket ?? 'month';
  const buckets = occ.data?.buckets ?? [];
  const current = occ.data?.current;

  return (
    <Loading busy={occ.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
        <ProblemNote error={occ.error} />

        <TileRow min={220}>
          <StatTile label={t('reports.occupancy.now')} value={pctLabel(frac(current?.occupancy_pct))} />
          <StatTile
            label={t('reports.occupancy.units_occupied')}
            value={
              current
                ? t('reports.occupancy.of', { occupied: current.units_occupied, total: current.units_total })
                : '—'
            }
            sub={
              current
                ? t('reports.occupancy.standing_empty', { count: current.units_total - current.units_occupied })
                : undefined
            }
          />
        </TileRow>

        {/* Two measures, two charts — the percentage and the count never share
            an axis (dataviz: never a second y-scale). */}
        <LineAreaChart
          title={t('reports.occupancy.chart_title', { bucket: bucketWord(t, bucket) })}
          ariaLabel={t('reports.occupancy.chart_aria')}
          labels={buckets.map((b) => bucketTick(b.start, bucket))}
          headings={buckets.map((b) => bucketHeading(b.start, bucket))}
          formatValue={(v) => pctLabel(v, 1)}
          formatTick={(v) => pctLabel(v)}
          empty={t('reports.occupancy.chart_empty')}
          series={[
            {
              id: 'occupancy',
              label: t('reports.series.occupancy'),
              color: CHART_ROLES.occupancy,
              values: buckets.map((b) => frac(b.occupancy_pct) ?? 0),
              area: true,
            },
          ]}
        />

        <TableScroll label={t('reports.occupancy.table')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('reports.col.period')}</th>
                <th className="num">{t('reports.col.units')}</th>
                <th className="num">{t('reports.col.occupied')}</th>
                <th className="num">{t('reports.col.occupancy')}</th>
              </tr>
            </thead>
            <tbody>
              {buckets.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                    {t('reports.empty.no_activity')}
                  </td>
                </tr>
              ) : (
                buckets.map((b) => (
                  <tr key={b.start}>
                    <td>{bucketHeading(b.start, bucket)}</td>
                    <td className="num">{b.units_total}</td>
                    <td className="num">{b.units_occupied}</td>
                    <td className="num">{pctLabel(frac(b.occupancy_pct))}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </TableScroll>
      </div>
    </Loading>
  );
}

/* ----------------------------- payment status ----------------------------- */

function PaymentStatusTab({ period, propertyId }: Scope) {
  const q = periodQuery(period);
  const t = useT();
  const [status, setStatus] = useState<PaymentStatusValue | ''>('');

  const read = useRead<{ items: PaymentStatusRow[] }>(
    (s) => reportsApi.paymentStatus({ ...q, status: status || undefined, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, status, propertyId],
  );

  const rows = read.data?.items ?? [];
  const outstanding = rows.reduce((t, r) => t + (r.outstanding ?? 0), 0);

  return (
    <Loading busy={read.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <Field id="status" label={t('common.status')}>
            <select
              id="status"
              className="input"
              value={status}
              onChange={(e) => setStatus(e.target.value as PaymentStatusValue | '')}
              style={{ minWidth: 180 }}
            >
              {STATUS_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {t(o.key)}
                </option>
              ))}
            </select>
          </Field>
          {/*
            A download, not a fetch: the link opens the same-origin CSV so the
            httpOnly session cookie rides along and the browser saves the file.
          */}
          <a
            className="btn btn-secondary"
            href={reportsApi.paymentStatusCsvUrl({
              ...q,
              status: status || undefined,
              property_id: propertyId || undefined,
            })}
            target="_blank"
            rel="noreferrer"
          >
            <Icon icon="solar:download-minimalistic-linear" width={18} /> {t('common.export_csv')}
          </a>
        </div>

        <ProblemNote error={read.error} />

        {rows.length > 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>
            {t.n('reports.payment_status.renters', rows.length)} ·{' '}
            <strong style={{ color: 'var(--ink)' }}>{fmtTZS(outstanding)}</strong>{' '}
            {t('reports.payment_status.outstanding_suffix')}
          </p>
        ) : null}

        <TableScroll label={t('reports.payment_status.table')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('common.renter')}</th>
                <th>{t('common.unit')}</th>
                <th>{t('common.status')}</th>
                <th>{t('reports.col.next_due')}</th>
                <th className="num">{t('reports.col.outstanding')}</th>
                <th className="num">{t('reports.col.overdue')}</th>
                <th>{t('reports.col.last_payment')}</th>
              </tr>
            </thead>
            <tbody>
              {read.loading && rows.length === 0 ? (
                <tr>
                  <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                    {t('common.loading')}
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                    {read.error ? t('common.no_results') : t('reports.payment_status.no_match')}
                  </td>
                </tr>
              ) : (
                rows.map((r) => (
                  <tr key={`${r.contract_id}-${r.renter_user_id}`}>
                    <td style={{ fontWeight: 500 }}>
                      <Link href={`/renters/${r.renter_user_id}`}>{r.renter_name}</Link>
                      {r.phone ? (
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)', fontWeight: 400 }}>
                          {r.phone}
                        </div>
                      ) : null}
                    </td>
                    <td style={{ color: 'var(--ink-soft)' }}>
                      <Link href={`/contracts/${r.contract_id}`}>{r.unit_name}</Link>
                      <div style={{ fontSize: 'var(--text-xs)' }}>{r.property_name}</div>
                    </td>
                    <td>
                      <PaymentStatusStampCell status={r.status} />
                    </td>
                    <td>
                      {r.next_due_date ? (
                        <>
                          {fmtDate(r.next_due_date)}
                          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                            {fmtTZS(r.next_due_amount)}
                          </div>
                        </>
                      ) : (
                        <span className="pencil">{t('reports.payment_status.nothing_due')}</span>
                      )}
                    </td>
                    <td className="num">{fmtTZS(r.outstanding)}</td>
                    <td className="num" style={{ color: r.overdue_amount > 0 ? 'var(--stamp-overdue)' : undefined }}>
                      {fmtTZS(r.overdue_amount)}
                    </td>
                    <td style={{ color: 'var(--ink-soft)' }}>
                      {r.last_payment_at ? (
                        fmtDateTime(r.last_payment_at)
                      ) : (
                        <span className="pencil">{t('reports.payment_status.never')}</span>
                      )}
                    </td>
                  </tr>
                ))
              )}
              {rows.length > 0 ? (
                <tr className="total">
                  <td colSpan={4}>{t('reports.payment_status.total_outstanding')}</td>
                  <td className="num">{fmtTZS(outstanding)}</td>
                  <td className="num">{fmtTZS(rows.reduce((t, r) => t + (r.overdue_amount ?? 0), 0))}</td>
                  <td />
                </tr>
              ) : null}
            </tbody>
          </table>
        </TableScroll>
      </div>
    </Loading>
  );
}

/* ------------------------------- collections ------------------------------ */

function CollectionsTab({ period, propertyId }: Scope) {
  const q = periodQuery(period);
  const t = useT();
  const [group, setGroup] = useState<CollectionsGroup>('month');

  const read = useRead<CollectionsReport>(
    (s) => reportsApi.collections({ ...q, group, property_id: propertyId || undefined }, s),
    [q.from, q.to, q.cadence, group, propertyId],
  );

  const data = read.data;
  const buckets = data?.buckets ?? [];
  const labels = buckets.map((b) => bucketTick(b.start, group));
  const headings = buckets.map((b) => bucketHeading(b.start, group));

  return (
    <Loading busy={read.loading}>
      <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
        <div
          className="segmented"
          role="group"
          aria-label={t('reports.collections.group_aria')}
          style={{ alignSelf: 'start' }}
        >
          {(['day', 'week', 'month'] as CollectionsGroup[]).map((g) => (
            <button key={g} type="button" aria-pressed={group === g} onClick={() => setGroup(g)}>
              {t(`reports.group.${g}`)}
            </button>
          ))}
        </div>

        <ProblemNote error={read.error} />

        <TileRow>
          <StatTile
            label={t('reports.tile.expected')}
            value={fmtTZS(data?.totals.expected ?? null)}
            goodDirection="none"
          />
          <StatTile
            label={t('reports.tile.collected')}
            value={fmtTZS(data?.totals.collected ?? null)}
            tone="paid"
            change={data?.change_pct?.collected ?? undefined}
            changeLabelText={data?.change_pct ? t('reports.vs.period') : undefined}
          />
          <StatTile
            label={t('reports.collections.shortfall')}
            value={fmtTZS(data ? Math.max(0, data.totals.expected - data.totals.collected) : null)}
            tone={data && data.totals.collected < data.totals.expected ? 'overdue' : undefined}
            sub={t('reports.collections.shortfall_sub')}
          />
        </TileRow>

        <BarChart
          title={t('reports.collections.chart_title', { bucket: bucketWord(t, group) })}
          ariaLabel={t('reports.collections.chart_aria')}
          labels={labels}
          headings={headings}
          formatValue={fmtTZS}
          formatTick={money}
          empty={t('reports.collections.empty')}
          series={[
            {
              id: 'expected',
              label: t('reports.series.expected'),
              color: 'var(--chart-expected)',
              values: buckets.map((b) => b.expected),
            },
            {
              id: 'collected',
              label: t('reports.series.collected'),
              color: CHART_ROLES.collected,
              values: buckets.map((b) => b.collected),
            },
          ]}
        />

        <TableScroll label={t('reports.collections.table')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('reports.col.period')}</th>
                <th className="num">{t('reports.col.expected')}</th>
                <th className="num">{t('reports.col.collected')}</th>
                <th className="num">{t('reports.col.difference')}</th>
              </tr>
            </thead>
            <tbody>
              {buckets.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                    {t('reports.collections.empty')}
                  </td>
                </tr>
              ) : (
                buckets.map((b, i) => {
                  const diff = b.collected - b.expected;
                  return (
                    <tr key={b.start}>
                      <td>{headings[i]}</td>
                      <td className="num">{fmtTZS(b.expected)}</td>
                      <td className="num">{fmtTZS(b.collected)}</td>
                      <td className="num" style={{ color: diff < 0 ? 'var(--stamp-overdue)' : 'var(--stamp-paid)' }}>
                        {fmtTZS(diff)}
                      </td>
                    </tr>
                  );
                })
              )}
              {data && buckets.length > 0 ? (
                <tr className="total">
                  <td>{t('common.total')}</td>
                  <td className="num">{fmtTZS(data.totals.expected)}</td>
                  <td className="num">{fmtTZS(data.totals.collected)}</td>
                  <td className="num">{fmtTZS(data.totals.collected - data.totals.expected)}</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </TableScroll>
      </div>
    </Loading>
  );
}

/* --------------------------------- page ---------------------------------- */

/** "vs previous quarter" — the comparison is always named, never just "Δ". */
function previousLabelFor(t: Translator, p: PeriodValue): string {
  if (p.cadence === 'month') return t('reports.vs.month');
  if (p.cadence === 'quarter') return t('reports.vs.quarter');
  if (p.cadence === 'half_year') return t('reports.vs.half_year');
  if (p.cadence === 'year') return t('reports.vs.year');
  return t('reports.vs.custom');
}

function ReportsBody() {
  const t = useT();
  const [tab, setTab] = useState<TabId>('overview');
  const [period, setPeriod] = useState<PeriodValue>(loadReportPeriod);
  const [propertyId, setPropertyId] = useState('');
  const properties = useProperties();

  useEffect(() => saveReportPeriod(period), [period]);

  const tabs: TabDef<TabId>[] = useMemo(
    () => TAB_IDS.map((id) => ({ value: id, label: t(`reports.tab.${id}`) })),
    [t],
  );

  const scope: Scope = useMemo(
    () => ({ period, propertyId, previousLabel: previousLabelFor(t, period) }),
    [t, period, propertyId],
  );

  const onPeriod = useCallback((next: PeriodValue) => setPeriod(next), []);

  return (
    <>
      <PageHead title={t('reports.title')} lead={t('reports.lead')} />

      {/* One filter row above everything: it scopes every tab below, so no two
          figures on the page can be answering different questions. */}
      <div
        style={{
          display: 'flex',
          gap: 'var(--sp-4)',
          alignItems: 'center',
          flexWrap: 'wrap',
          paddingBottom: 'var(--sp-4)',
        }}
      >
        <PeriodPicker value={period} onChange={onPeriod} label={t('reports.period_label')} />
        <label style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('common.property')}</span>
          <select
            className="input"
            value={propertyId}
            onChange={(e) => setPropertyId(e.target.value)}
            style={{ width: 'auto', minWidth: 180 }}
          >
            <option value="">{t('reports.all_properties')}</option>
            {properties.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('reports.window', {
            label: periodLabel(period),
            from: fmtDate(period.from),
            to: fmtDate(period.to),
          })}
        </span>
      </div>

      <TabBar tabs={tabs} value={tab} onChange={setTab} />

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)' }}>
        {tab === 'overview' ? <OverviewTab {...scope} /> : null}
        {tab === 'revenue' ? <RevenueTab {...scope} /> : null}
        {tab === 'expenses' ? <ExpensesTab {...scope} /> : null}
        {tab === 'occupancy' ? <OccupancyTab {...scope} /> : null}
        {tab === 'payment_status' ? <PaymentStatusTab {...scope} /> : null}
        {tab === 'collections' ? <CollectionsTab {...scope} /> : null}
      </div>
    </>
  );
}

export default function ReportsPage() {
  return (
    <>
      <ReportsBody />
    </>
  );
}
