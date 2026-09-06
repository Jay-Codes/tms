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
import { CARD_LABEL_KEYS, DashboardCustomize } from '../../components/DashboardCustomize';
import { ChangeMark } from '../../components/ExpenseBits';
import { StatTile, TileRow } from '../../components/ReportBits';
import { CHART_ROLES, ChangeMark as DeltaMark, Sparkline, formatDateIntl, useLocale, useT } from '@tms/ui';
import { PageHead } from '../../components/PageHead';
import { pendingLabel, usePendingLinkRequests } from '../../components/NavBadges';
import { SmsCreditsBanner } from '../../components/NotificationBits';
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
  const t = useT();
  const locale = useLocale();
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

  const today = formatDateIntl(locale, new Date().toISOString(), {
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
            <DashCard
              key={card}
              icon="solar:buildings-2-linear"
              label={t(CARD_LABEL_KEYS.assets)}
              href="/properties"
              cta={t('nav.properties')}
            >
              <Figure
                value={a ? `${a.properties} · ${a.units}` : '—'}
                sub={
                  a
                    ? t('dash.assets.sub', {
                        properties: t.n('properties.count', a.properties),
                        units: t.n('units.count', a.units),
                        pct: Math.round((a.occupancy_rate ?? 0) * 100),
                      })
                    : t('dash.waiting')
                }
              />
            </DashCard>
          );
        case 'renters':
          return (
            <DashCard
              key={card}
              icon="solar:users-group-rounded-linear"
              label={t(CARD_LABEL_KEYS.renters)}
              href="/renters"
              cta={t('dash.cta.open_renters')}
            >
              <Figure
                value={summary ? summary.renters.active : '—'}
                sub={
                  summary ? t.n('dash.active_contracts', summary.contracts.active) : t('dash.waiting')
                }
              />
            </DashCard>
          );
        case 'payment_status':
          return (
            <DashCard
              key={card}
              icon="solar:clipboard-check-linear"
              label={t(CARD_LABEL_KEYS.payment_status)}
              href="/reports"
              cta={t('dash.cta.full_report')}
            >
              <Figure
                value={
                  statuses === null ? (
                    '—'
                  ) : (
                    <span style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'baseline', flexWrap: 'wrap' }}>
                      <span style={{ color: 'var(--stamp-paid)' }}>
                        {t('dash.status.paid', { count: count('paid') })}
                      </span>
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-lg)' }}>
                        {t('dash.status.pending', { count: count('pending') + count('partial') })}
                      </span>
                      <span style={{ color: 'var(--stamp-overdue)' }}>
                        {t('dash.status.overdue', { count: count('overdue') })}
                      </span>
                    </span>
                  )
                }
                sub={statuses ? t.n('dash.renters_live', statuses.length) : undefined}
              />
            </DashCard>
          );
        case 'collections':
          return (
            <DashCard
              key={card}
              icon="solar:wallet-money-linear"
              label={t(CARD_LABEL_KEYS.collections)}
              href="/payments?tab=history"
              cta={t('dash.cta.payment_history')}
            >
              <Figure
                value={p ? fmtTZS(p.collected) : '—'}
                sub={
                  p
                    ? t('dash.collections.sub', {
                        expected: fmtTZS(p.expected),
                        outstanding: fmtTZS(p.outstanding),
                      })
                    : t('dash.waiting')
                }
              />
            </DashCard>
          );
        case 'link_requests':
          return (
            <DashCard
              key={card}
              icon="solar:inbox-in-linear"
              label={t(CARD_LABEL_KEYS.link_requests)}
              href="/link-requests"
              cta={pending ? t('dash.cta.review_requests') : t('dash.cta.open_inbox')}
            >
              <Figure
                value={pending === null ? '—' : pending === 0 ? t('dash.link_requests.none') : pendingLabel(pending)}
                sub={
                  pending
                    ? t('dash.link_requests.sub_waiting')
                    : t('dash.link_requests.sub_idle')
                }
              />
            </DashCard>
          );
        case 'overdue':
          return (
            <DashCard
              key={card}
              icon="solar:bell-bing-linear"
              label={t(CARD_LABEL_KEYS.overdue)}
              href="/payments?tab=overdue"
              cta={p && p.overdue_count > 0 ? t('dash.cta.chase') : t('dash.cta.open_payments')}
            >
              <Figure
                tone={p && p.overdue_count > 0 ? 'overdue' : undefined}
                value={!p ? '—' : p.overdue_count === 0 ? t('dash.overdue.none') : fmtTZS(p.overdue_amount)}
                sub={
                  p
                    ? p.overdue_count === 0
                      ? t('dash.overdue.settled')
                      : t.n('dash.overdue.count', p.overdue_count)
                    : undefined
                }
              />
            </DashCard>
          );
        case 'expenses':
          return (
            <DashCard
              key={card}
              icon="solar:bill-list-linear"
              label={t(CARD_LABEL_KEYS.expenses)}
              href="/expenses"
              cta={t('dash.cta.open_expenses')}
            >
              <Figure
                value={expenses ? fmtTZS(expenses.total.amount) : '—'}
                sub={
                  expenses ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <ChangeMark pct={expenses.change_pct} />
                      <span>
                        {t('dash.expenses.sub', {
                          amount: fmtTZS(expenses.previous_total?.amount ?? 0),
                          expenses: t.n('dash.expense_count', expenses.total.count),
                        })}
                      </span>
                    </span>
                  ) : (
                    t('dash.waiting')
                  )
                }
              />
            </DashCard>
          );
        case 'revenue':
          return (
            <DashCard
              key={card}
              icon="solar:chart-square-linear"
              label={t(CARD_LABEL_KEYS.revenue)}
              href="/reports"
              cta={t('dash.cta.revenue_report')}
            >
              <Figure
                value={revenue ? fmtTZS(revenue.totals.collected) : '—'}
                sub={
                  revenue ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <DeltaMark change={revenue.change_pct?.collected ?? null} goodDirection="up" />
                      <span>
                        {t('dash.revenue.sub', { amount: fmtTZS(revenue.previous_totals?.collected ?? 0) })}
                      </span>
                    </span>
                  ) : (
                    t('dash.waiting')
                  )
                }
              />
              {revenue && revenue.buckets.length > 1 ? (
                <Sparkline
                  values={revenue.buckets.map((b) => b.collected)}
                  color={CHART_ROLES.collected}
                  width={180}
                  ariaLabel={t('dash.revenue.spark_aria', { count: revenue.buckets.length })}
                />
              ) : null}
            </DashCard>
          );
        case 'net_income':
          return (
            <DashCard
              key={card}
              icon="solar:banknote-2-linear"
              label={t(CARD_LABEL_KEYS.net_income)}
              href="/reports"
              cta={t('dash.cta.revenue_report')}
            >
              <Figure
                tone={revenue && revenue.totals.net < 0 ? 'overdue' : undefined}
                value={revenue ? fmtTZS(revenue.totals.net) : '—'}
                sub={
                  revenue ? (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                      <DeltaMark change={revenue.change_pct?.net ?? null} goodDirection="up" />
                      <span>
                        {t('dash.net.sub', {
                          collected: fmtTZS(revenue.totals.collected),
                          expenses: fmtTZS(revenue.totals.expenses),
                        })}
                      </span>
                    </span>
                  ) : (
                    t('dash.waiting')
                  )
                }
              />
            </DashCard>
          );
        default:
          return null;
      }
    },
    [summary, statuses, pending, expenses, revenue, t],
  );

  return (
    <>
      <PageHead
        title={org?.name ?? t('nav.dashboard')}
        lead={t('dash.lead', { date: today, name: user?.full_name ?? '' })}
        actions={
          <>
            <button type="button" className="btn btn-quiet" onClick={() => setCustomizing(true)}>
              <Icon icon="solar:widget-add-linear" width={20} /> {t('dash.customize')}
            </button>
            <Link href="/reports" className="btn btn-secondary">
              <Icon icon="solar:chart-square-linear" width={20} /> {t('nav.reports')}
            </Link>
          </>
        }
      />

      <hr className="rule rule-strong" />

      {/* Phase 14 — a persistent strip, not a card: it says nothing at all
          unless the balance is low or messages are being held. */}
      <SmsCreditsBanner />

      {prefs.cards.length === 0 ? (
        <p style={{ marginTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>
          {t('dash.all_off').split('{action}')[0]}
          <strong>{t('dash.customize')}</strong>
          {t('dash.all_off').split('{action}')[1] ?? ''}
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
              label={t('dash.tile.pending_signature')}
              value={summary.contracts.pending_signature}
              sub={<Link href="/contracts">{t('dash.open_contracts')}</Link>}
            />
            {summary.contracts.expiring > 0 ? (
              <StatTile
                label={t('dash.tile.expiring')}
                value={summary.contracts.expiring}
                sub={t('dash.tile.expiring_sub')}
              />
            ) : null}
          </TileRow>
        </div>
      ) : null}

      {firstProperty === null ? (
        <p style={{ marginTop: 'var(--sp-6)' }}>
          {t('dash.empty_org')}
        </p>
      ) : null}

      <h2 style={{ fontSize: 'var(--text-lg)', marginTop: 'var(--sp-7)' }}>{t('dash.next_steps')}</h2>

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
          title={countersign ? t('dash.next.contracts_ready', { count: countersign }) : t('nav.contracts')}
          body={countersign ? t('dash.next.contracts_ready_body') : t('dash.next.contracts_body')}
          action={
            <Link href="/contracts" className={countersign ? 'btn btn-primary' : 'btn btn-secondary'}>
              {countersign ? t('dash.next.countersign_now') : t('dash.open_contracts')}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:qr-code-linear"
          title={t('dash.next.qr_title')}
          body={t('dash.next.qr_body')}
          action={
            <Link
              href={firstProperty ? `/properties/${firstProperty.id}/qr` : '/properties'}
              className="btn btn-secondary"
            >
              {firstProperty ? t('qr.print_sheet') : t('dash.next.qr_setup')}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:users-group-rounded-linear"
          title={t('dash.next.staff_title')}
          body={t('dash.next.staff_body')}
          action={
            <Link href="/settings" className="btn btn-secondary">
              {t('dash.next.staff_action')}
            </Link>
          }
        />
        <EmptyCard
          icon="solar:checklist-minimalistic-linear"
          title={t('dash.next.setup_title')}
          body={t('dash.next.setup_body')}
          action={
            <Link href="/setup" className="btn btn-secondary">
              {t('dash.next.setup_action')}
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
