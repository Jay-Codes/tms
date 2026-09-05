'use client';

/**
 * Reports (SPEC §5.9, FLOWS flow 9). Three readings of the same ledger:
 *
 *  - Overview — what the business is, for one month: assets and occupancy,
 *    renters, contracts, and the month's money set out as an account with an
 *    accountant's double rule under the total.
 *  - Payment status — one row per renter with a live contract, filterable,
 *    exportable as CSV.
 *  - Collections — expected against collected over a date range.
 *
 * Every figure is computed by the backend; this screen only lays it out.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../../components/FormBits';
import {
  CollectionsChart,
  PaymentStatusStampCell,
  SectionHead,
  StatTile,
  TabBar,
  TileRow,
  bucketLabel,
  type TabDef,
} from '../../../components/ReportBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  propertiesApi,
  reportsApi,
  type CollectionsGroup,
  type CollectionsReport,
  type PaymentStatusRow,
  type PaymentStatusValue,
  type Property,
  type ReportSummary,
} from '../../../lib/api';
import { fmtDate, fmtDateTime, fmtTZS, monthStartISO, todayISO } from '../../../lib/format';

type TabId = 'overview' | 'payment_status' | 'collections';

const TABS: readonly TabDef<TabId>[] = [
  { value: 'overview', label: 'Overview' },
  { value: 'payment_status', label: 'Payment status' },
  { value: 'collections', label: 'Collections' },
];

const STATUS_OPTIONS: { value: PaymentStatusValue | ''; label: string }[] = [
  { value: '', label: 'All statuses' },
  { value: 'overdue', label: 'Overdue' },
  { value: 'partial', label: 'Part paid' },
  { value: 'pending', label: 'Pending' },
  { value: 'paid', label: 'Paid' },
];

const asError = (e: unknown) => (e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));

/** Current month as `YYYY-MM` — the default for the month picker. */
function currentMonth(): string {
  return monthStartISO().slice(0, 7);
}

/** `YYYY-MM-DD` six months back, the default left edge of the collections range. */
function sixMonthsAgoISO(): string {
  const d = new Date();
  d.setDate(1);
  d.setMonth(d.getMonth() - 5);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-01`;
}

/** The org's properties, for the payment-status property filter. Silent on failure. */
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

/* -------------------------------- overview -------------------------------- */

function OverviewTab() {
  const [month, setMonth] = useState(currentMonth());
  const [data, setData] = useState<ReportSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    setError(null);
    reportsApi
      .summary(month, ac.signal)
      .then((r) => setData(r))
      .catch((e) => {
        if (ac.signal.aborted) return;
        setError(asError(e));
        setData(null);
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false);
      });
    return () => ac.abort();
  }, [month]);

  const a = data?.assets;
  const p = data?.period;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <Field id="period" label="Month" hint="Expected and collected are counted inside this month.">
          <input
            id="period"
            className="input"
            type="month"
            value={month}
            max={currentMonth()}
            onChange={(e) => setMonth(e.target.value || currentMonth())}
            style={{ minWidth: 200 }}
          />
        </Field>
        {loading ? <span style={{ color: 'var(--ink-soft)', paddingBottom: 'var(--sp-3)' }}>Loading…</span> : null}
      </div>

      <ProblemNote error={error} />

      {a && data ? (
        <>
          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <SectionHead icon="solar:buildings-2-linear" title="Assets" />
            <TileRow>
              <StatTile label="Properties" value={a.properties} />
              <StatTile label="Units" value={a.units} sub={`${a.vacant} vacant · ${a.maintenance} maintenance`} />
              <StatTile
                label="Occupancy"
                value={`${Math.round((a.occupancy_rate ?? 0) * 100)}%`}
                sub={`${a.occupied} of ${a.units} occupied`}
              />
              <StatTile label="Active renters" value={data.renters.active} sub="Renters on a live contract" />
            </TileRow>
          </section>

          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <SectionHead icon="solar:document-text-linear" title="Contracts" />
            <TileRow>
              <StatTile label="Active" value={data.contracts.active} />
              <StatTile label="Expiring" value={data.contracts.expiring} sub="Ending within 30 days" />
              <StatTile
                label="Awaiting signature"
                value={data.contracts.pending_signature}
                sub={
                  data.contracts.pending_signature > 0 ? (
                    <Link href="/contracts?status=pending_signature">Open contracts</Link>
                  ) : undefined
                }
              />
            </TileRow>
          </section>

          {p ? (
            <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
              <SectionHead
                icon="solar:wallet-money-linear"
                title="Money this period"
                aside={
                  <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {fmtDate(p.from)} — {fmtDate(p.to)}
                  </span>
                }
              />
              <table className="ledger" style={{ maxWidth: 560 }}>
                <tbody>
                  <tr>
                    <td>Expected</td>
                    <td className="num">{fmtTZS(p.expected)}</td>
                  </tr>
                  <tr>
                    <td>Collected</td>
                    <td className="num" style={{ color: 'var(--stamp-paid)' }}>
                      {fmtTZS(p.collected)}
                    </td>
                  </tr>
                  <tr className="total">
                    <td>Outstanding</td>
                    <td className="num" style={{ color: p.outstanding > 0 ? 'var(--stamp-overdue)' : 'var(--ink)' }}>
                      {fmtTZS(p.outstanding)}
                    </td>
                  </tr>
                </tbody>
              </table>
              <TileRow>
                <StatTile
                  label="Overdue payments"
                  value={p.overdue_count}
                  tone={p.overdue_count > 0 ? 'overdue' : undefined}
                  sub={p.overdue_count > 0 ? <Link href="/payments?tab=overdue">Chase them</Link> : 'Nothing late.'}
                />
                <StatTile
                  label="Overdue amount"
                  value={fmtTZS(p.overdue_amount)}
                  tone={p.overdue_amount > 0 ? 'overdue' : undefined}
                />
              </TileRow>
            </section>
          ) : null}

          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <SectionHead
              icon="solar:home-smile-linear"
              title="Vacant units"
              aside={
                <Link href="/units?status=vacant" className="btn btn-quiet" style={{ minHeight: 36 }}>
                  Vacancy board
                </Link>
              }
            />
            <table className="ledger">
              <thead>
                <tr>
                  <th>Unit</th>
                  <th>Property</th>
                  <th className="num">Days vacant</th>
                </tr>
              </thead>
              <tbody>
                {(data.vacant_units ?? []).length === 0 ? (
                  <tr>
                    <td colSpan={3} style={{ color: 'var(--ink-soft)' }}>
                      Every unit is spoken for.
                    </td>
                  </tr>
                ) : (
                  data.vacant_units.map((v) => (
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
          </section>
        </>
      ) : null}
    </div>
  );
}

/* ----------------------------- payment status ----------------------------- */

function PaymentStatusTab() {
  const properties = useProperties();
  const [filters, setFilters] = useState<{ status: PaymentStatusValue | ''; property_id: string }>({
    status: '',
    property_id: '',
  });
  const [items, setItems] = useState<PaymentStatusRow[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    setError(null);
    reportsApi
      .paymentStatus(
        { status: filters.status || undefined, property_id: filters.property_id || undefined },
        ac.signal,
      )
      .then((r) => setItems(r.items ?? []))
      .catch((e) => {
        if (ac.signal.aborted) return;
        setError(asError(e));
        setItems(null);
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false);
      });
    return () => ac.abort();
  }, [filters]);

  const rows = items ?? [];
  const outstanding = rows.reduce((t, r) => t + (r.outstanding ?? 0), 0);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <Field id="status" label="Status">
          <select
            id="status"
            className="input"
            value={filters.status}
            onChange={(e) => setFilters((f) => ({ ...f, status: e.target.value as PaymentStatusValue | '' }))}
            style={{ minWidth: 180 }}
          >
            {STATUS_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </Field>
        <Field id="property_id" label="Property">
          <select
            id="property_id"
            className="input"
            value={filters.property_id}
            onChange={(e) => setFilters((f) => ({ ...f, property_id: e.target.value }))}
            style={{ minWidth: 200 }}
          >
            <option value="">All properties</option>
            {properties.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
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
            status: filters.status || undefined,
            property_id: filters.property_id || undefined,
          })}
          target="_blank"
          rel="noreferrer"
        >
          <Icon icon="solar:download-minimalistic-linear" width={18} /> Export CSV
        </a>
      </div>

      <ProblemNote error={error} />

      {rows.length > 0 ? (
        <p style={{ color: 'var(--ink-soft)' }}>
          {rows.length} renter{rows.length === 1 ? '' : 's'} ·{' '}
          <strong style={{ color: 'var(--ink)' }}>{fmtTZS(outstanding)}</strong> outstanding
        </p>
      ) : null}

      <table className="ledger">
        <thead>
          <tr>
            <th>Renter</th>
            <th>Unit</th>
            <th>Status</th>
            <th>Next due</th>
            <th className="num">Outstanding</th>
            <th className="num">Overdue</th>
            <th>Last payment</th>
          </tr>
        </thead>
        <tbody>
          {loading ? (
            <tr>
              <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                {error ? 'Nothing to show.' : 'No renter matches these filters.'}
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
                    <span className="pencil">nothing due</span>
                  )}
                </td>
                <td className="num">{fmtTZS(r.outstanding)}</td>
                <td className="num" style={{ color: r.overdue_amount > 0 ? 'var(--stamp-overdue)' : undefined }}>
                  {fmtTZS(r.overdue_amount)}
                </td>
                <td style={{ color: 'var(--ink-soft)' }}>
                  {r.last_payment_at ? fmtDateTime(r.last_payment_at) : <span className="pencil">never</span>}
                </td>
              </tr>
            ))
          )}
          {rows.length > 0 ? (
            <tr className="total">
              <td colSpan={4}>Total outstanding</td>
              <td className="num">{fmtTZS(outstanding)}</td>
              <td className="num">{fmtTZS(rows.reduce((t, r) => t + (r.overdue_amount ?? 0), 0))}</td>
              <td />
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}

/* ------------------------------- collections ------------------------------ */

function CollectionsTab() {
  const [form, setForm] = useState<{ from: string; to: string; group: CollectionsGroup }>({
    from: sixMonthsAgoISO(),
    to: todayISO(),
    group: 'month',
  });
  const [applied, setApplied] = useState(form);
  const [data, setData] = useState<CollectionsReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback((q: typeof form, signal: AbortSignal) => {
    setLoading(true);
    setError(null);
    reportsApi
      .collections(q, signal)
      .then((r) => setData(r))
      .catch((e) => {
        if (signal.aborted) return;
        setError(asError(e));
        setData(null);
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false);
      });
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    load(applied, ac.signal);
    return () => ac.abort();
  }, [applied, load]);

  const buckets = data?.buckets ?? [];

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(form);
        }}
        style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-end', flexWrap: 'wrap' }}
      >
        <Field id="from" label="From">
          <input
            id="from"
            className="input"
            type="date"
            value={form.from}
            onChange={(e) => setForm((f) => ({ ...f, from: e.target.value }))}
          />
        </Field>
        <Field id="to" label="To">
          <input
            id="to"
            className="input"
            type="date"
            value={form.to}
            onChange={(e) => setForm((f) => ({ ...f, to: e.target.value }))}
          />
        </Field>
        <Field id="group" label="Group by">
          <select
            id="group"
            className="input"
            value={form.group}
            onChange={(e) => setForm((f) => ({ ...f, group: e.target.value as CollectionsGroup }))}
            style={{ minWidth: 140 }}
          >
            <option value="day">Day</option>
            <option value="week">Week</option>
            <option value="month">Month</option>
          </select>
        </Field>
        <button type="submit" className="btn btn-secondary" disabled={loading}>
          {loading ? 'Loading…' : 'Apply'}
        </button>
      </form>

      <ProblemNote error={error} />

      {data ? (
        <>
          <TileRow>
            <StatTile label="Expected" value={fmtTZS(data.totals.expected)} />
            <StatTile label="Collected" value={fmtTZS(data.totals.collected)} tone="paid" />
            <StatTile
              label="Shortfall"
              value={fmtTZS(Math.max(0, data.totals.expected - data.totals.collected))}
              tone={data.totals.collected < data.totals.expected ? 'overdue' : undefined}
              sub="Expected less collected, for the range shown."
            />
          </TileRow>

          <CollectionsChart buckets={buckets} group={applied.group} />

          <table className="ledger">
            <thead>
              <tr>
                <th>Period</th>
                <th className="num">Expected</th>
                <th className="num">Collected</th>
                <th className="num">Difference</th>
              </tr>
            </thead>
            <tbody>
              {buckets.length === 0 ? (
                <tr>
                  <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                    Nothing was due or paid in this range.
                  </td>
                </tr>
              ) : (
                buckets.map((b) => {
                  const diff = b.collected - b.expected;
                  return (
                    <tr key={b.start}>
                      <td>{bucketLabel(b.start, applied.group)}</td>
                      <td className="num">{fmtTZS(b.expected)}</td>
                      <td className="num">{fmtTZS(b.collected)}</td>
                      <td className="num" style={{ color: diff < 0 ? 'var(--stamp-overdue)' : 'var(--stamp-paid)' }}>
                        {fmtTZS(diff)}
                      </td>
                    </tr>
                  );
                })
              )}
              {buckets.length > 0 ? (
                <tr className="total">
                  <td>Total</td>
                  <td className="num">{fmtTZS(data.totals.expected)}</td>
                  <td className="num">{fmtTZS(data.totals.collected)}</td>
                  <td className="num">{fmtTZS(data.totals.collected - data.totals.expected)}</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </>
      ) : null}
    </div>
  );
}

/* --------------------------------- page ---------------------------------- */

function ReportsBody() {
  const [tab, setTab] = useState<TabId>('overview');

  return (
    <>
      <PageHead
        title="Reports"
        lead="What you own, who is in it, and what has actually been paid."
      />

      <TabBar tabs={TABS} value={tab} onChange={setTab} />

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)' }}>
        {tab === 'overview' ? <OverviewTab /> : null}
        {tab === 'payment_status' ? <PaymentStatusTab /> : null}
        {tab === 'collections' ? <CollectionsTab /> : null}
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
