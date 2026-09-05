'use client';

/**
 * Dashboard (FLOWS flow 9). The cards a landlord sees, and the order they sit
 * in, come from `dashboard_prefs.cards` on the org branding record; an org that
 * has never customised gets the default order. Every figure on the cards is
 * read from the Phase 7 report endpoints — the frontend sums nothing.
 *
 * Each card is also a door: renter → contract → schedules → payment history.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { useReadyToCountersign } from '../../components/ContractBits';
import { CARD_LABELS, DashboardCustomize } from '../../components/DashboardCustomize';
import { ChangeMark } from '../../components/ExpenseBits';
import { StatTile, TileRow } from '../../components/ReportBits';
import { CHART_ROLES, ChangeMark as DeltaMark, Sparkline } from '@tms/ui';
import { PageHead } from '../../components/PageHead';
import { pendingLabel, usePendingLinkRequests } from '../../components/NavBadges';
import {
  brandingApi,
  expensesApi,
  propertiesApi,
  readDashboardPrefs,
  reportsApi,
  unwrapBranding,
  type DashboardCard,
  type DashboardPrefs,
  type ExpenseSummary,
  type PaymentStatusRow,
  type Property,
  type ReportSummary,
  type RevenueReport,
} from '../../lib/api';
import { useMe } from '../../lib/auth';
import { fmtTZS } from '../../lib/format';

function EmptyCard({
  icon,
  title,
  body,
  action,
}: {
  icon: string;
  title: string;
  body: string;
  action: ReactNode;
}) {
  return (
    <div className="sheet" style={{ padding: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-3)', alignContent: 'start' }}>
      <Icon icon={icon} width={24} />
      <h2 style={{ fontSize: 'var(--text-lg)' }}>{title}</h2>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{body}</p>
      <div>{action}</div>
    </div>
  );
}

/**
 * One dashboard card: the label, the figure, and the link that drills into it.
 * Flat, ruled at the top like a ledger section — no card grid (SPEC §2.0).
 */
function DashCard({
  icon,
  label,
  children,
  href,
  cta,
}: {
  icon: string;
  label: string;
  children: ReactNode;
  href: string;
  cta: string;
}) {
  return (
    <section
      style={{
        borderTop: '1px solid var(--rule-strong)',
        paddingTop: 'var(--sp-3)',
        display: 'grid',
        gap: 'var(--sp-3)',
        alignContent: 'start',
      }}
    >
      <span
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-2)',
          color: 'var(--ink-soft)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon={icon} width={18} /> {label}
      </span>
      {children}
      <div>
        <Link href={href} className="btn btn-quiet" style={{ minHeight: 36 }}>
          {cta}
        </Link>
      </div>
    </section>
  );
}

/** A big figure with a quiet line under it — the body of most cards. */
function Figure({ value, sub, tone }: { value: ReactNode; sub?: ReactNode; tone?: 'overdue' }) {
  return (
    <>
      <strong
        style={{
          fontSize: 'var(--text-2xl)',
          color: tone === 'overdue' ? 'var(--stamp-overdue)' : 'var(--ink)',
          fontVariantNumeric: 'tabular-nums lining-nums',
        }}
      >
        {value}
      </strong>
      {sub ? <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{sub}</span> : null}
    </>
  );
}

/**
 * The three reads behind every card: the month summary, the per-renter payment
 * status (counted here only to say how many are in each state), and the pending
 * link-request badge. A failed read leaves its cards showing a dash rather than
 * taking the page down with it.
 */
function useDashboardData() {
  const [summary, setSummary] = useState<ReportSummary | null>(null);
  const [statuses, setStatuses] = useState<PaymentStatusRow[] | null>(null);
  const [expenses, setExpenses] = useState<ExpenseSummary | null>(null);
  const [revenue, setRevenue] = useState<RevenueReport | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    reportsApi
      .summary({ cadence: 'month' }, ac.signal)
      .then(setSummary)
      .catch(() => setSummary(null));
    reportsApi
      .paymentStatus({}, ac.signal)
      .then((r) => setStatuses(r.items ?? []))
      .catch(() => setStatuses(null));
    // This month's spending, with the previous month for the change figure —
    // the same endpoint the expense ledger's summary strip reads.
    expensesApi
      .summary({ cadence: 'month' }, ac.signal)
      .then(setExpenses)
      .catch(() => setExpenses(null));
    // The month's money as a daily series — the revenue and net cards read
    // their figure, their change and their sparkline from this one call.
    reportsApi
      .revenue({ cadence: 'month', bucket: 'day' }, ac.signal)
      .then(setRevenue)
      .catch(() => setRevenue(null));
    return () => ac.abort();
  }, []);

  return { summary, statuses, expenses, revenue };
}

function DashboardBody() {
  const { user, org } = useMe();
  const [firstProperty, setFirstProperty] = useState<Property | null>(null);
  const [prefs, setPrefs] = useState<DashboardPrefs>(() => readDashboardPrefs(null));
  const [customizing, setCustomizing] = useState(false);
  const pending = usePendingLinkRequests();
  const countersign = useReadyToCountersign();
  const { summary, statuses, expenses, revenue } = useDashboardData();

  useEffect(() => {
    const ac = new AbortController();
    brandingApi
      .get(ac.signal)
      .then((r) => setPrefs(readDashboardPrefs(unwrapBranding(r).dashboard_prefs)))
      .catch(() => undefined);
    propertiesApi
      .list({ limit: 1 }, ac.signal)
      .then((r) => setFirstProperty((r.items ?? [])[0] ?? null))
      .catch(() => setFirstProperty(null));
    return () => ac.abort();
  }, []);

  const today = new Date().toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  });

  const renderCard = useCallback(
    (card: DashboardCard) => {
      const a = summary?.assets;
      const p = summary?.period;
      const count = (s: PaymentStatusRow['status']) =>
        (statuses ?? []).filter((r) => r.status === s).length;
      switch (card) {
        case 'assets':
          return (
            <DashCard key={card} icon="solar:buildings-2-linear" label={CARD_LABELS.assets} href="/properties" cta="Properties">
              <Figure
                value={a ? `${a.properties} · ${a.units}` : '—'}
                sub={
                  a
                    ? `${a.properties} propert${a.properties === 1 ? 'y' : 'ies'}, ${a.units} unit${
                        a.units === 1 ? '' : 's'
                      } · ${Math.round((a.occupancy_rate ?? 0) * 100)}% occupied`
                    : 'Waiting for figures.'
                }
              />
            </DashCard>
          );
        case 'renters':
          return (
            <DashCard key={card} icon="solar:users-group-rounded-linear" label={CARD_LABELS.renters} href="/renters" cta="Open renters">
              <Figure
                value={summary ? summary.renters.active : '—'}
                sub={
                  summary
                    ? `${summary.contracts.active} active contract${summary.contracts.active === 1 ? '' : 's'}`
                    : 'Waiting for figures.'
                }
              />
            </DashCard>
          );
        case 'payment_status':
          return (
            <DashCard key={card} icon="solar:clipboard-check-linear" label={CARD_LABELS.payment_status} href="/reports" cta="Full report">
              <Figure
                value={
                  statuses === null ? (
                    '—'
                  ) : (
                    <span style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'baseline', flexWrap: 'wrap' }}>
                      <span style={{ color: 'var(--stamp-paid)' }}>{count('paid')} paid</span>
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-lg)' }}>
                        {count('pending') + count('partial')} pending
                      </span>
                      <span style={{ color: 'var(--stamp-overdue)' }}>{count('overdue')} overdue</span>
                    </span>
                  )
                }
                sub={statuses ? `${statuses.length} renter${statuses.length === 1 ? '' : 's'} on a live contract` : undefined}
              />
            </DashCard>
          );
        case 'collections':
          return (
            <DashCard key={card} icon="solar:wallet-money-linear" label={CARD_LABELS.collections} href="/payments?tab=history" cta="Payment history">
              <Figure
                value={p ? fmtTZS(p.collected) : '—'}
                sub={p ? `of ${fmtTZS(p.expected)} expected · ${fmtTZS(p.outstanding)} outstanding` : 'Waiting for figures.'}
              />
            </DashCard>
          );
        case 'link_requests':
          return (
            <DashCard key={card} icon="solar:inbox-in-linear" label={CARD_LABELS.link_requests} href="/link-requests" cta={pending ? 'Review requests' : 'Open the inbox'}>
              <Figure
                value={pending === null ? '—' : pending === 0 ? 'None waiting' : pendingLabel(pending)}
                sub={
                  pending
                    ? 'A renter scanned a QR code and is waiting to be linked.'
                    : 'Scanned requests land here for approval.'
                }
              />
            </DashCard>
          );
        case 'overdue':
          return (
            <DashCard key={card} icon="solar:bell-bing-linear" label={CARD_LABELS.overdue} href="/payments?tab=overdue" cta={p && p.overdue_count > 0 ? 'Chase them' : 'Open payments'}>
              <Figure
                tone={p && p.overdue_count > 0 ? 'overdue' : undefined}
                value={!p ? '—' : p.overdue_count === 0 ? 'Nothing overdue' : fmtTZS(p.overdue_amount)}
                sub={
                  p
                    ? p.overdue_count === 0
                      ? 'Every payment that has fallen due has been settled.'
                      : `${p.overdue_count} payment${p.overdue_count === 1 ? '' : 's'} past their due date.`
                    : undefined
                }
              />
            </DashCard>
          );
        case 'expenses':
          return (
            <DashCard key={card} icon="solar:bill-list-linear" label={CARD_LABELS.expenses} href="/expenses" cta="Open expenses">
              <Figure
                value={expenses ? fmtTZS(expenses.total.amount) : '—'}
                sub={
                  expenses ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <ChangeMark pct={expenses.change_pct} />
                      <span>
                        vs {fmtTZS(expenses.previous_total?.amount ?? 0)} last month ·{' '}
                        {expenses.total.count} expense{expenses.total.count === 1 ? '' : 's'}
                      </span>
                    </span>
                  ) : (
                    'Waiting for figures.'
                  )
                }
              />
            </DashCard>
          );
        case 'revenue':
          return (
            <DashCard key={card} icon="solar:chart-square-linear" label={CARD_LABELS.revenue} href="/reports" cta="Revenue report">
              <Figure
                value={revenue ? fmtTZS(revenue.totals.collected) : '—'}
                sub={
                  revenue ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <DeltaMark change={revenue.change_pct?.collected ?? null} goodDirection="up" />
                      <span>vs {fmtTZS(revenue.previous_totals?.collected ?? 0)} last month</span>
                    </span>
                  ) : (
                    'Waiting for figures.'
                  )
                }
              />
              {revenue && revenue.buckets.length > 1 ? (
                <Sparkline
                  values={revenue.buckets.map((b) => b.collected)}
                  color={CHART_ROLES.collected}
                  width={180}
                  ariaLabel={`Collected each day this month, ${revenue.buckets.length} days`}
                />
              ) : null}
            </DashCard>
          );
        case 'net_income':
          return (
            <DashCard key={card} icon="solar:banknote-2-linear" label={CARD_LABELS.net_income} href="/reports" cta="Revenue report">
              <Figure
                tone={revenue && revenue.totals.net < 0 ? 'overdue' : undefined}
                value={revenue ? fmtTZS(revenue.totals.net) : '—'}
                sub={
                  revenue ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <DeltaMark change={revenue.change_pct?.net ?? null} goodDirection="up" />
                      <span>
                        collected {fmtTZS(revenue.totals.collected)} less expenses {fmtTZS(revenue.totals.expenses)}
                      </span>
                    </span>
                  ) : (
                    'Waiting for figures.'
                  )
                }
              />
            </DashCard>
          );
        default:
          return null;
      }
    },
    [summary, statuses, pending, expenses, revenue],
  );

  return (
    <>
      <PageHead
        title={org?.name ?? 'Dashboard'}
        lead={`${today} · signed in as ${user?.full_name ?? ''}`}
        actions={
          <>
            <button type="button" className="btn btn-quiet" onClick={() => setCustomizing(true)}>
              <Icon icon="solar:widget-add-linear" width={20} /> Customize
            </button>
            <Link href="/reports" className="btn btn-secondary">
              <Icon icon="solar:chart-square-linear" width={20} /> Reports
            </Link>
          </>
        }
      />

      <hr className="rule rule-strong" />

      {prefs.cards.length === 0 ? (
        <p style={{ marginTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>
          Every card is switched off. Use <strong>Customize</strong> to bring some back.
        </p>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns:
              prefs.layout === 'list' ? '1fr' : 'repeat(auto-fit, minmax(260px, 1fr))',
            gap: 'var(--sp-5)',
            marginTop: 'var(--sp-5)',
          }}
        >
          {prefs.cards.map(renderCard)}
        </div>
      )}

      {summary && summary.contracts.pending_signature > 0 ? (
        <div style={{ marginTop: 'var(--sp-6)' }}>
          <TileRow min={220}>
            <StatTile
              label="Contracts awaiting signature"
              value={summary.contracts.pending_signature}
              sub={<Link href="/contracts">Open contracts</Link>}
            />
            {summary.contracts.expiring > 0 ? (
              <StatTile label="Contracts expiring" value={summary.contracts.expiring} sub="Ending within 30 days" />
            ) : null}
          </TileRow>
        </div>
      ) : null}

      {firstProperty === null ? (
        <p style={{ marginTop: 'var(--sp-6)' }}>
          Nothing has been recorded yet. Add your first property and units, then print the QR stickers so renters
          can connect themselves.
        </p>
      ) : null}

      <h2 style={{ fontSize: 'var(--text-lg)', marginTop: 'var(--sp-7)' }}>Next steps</h2>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
          gap: 'var(--sp-4)',
          marginTop: 'var(--sp-4)',
        }}
      >
        {/* FLOWS flow 3 step 5 — the landlord's turn, once the renter has signed. */}
        <EmptyCard
          icon="solar:document-text-linear"
          title={countersign ? `Ready to countersign: ${countersign}` : 'Contracts'}
          body={
            countersign
              ? 'A renter has signed. Activating countersigns the contract, writes the payment schedule and marks the unit occupied.'
              : 'Contracts you have issued, what each is waiting for, and the terms they are written from.'
          }
          action={
            <Link href="/contracts" className={countersign ? 'btn btn-primary' : 'btn btn-secondary'}>
              {countersign ? 'Countersign now' : 'Open contracts'}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:qr-code-linear"
          title="Print QR codes"
          body="Each unit gets a permanent sticker. A renter scans it and starts their own registration."
          action={
            <Link
              href={firstProperty ? `/properties/${firstProperty.id}/qr` : '/properties'}
              className="btn btn-secondary"
            >
              {firstProperty ? 'Print QR sheet' : 'Set up units'}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:users-group-rounded-linear"
          title="Invite staff"
          body="Give a manager access to record payments and approve renters, without sharing your password."
          action={
            <Link href="/settings" className="btn btn-secondary">
              Invite a manager
            </Link>
          }
        />
        <EmptyCard
          icon="solar:checklist-minimalistic-linear"
          title="Finish setup"
          body="Branding, first property, payment periods, units, contract template and reminders."
          action={
            <Link href="/setup" className="btn btn-secondary">
              Open the wizard
            </Link>
          }
        />
      </div>

      <DashboardCustomize
        open={customizing}
        prefs={prefs}
        onClose={() => setCustomizing(false)}
        onSaved={setPrefs}
      />
    </>
  );
}

export default function DashboardPage() {
  return (
    <>
      <DashboardBody />
    </>
  );
}
